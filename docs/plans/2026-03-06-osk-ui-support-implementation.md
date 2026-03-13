# OSK UI Support Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add Open Source Kafka (OSK) cluster visualization to the KCP web UI alongside MSK clusters.

**Architecture:** Unified source model with discriminated union on backend (`ProcessedSource[]`) and frontend (TypeScript type guards). Separate UI sections for MSK and OSK in sidebar. State file always initializes both source types.

**Tech Stack:** Go 1.25, TypeScript, React 19, Zustand, Playwright, Vite

---

## Task 1: Fix OSK Credentials Validation

**Files:**
- Modify: `internal/types/osk_credentials.go:90-97`
- Test: Manual (existing tests should pass)

**Step 1: Remove auth method requirement**

Edit `internal/types/osk_credentials.go`, remove lines 90-97:

```go
// REMOVE THESE LINES (90-97):
if len(enabledMethods) == 0 {
    errs = append(errs, fmt.Errorf("%s (id=%s): no authentication method enabled", clusterRef, cluster.ID))
}
```

This allows clusters with all auth methods `use: false` to pass validation (matching MSK behavior).

**Step 2: Run tests to verify**

```bash
go test ./internal/types/... -v -run TestOSKCredentials
```

Expected: PASS - validation tests should still pass, but now allow all auth methods disabled

**Step 3: Test with sample credentials**

Create `test/credentials/osk-credentials-disabled.yaml`:

```yaml
clusters:
  - id: disabled-cluster
    bootstrap_servers:
      - localhost:9092
    auth_method:
      sasl_scram:
        use: false
      tls:
        use: false
      unauthenticated_plaintext:
        use: false
```

```bash
# This should now load successfully
go run main.go scan clusters --source-type osk --credentials-file test/credentials/osk-credentials-disabled.yaml --dry-run
```

Expected: No validation errors

**Step 4: Commit**

```bash
git add internal/types/osk_credentials.go test/credentials/osk-credentials-disabled.yaml
git commit -m "fix: allow OSK clusters with all auth methods disabled

Removes validation error when no auth methods are enabled, matching
MSK behavior. Clusters with use: false on all auth methods are now
skipped during scan rather than causing validation failure.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 2: Update State Initialization

**Files:**
- Modify: `internal/types/state.go:65-96` (NewStateFrom function)
- Test: `internal/types/state_test.go` (new test)

**Step 1: Write test for state initialization**

Create test in `internal/types/state_test.go`:

```go
package types

import (
	"testing"
)

func TestNewStateFrom_AlwaysInitializesBothSources(t *testing.T) {
	// Test nil input
	state := NewStateFrom(nil)
	if state.MSKSources == nil {
		t.Error("MSKSources should be initialized, got nil")
	}
	if state.OSKSources == nil {
		t.Error("OSKSources should be initialized, got nil")
	}
	if len(state.MSKSources.Regions) != 0 {
		t.Errorf("MSKSources.Regions should be empty, got %d items", len(state.MSKSources.Regions))
	}
	if len(state.OSKSources.Clusters) != 0 {
		t.Errorf("OSKSources.Clusters should be empty, got %d items", len(state.OSKSources.Clusters))
	}
}

func TestNewStateFrom_PreservesExistingOSKData(t *testing.T) {
	// Create state with OSK data
	existingState := &State{
		OSKSources: &OSKSourcesState{
			Clusters: []OSKDiscoveredCluster{
				{ID: "test-cluster"},
			},
		},
	}

	newState := NewStateFrom(existingState)
	if newState.OSKSources == nil {
		t.Fatal("OSKSources should be preserved")
	}
	if len(newState.OSKSources.Clusters) != 1 {
		t.Errorf("Expected 1 OSK cluster, got %d", len(newState.OSKSources.Clusters))
	}
	if newState.MSKSources == nil {
		t.Error("MSKSources should be initialized even when copying OSK data")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/types/... -v -run TestNewStateFrom
```

Expected: FAIL - OSKSources is nil

**Step 3: Update NewStateFrom implementation**

Modify `internal/types/state.go` lines 65-96:

```go
func NewStateFrom(fromState *State) *State {
	// Always create with fresh metadata for the current discovery run
	workingState := &State{
		KcpBuildInfo: KcpBuildInfo{
			Version: build_info.Version,
			Commit:  build_info.Commit,
			Date:    build_info.Date,
		},
		Timestamp: time.Now(),
	}

	if fromState == nil {
		// Initialize both sources with empty arrays
		workingState.MSKSources = &MSKSourcesState{
			Regions: []DiscoveredRegion{},
		}
		workingState.OSKSources = &OSKSourcesState{
			Clusters: []OSKDiscoveredCluster{},
		}
	} else {
		// Copy existing MSK data or initialize empty
		if fromState.MSKSources != nil {
			mskSources := &MSKSourcesState{
				Regions: make([]DiscoveredRegion, len(fromState.MSKSources.Regions)),
			}
			copy(mskSources.Regions, fromState.MSKSources.Regions)
			workingState.MSKSources = mskSources
		} else {
			workingState.MSKSources = &MSKSourcesState{
				Regions: []DiscoveredRegion{},
			}
		}

		// Copy existing OSK data or initialize empty
		if fromState.OSKSources != nil {
			workingState.OSKSources = fromState.OSKSources
		} else {
			workingState.OSKSources = &OSKSourcesState{
				Clusters: []OSKDiscoveredCluster{},
			}
		}
	}

	return workingState
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/types/... -v -run TestNewStateFrom
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/types/state.go internal/types/state_test.go
git commit -m "feat: always initialize both MSK and OSK sources in state

Ensures state files always have both msk_sources and osk_sources
initialized (even if empty), eliminating nil checks and ensuring
consistent structure across MSK-only, OSK-only, and mixed scenarios.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 3: Add ProcessedSource Types

**Files:**
- Modify: `internal/types/state.go` (add new types after line 755)
- Test: `internal/types/state_test.go` (add type test)

**Step 1: Write test for ProcessedSource types**

Add to `internal/types/state_test.go`:

```go
func TestProcessedSource_TypeDiscrimination(t *testing.T) {
	// Test MSK source
	mskSource := ProcessedSource{
		Type: SourceTypeMSK,
		MSKData: &ProcessedMSKSource{
			Regions: []ProcessedRegion{},
		},
	}
	if mskSource.Type != SourceTypeMSK {
		t.Errorf("Expected MSK type, got %s", mskSource.Type)
	}
	if mskSource.MSKData == nil {
		t.Error("MSKData should not be nil for MSK source")
	}

	// Test OSK source
	oskSource := ProcessedSource{
		Type: SourceTypeOSK,
		OSKData: &ProcessedOSKSource{
			Clusters: []ProcessedOSKCluster{},
		},
	}
	if oskSource.Type != SourceTypeOSK {
		t.Errorf("Expected OSK type, got %s", oskSource.Type)
	}
	if oskSource.OSKData == nil {
		t.Error("OSKData should not be nil for OSK source")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/types/... -v -run TestProcessedSource
```

Expected: FAIL - types not defined

**Step 3: Add ProcessedSource types**

Add to `internal/types/state.go` after line 755:

```go
// ProcessedSource represents a unified source (MSK or OSK) with discriminated union
type ProcessedSource struct {
	Type    SourceType           `json:"type"`
	MSKData *ProcessedMSKSource  `json:"msk_data,omitempty"`
	OSKData *ProcessedOSKSource  `json:"osk_data,omitempty"`
}

// ProcessedMSKSource contains processed MSK data (regions)
type ProcessedMSKSource struct {
	Regions []ProcessedRegion `json:"regions"`
}

// ProcessedOSKSource contains processed OSK data (flat cluster array)
type ProcessedOSKSource struct {
	Clusters []ProcessedOSKCluster `json:"clusters"`
}

// ProcessedOSKCluster represents an OSK cluster in the API response
type ProcessedOSKCluster struct {
	ID                          string                      `json:"id"`
	BootstrapServers            []string                    `json:"bootstrap_servers"`
	KafkaAdminClientInformation KafkaAdminClientInformation `json:"kafka_admin_client_information"`
	DiscoveredClients           []DiscoveredClient          `json:"discovered_clients"`
	Metadata                    OSKClusterMetadata          `json:"metadata"`
}
```

**Step 4: Update ProcessedState to use sources array**

Modify `internal/types/state.go` around line 710:

```go
// ProcessedState is the API response format with flattened/processed data
type ProcessedState struct {
	Sources          []ProcessedSource           `json:"sources"`           // NEW: unified source array
	SchemaRegistries []SchemaRegistryInformation `json:"schema_registries"`
	KcpBuildInfo     interface{}                 `json:"kcp_build_info,omitempty"`
	Timestamp        time.Time                   `json:"timestamp"`
}
```

**Step 5: Run test to verify it passes**

```bash
go test ./internal/types/... -v -run TestProcessedSource
```

Expected: PASS

**Step 6: Commit**

```bash
git add internal/types/state.go internal/types/state_test.go
git commit -m "feat: add ProcessedSource types for unified source model

Introduces discriminated union pattern for API responses, allowing
MSK and OSK sources to coexist in a single sources array with
type-based discrimination.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 4: Update ProcessState Logic

**Files:**
- Modify: `internal/services/report/report_service.go:19-63`
- Test: Manual (will test via UI later)

**Step 1: Update ProcessState to use unified source model**

Replace the `ProcessState` function in `internal/services/report/report_service.go`:

```go
func (rs *ReportService) ProcessState(state types.State) types.ProcessedState {
	sources := []types.ProcessedSource{}

	// Process MSK if present
	if state.MSKSources != nil && len(state.MSKSources.Regions) > 0 {
		processedRegions := []types.ProcessedRegion{}

		for _, region := range state.MSKSources.Regions {
			// Flatten cost data from nested AWS Cost Explorer format
			processedCosts := rs.flattenCosts(region)

			// Process each cluster's metrics
			processedClusters := []types.ProcessedCluster{}
			for _, cluster := range region.Clusters {
				// Flatten metrics data from nested CloudWatch format
				processedMetrics := rs.flattenMetrics(cluster)

				processedClusters = append(processedClusters, types.ProcessedCluster{
					Name:                        cluster.Name,
					Arn:                         cluster.Arn,
					Region:                      cluster.Region,
					ClusterMetrics:              processedMetrics,
					AWSClientInformation:        cluster.AWSClientInformation,
					KafkaAdminClientInformation: cluster.KafkaAdminClientInformation,
					DiscoveredClients:           cluster.DiscoveredClients,
				})
			}

			processedRegions = append(processedRegions, types.ProcessedRegion{
				Name:           region.Name,
				Configurations: region.Configurations,
				Costs:          processedCosts,
				Clusters:       processedClusters,
			})
		}

		mskSource := types.ProcessedSource{
			Type: types.SourceTypeMSK,
			MSKData: &types.ProcessedMSKSource{
				Regions: processedRegions,
			},
		}
		sources = append(sources, mskSource)
	}

	// Process OSK if present
	if state.OSKSources != nil && len(state.OSKSources.Clusters) > 0 {
		processedOSKClusters := []types.ProcessedOSKCluster{}

		for _, cluster := range state.OSKSources.Clusters {
			processedOSKClusters = append(processedOSKClusters, types.ProcessedOSKCluster{
				ID:                          cluster.ID,
				BootstrapServers:            cluster.BootstrapServers,
				KafkaAdminClientInformation: cluster.KafkaAdminClientInformation,
				DiscoveredClients:           cluster.DiscoveredClients,
				Metadata:                    cluster.Metadata,
			})
		}

		oskSource := types.ProcessedSource{
			Type: types.SourceTypeOSK,
			OSKData: &types.ProcessedOSKSource{
				Clusters: processedOSKClusters,
			},
		}
		sources = append(sources, oskSource)
	}

	// Return the processed state with unified sources
	processedState := types.ProcessedState{
		Sources:          sources,
		SchemaRegistries: state.SchemaRegistries,
		KcpBuildInfo:     state.KcpBuildInfo,
		Timestamp:        state.Timestamp,
	}

	return processedState
}
```

**Step 2: Build to verify compilation**

```bash
make build
```

Expected: Build succeeds

**Step 3: Test with sample state file**

```bash
# Start UI and upload a state file with both MSK and OSK
make build
./bin/kcp ui --port 5556
```

Open browser, upload state file, check network tab for `/upload-state` response - should show `sources` array.

**Step 4: Commit**

```bash
git add internal/services/report/report_service.go
git commit -m "feat: update ProcessState to use unified source model

Refactors ProcessState to return sources array with discriminated
MSK and OSK sources, enabling frontend to handle both source types
via a single unified structure.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 5: Add Frontend TypeScript Types

**Files:**
- Create: `cmd/ui/frontend/src/types/api/sources.ts`
- Modify: `cmd/ui/frontend/src/types/api/state.ts`
- Modify: `cmd/ui/frontend/src/types/index.ts`

**Step 1: Create source types file**

Create `cmd/ui/frontend/src/types/api/sources.ts`:

```typescript
import type { KafkaAdminInfo, DiscoveredClient } from '../aws/msk'

export type SourceType = 'msk' | 'osk'

export interface ProcessedSource {
  type: SourceType
  msk_data?: ProcessedMSKSource
  osk_data?: ProcessedOSKSource
}

export interface ProcessedMSKSource {
  regions: import('./state').Region[]
}

export interface ProcessedOSKSource {
  clusters: OSKCluster[]
}

export interface OSKCluster {
  id: string
  bootstrap_servers: string[]
  kafka_admin_client_information: KafkaAdminInfo
  discovered_clients: DiscoveredClient[]
  metadata: OSKClusterMetadata
}

export interface OSKClusterMetadata {
  environment?: string
  location?: string
  kafka_version?: string
  labels?: Record<string, string>
  last_scanned: string
}
```

**Step 2: Update ProcessedState in state.ts**

Modify `cmd/ui/frontend/src/types/api/state.ts`:

```typescript
import type { ProcessedSource } from './sources'

/**
 * Processed state structure from backend
 */
export interface ProcessedState {
  sources: ProcessedSource[]  // CHANGED from regions
  schema_registries?: SchemaRegistry[]
  kcp_build_info?: unknown
  timestamp?: string
}

// Keep other types (SchemaRegistry, etc.) unchanged
```

**Step 3: Update index.ts exports**

Add to `cmd/ui/frontend/src/types/index.ts`:

```typescript
// Re-export source types
export type {
  SourceType,
  ProcessedSource,
  ProcessedMSKSource,
  ProcessedOSKSource,
  OSKCluster,
  OSKClusterMetadata,
} from './api/sources'
```

**Step 4: Run type check**

```bash
cd cmd/ui/frontend
npm run type-check
```

Expected: Errors (frontend code still expects old structure)

**Step 5: Commit types only**

```bash
git add cmd/ui/frontend/src/types/
git commit -m "feat: add TypeScript types for unified source model

Introduces ProcessedSource discriminated union and OSK cluster types
to support both MSK and OSK sources in the frontend.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 6: Add Type Guards and Utilities

**Files:**
- Create: `cmd/ui/frontend/src/lib/sourceUtils.ts`
- Test: Manual (TypeScript compilation)

**Step 1: Create source utility functions**

Create `cmd/ui/frontend/src/lib/sourceUtils.ts`:

```typescript
import type { ProcessedSource, ProcessedMSKSource, ProcessedOSKSource } from '@/types'

/**
 * Type guard to check if a source is MSK
 */
export function isMSKSource(
  source: ProcessedSource
): source is ProcessedSource & { msk_data: ProcessedMSKSource } {
  return source.type === 'msk' && source.msk_data !== undefined
}

/**
 * Type guard to check if a source is OSK
 */
export function isOSKSource(
  source: ProcessedSource
): source is ProcessedSource & { osk_data: ProcessedOSKSource } {
  return source.type === 'osk' && source.osk_data !== undefined
}

/**
 * Get MSK source from sources array (if present)
 */
export function getMSKSource(sources: ProcessedSource[]): ProcessedMSKSource | null {
  const mskSource = sources.find(isMSKSource)
  return mskSource?.msk_data ?? null
}

/**
 * Get OSK source from sources array (if present)
 */
export function getOSKSource(sources: ProcessedSource[]): ProcessedOSKSource | null {
  const oskSource = sources.find(isOSKSource)
  return oskSource?.osk_data ?? null
}
```

**Step 2: Run type check**

```bash
cd cmd/ui/frontend
npm run type-check
```

Expected: File compiles without errors

**Step 3: Commit**

```bash
git add cmd/ui/frontend/src/lib/sourceUtils.ts
git commit -m "feat: add type guards and utilities for source handling

Provides type-safe functions to discriminate between MSK and OSK
sources in the frontend, enabling proper TypeScript narrowing.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 7: Update Zustand Store

**Files:**
- Modify: `cmd/ui/frontend/src/stores/store.ts`

**Step 1: Add OSK-related state and actions**

Update `cmd/ui/frontend/src/stores/store.ts`:

```typescript
import { create } from 'zustand'
import type { ProcessedState, Region, SourceType } from '@/types'

interface AppState {
  // State data
  processedState: ProcessedState | null
  setProcessedState: (state: ProcessedState) => void

  // View selection
  selectedView: 'summary' | 'region' | 'cluster' | 'schema-registries'
  selectedSourceType: SourceType | null  // NEW
  selectedRegionName: string | null
  selectedClusterArn: string | null
  selectedOSKClusterId: string | null  // NEW

  // MSK actions
  selectSummary: () => void
  selectRegion: (regionName: string) => void
  selectCluster: (regionName: string, clusterArn: string) => void

  // OSK actions (NEW)
  selectOSKCluster: (clusterId: string) => void

  // Schema registry actions
  selectSchemaRegistries: () => void

  // Date filters (existing - for Summary view)
  summaryStartDate: Date | undefined
  summaryEndDate: Date | undefined
  setSummaryStartDate: (date: Date | undefined) => void
  setSummaryEndDate: (date: Date | undefined) => void

  // Region date filters (existing)
  regionDateFilters: Record<string, { startDate?: Date; endDate?: Date }>
  setRegionDateFilter: (regionName: string, startDate?: Date, endDate?: Date) => void

  // Cluster date filters (existing)
  clusterDateFilters: Record<string, { startDate?: Date; endDate?: Date }>
  setClusterDateFilter: (clusterArn: string, startDate?: Date, endDate?: Date) => void
}

export const useAppStore = create<AppState>((set) => ({
  // State
  processedState: null,
  setProcessedState: (state) => set({ processedState: state }),

  // View selection
  selectedView: 'summary',
  selectedSourceType: null,
  selectedRegionName: null,
  selectedClusterArn: null,
  selectedOSKClusterId: null,

  // MSK actions
  selectSummary: () =>
    set({
      selectedView: 'summary',
      selectedSourceType: 'msk',
      selectedRegionName: null,
      selectedClusterArn: null,
      selectedOSKClusterId: null,
    }),

  selectRegion: (regionName) =>
    set({
      selectedView: 'region',
      selectedSourceType: 'msk',
      selectedRegionName: regionName,
      selectedClusterArn: null,
      selectedOSKClusterId: null,
    }),

  selectCluster: (regionName, clusterArn) =>
    set({
      selectedView: 'cluster',
      selectedSourceType: 'msk',
      selectedRegionName: regionName,
      selectedClusterArn: clusterArn,
      selectedOSKClusterId: null,
    }),

  // OSK actions (NEW)
  selectOSKCluster: (clusterId) =>
    set({
      selectedView: 'cluster',
      selectedSourceType: 'osk',
      selectedOSKClusterId: clusterId,
      selectedRegionName: null,
      selectedClusterArn: null,
    }),

  selectSchemaRegistries: () =>
    set({
      selectedView: 'schema-registries',
      selectedSourceType: null,
      selectedRegionName: null,
      selectedClusterArn: null,
      selectedOSKClusterId: null,
    }),

  // Date filters (existing - keep unchanged)
  summaryStartDate: undefined,
  summaryEndDate: undefined,
  setSummaryStartDate: (date) => set({ summaryStartDate: date }),
  setSummaryEndDate: (date) => set({ summaryEndDate: date }),

  regionDateFilters: {},
  setRegionDateFilter: (regionName, startDate, endDate) =>
    set((state) => ({
      regionDateFilters: {
        ...state.regionDateFilters,
        [regionName]: { startDate, endDate },
      },
    })),

  clusterDateFilters: {},
  setClusterDateFilter: (clusterArn, startDate, endDate) =>
    set((state) => ({
      clusterDateFilters: {
        ...state.clusterDateFilters,
        [clusterArn]: { startDate, endDate },
      },
    })),
}))

// Convenience hooks (existing - keep unchanged)
export const useRegions = () => {
  const processedState = useAppStore((state) => state.processedState)
  // NOTE: This will need updating in next task
  return processedState?.regions ?? []
}

export const useSummaryDateFilters = () => {
  const startDate = useAppStore((state) => state.summaryStartDate)
  const endDate = useAppStore((state) => state.summaryEndDate)
  const setStartDate = useAppStore((state) => state.setSummaryStartDate)
  const setEndDate = useAppStore((state) => state.setSummaryEndDate)
  return { startDate, endDate, setStartDate, setEndDate }
}
```

**Step 2: Run type check**

```bash
cd cmd/ui/frontend
npm run type-check
```

Expected: Errors (components still expect old structure)

**Step 3: Commit**

```bash
git add cmd/ui/frontend/src/stores/store.ts
git commit -m "feat: update Zustand store for OSK support

Adds selectedSourceType and selectedOSKClusterId state fields,
plus selectOSKCluster action for navigating to OSK cluster views.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 8: Update useRegions Hook

**Files:**
- Modify: `cmd/ui/frontend/src/stores/store.ts` (bottom of file)

**Step 1: Update useRegions to use new source model**

Replace the `useRegions` hook at the bottom of `store.ts`:

```typescript
// Convenience hooks
export const useRegions = () => {
  const processedState = useAppStore((state) => state.processedState)
  const mskSource = processedState?.sources.find(
    (s) => s.type === 'msk' && s.msk_data !== undefined
  )
  return mskSource?.msk_data?.regions ?? []
}
```

**Step 2: Run type check**

```bash
cd cmd/ui/frontend
npm run type-check
```

Expected: Still errors (components not updated yet)

**Step 3: Commit**

```bash
git add cmd/ui/frontend/src/stores/store.ts
git commit -m "fix: update useRegions hook for unified source model

Updates convenience hook to extract MSK regions from sources array
instead of accessing regions directly on state.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 9: Create MSKSourceSection Component

**Files:**
- Create: `cmd/ui/frontend/src/components/explore/sidebar/MSKSourceSection.tsx`

**Step 1: Create MSK source section component**

Create `cmd/ui/frontend/src/components/explore/sidebar/MSKSourceSection.tsx`:

```typescript
import type { Region } from '@/types'
import { useAppStore } from '@/stores/store'
import { getClusterArn } from '@/lib/clusterUtils'

interface MSKSourceSectionProps {
  regions: Region[]
}

export const MSKSourceSection = ({ regions }: MSKSourceSectionProps) => {
  const selectedView = useAppStore((state) => state.selectedView)
  const selectedSourceType = useAppStore((state) => state.selectedSourceType)
  const selectedRegionName = useAppStore((state) => state.selectedRegionName)
  const selectedClusterArn = useAppStore((state) => state.selectedClusterArn)

  const selectSummary = useAppStore((state) => state.selectSummary)
  const selectRegion = useAppStore((state) => state.selectRegion)
  const selectCluster = useAppStore((state) => state.selectCluster)

  const isSummarySelected = selectedView === 'summary' && selectedSourceType === 'msk'

  return (
    <div className="space-y-3">
      {/* Section Header */}
      <h3 className="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider px-2">
        AWS MSK
      </h3>

      {/* Summary Button */}
      <button
        onClick={selectSummary}
        className={`w-full text-left flex items-center justify-between p-3 rounded-lg transition-colors ${
          isSummarySelected
            ? 'bg-blue-100 dark:bg-accent/20 border border-blue-200 dark:border-accent'
            : 'hover:bg-gray-100 dark:hover:bg-gray-600'
        }`}
      >
        <div className="flex items-center space-x-2 min-w-0 flex-1">
          <div
            className={`w-2 h-2 rounded-full flex-shrink-0 ${
              isSummarySelected ? 'bg-blue-600' : 'bg-gray-500'
            }`}
          />
          <h4
            className={`text-sm whitespace-nowrap ${
              isSummarySelected
                ? 'text-blue-900 dark:text-accent'
                : 'text-gray-800 dark:text-gray-200'
            }`}
          >
            Summary
          </h4>
        </div>
      </button>

      {/* Regions List */}
      <div className="ml-4 space-y-2">
        {regions.map((region) => {
          const isRegionSelected = selectedView === 'region' && selectedRegionName === region.name

          return (
            <div key={region.name} className="space-y-1">
              <button
                onClick={() => selectRegion(region.name)}
                className={`w-full text-left flex items-center justify-between p-2 rounded-md transition-colors ${
                  isRegionSelected
                    ? 'bg-blue-100 dark:bg-accent/20 border border-blue-200 dark:border-accent'
                    : 'hover:bg-gray-100 dark:hover:bg-gray-600'
                }`}
              >
                <div className="flex items-center space-x-2 min-w-0 flex-1">
                  <div
                    className={`w-1.5 h-1.5 rounded-full flex-shrink-0 ${
                      isRegionSelected ? 'bg-blue-500' : 'bg-blue-400'
                    }`}
                  />
                  <h5
                    className={`text-sm font-medium whitespace-nowrap ${
                      isRegionSelected
                        ? 'text-blue-900 dark:text-accent'
                        : 'text-gray-700 dark:text-gray-300'
                    }`}
                  >
                    {region.name}
                  </h5>
                </div>
              </button>

              {/* Clusters under each region */}
              <div className="ml-4 space-y-1">
                {(region.clusters || [])
                  .filter(
                    (cluster) =>
                      cluster.aws_client_information?.msk_cluster_config?.Provisioned
                  )
                  .map((cluster) => {
                    const clusterArn = getClusterArn(cluster)
                    const isSelected =
                      selectedView === 'cluster' && selectedClusterArn === clusterArn

                    return (
                      <button
                        key={cluster.name}
                        onClick={() => selectCluster(region.name, clusterArn!)}
                        className={`w-full text-left px-2 py-1 text-xs rounded-sm transition-colors ${
                          isSelected
                            ? 'bg-blue-100 dark:bg-accent/20 text-blue-900 dark:text-accent border border-blue-200 dark:border-accent'
                            : 'text-gray-600 dark:text-gray-300 hover:text-gray-900 dark:hover:text-gray-100 hover:bg-gray-100 dark:hover:bg-gray-600'
                        }`}
                      >
                        <div className="flex items-center space-x-1">
                          <div
                            className={`w-1 h-1 rounded-full flex-shrink-0 ${
                              isSelected ? 'bg-blue-500' : 'bg-gray-400'
                            }`}
                          />
                          <span className="truncate">{cluster.name}</span>
                        </div>
                      </button>
                    )
                  })}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
```

**Step 2: Run type check**

```bash
cd cmd/ui/frontend
npm run type-check
```

Expected: Component compiles

**Step 3: Commit**

```bash
git add cmd/ui/frontend/src/components/explore/sidebar/MSKSourceSection.tsx
git commit -m "feat: create MSKSourceSection component

Extracts MSK-specific sidebar logic into dedicated component,
including Summary button, regions, and cluster hierarchy.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 10: Create OSKSourceSection Component

**Files:**
- Create: `cmd/ui/frontend/src/components/explore/sidebar/OSKSourceSection.tsx`

**Step 1: Create OSK source section component**

Create `cmd/ui/frontend/src/components/explore/sidebar/OSKSourceSection.tsx`:

```typescript
import type { OSKCluster } from '@/types'
import { useAppStore } from '@/stores/store'

interface OSKSourceSectionProps {
  clusters: OSKCluster[]
}

export const OSKSourceSection = ({ clusters }: OSKSourceSectionProps) => {
  const selectedView = useAppStore((state) => state.selectedView)
  const selectedOSKClusterId = useAppStore((state) => state.selectedOSKClusterId)
  const selectOSKCluster = useAppStore((state) => state.selectOSKCluster)

  return (
    <div className="space-y-3">
      {/* Section Header */}
      <h3 className="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider px-2">
        Open Source Kafka
      </h3>

      {/* OSK Clusters - Flat List */}
      <div className="ml-4 space-y-1">
        {clusters.map((cluster) => {
          const isSelected = selectedView === 'cluster' && selectedOSKClusterId === cluster.id

          return (
            <button
              key={cluster.id}
              onClick={() => selectOSKCluster(cluster.id)}
              className={`w-full text-left px-2 py-1 text-xs rounded-sm transition-colors ${
                isSelected
                  ? 'bg-blue-100 dark:bg-accent/20 text-blue-900 dark:text-accent border border-blue-200 dark:border-accent'
                  : 'text-gray-600 dark:text-gray-300 hover:text-gray-900 dark:hover:text-gray-100 hover:bg-gray-100 dark:hover:bg-gray-600'
              }`}
            >
              <div className="flex items-center space-x-1">
                <div
                  className={`w-1 h-1 rounded-full flex-shrink-0 ${
                    isSelected ? 'bg-blue-500' : 'bg-gray-400'
                  }`}
                />
                <span className="truncate">{cluster.id}</span>
              </div>
            </button>
          )
        })}
      </div>
    </div>
  )
}
```

**Step 2: Run type check**

```bash
cd cmd/ui/frontend
npm run type-check
```

Expected: Component compiles

**Step 3: Commit**

```bash
git add cmd/ui/frontend/src/components/explore/sidebar/OSKSourceSection.tsx
git commit -m "feat: create OSKSourceSection component

Adds flat list of OSK clusters in sidebar with selection handling.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 11: Update Sidebar Component

**Files:**
- Modify: `cmd/ui/frontend/src/components/explore/Sidebar.tsx`

**Step 1: Update Sidebar to use new components**

Replace content of `cmd/ui/frontend/src/components/explore/Sidebar.tsx`:

```typescript
import { useAppStore } from '@/stores/store'
import { isMSKSource, isOSKSource } from '@/lib/sourceUtils'
import { MSKSourceSection } from './sidebar/MSKSourceSection'
import { OSKSourceSection } from './sidebar/OSKSourceSection'

export const Sidebar = () => {
  const processedState = useAppStore((state) => state.processedState)
  const selectedView = useAppStore((state) => state.selectedView)
  const selectSchemaRegistries = useAppStore((state) => state.selectSchemaRegistries)

  // Extract sources
  const mskSource = processedState?.sources.find(isMSKSource)
  const oskSource = processedState?.sources.find(isOSKSource)

  const hasMSK = mskSource !== undefined
  const hasOSK = oskSource !== undefined

  return (
    <div className="h-full flex flex-col">
      <div className="p-4 pb-0">
        <p className="text-sm text-gray-600 dark:text-gray-400 mt-1">
          Explore your Kafka infrastructure
        </p>
      </div>

      <div className="flex-1 overflow-y-auto p-4">
        {/* Show MSK section if MSK data exists */}
        {hasMSK && mskSource.msk_data && (
          <div className="mb-6">
            <MSKSourceSection regions={mskSource.msk_data.regions} />
          </div>
        )}

        {/* Show OSK section if OSK data exists */}
        {hasOSK && oskSource.osk_data && (
          <div className="mb-6">
            <OSKSourceSection clusters={oskSource.osk_data.clusters} />
          </div>
        )}

        {/* Empty state if no sources */}
        {!hasMSK && !hasOSK && (
          <div className="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-border rounded-lg p-4">
            <p className="text-sm text-yellow-800 dark:text-yellow-200">
              No clusters available. Please upload a KCP state file to explore your infrastructure.
            </p>
          </div>
        )}
      </div>

      {/* Schema Registries Section */}
      <div className="border-t border-gray-200 dark:border-border p-4">
        <div className="space-y-2">
          <p className="text-sm text-gray-600 dark:text-gray-400 px-2">
            Explore Schema Registries
          </p>

          <button
            onClick={selectSchemaRegistries}
            className={`w-full text-left flex items-center justify-between p-3 rounded-lg transition-colors ${
              selectedView === 'schema-registries'
                ? 'bg-blue-100 dark:bg-accent/20 border border-blue-200 dark:border-accent'
                : 'hover:bg-gray-100 dark:hover:bg-gray-600'
            }`}
          >
            <div className="flex items-center space-x-2 min-w-0 flex-1">
              <div
                className={`w-2 h-2 rounded-full flex-shrink-0 ${
                  selectedView === 'schema-registries' ? 'bg-blue-600' : 'bg-gray-500'
                }`}
              />
              <h4
                className={`text-sm whitespace-nowrap ${
                  selectedView === 'schema-registries'
                    ? 'text-blue-900 dark:text-accent'
                    : 'text-gray-800 dark:text-gray-200'
                }`}
              >
                Schema Registries
              </h4>
            </div>
          </button>
        </div>
      </div>
    </div>
  )
}
```

**Step 2: Run type check**

```bash
cd cmd/ui/frontend
npm run type-check
```

Expected: Compiles successfully

**Step 3: Build frontend**

```bash
cd cmd/ui/frontend
npm run build
```

Expected: Build succeeds

**Step 4: Commit**

```bash
git add cmd/ui/frontend/src/components/explore/Sidebar.tsx
git commit -m "feat: update Sidebar to use unified source components

Replaces region-centric sidebar with conditional rendering of
MSKSourceSection and OSKSourceSection based on source type presence.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 12: Create OSKClusterOverview Component

**Files:**
- Create: `cmd/ui/frontend/src/components/explore/views/OSKClusterOverview.tsx`

**Step 1: Create OSK cluster overview component**

Create `cmd/ui/frontend/src/components/explore/views/OSKClusterOverview.tsx`:

```typescript
import type { OSKCluster } from '@/types'
import { KeyValueGrid } from '@/components/common/KeyValueGrid'
import { KeyValuePair } from '@/components/common/KeyValuePair'

interface OSKClusterOverviewProps {
  cluster: OSKCluster
}

export const OSKClusterOverview = ({ cluster }: OSKClusterOverviewProps) => {
  return (
    <div className="space-y-6">
      {/* Bootstrap Servers */}
      <div className="bg-white dark:bg-card rounded-xl p-6 shadow-lg border border-gray-200 dark:border-border">
        <h3 className="text-lg font-semibold mb-4 text-gray-900 dark:text-gray-100">
          Bootstrap Servers
        </h3>
        <div className="space-y-2">
          {cluster.bootstrap_servers.map((server, idx) => (
            <div
              key={idx}
              className="font-mono text-sm bg-gray-50 dark:bg-gray-800 p-3 rounded border border-gray-200 dark:border-border"
            >
              {server}
            </div>
          ))}
        </div>
      </div>

      {/* Metadata */}
      <div className="bg-white dark:bg-card rounded-xl p-6 shadow-lg border border-gray-200 dark:border-border">
        <h3 className="text-lg font-semibold mb-4 text-gray-900 dark:text-gray-100">
          Cluster Metadata
        </h3>
        <KeyValueGrid>
          {cluster.metadata.environment && (
            <KeyValuePair label="Environment" value={cluster.metadata.environment} />
          )}
          {cluster.metadata.location && (
            <KeyValuePair label="Location" value={cluster.metadata.location} />
          )}
          {cluster.metadata.kafka_version && (
            <KeyValuePair label="Kafka Version" value={cluster.metadata.kafka_version} />
          )}
          {cluster.metadata.last_scanned && (
            <KeyValuePair
              label="Last Scanned"
              value={new Date(cluster.metadata.last_scanned).toLocaleString()}
            />
          )}
        </KeyValueGrid>
      </div>

      {/* Labels */}
      {cluster.metadata.labels && Object.keys(cluster.metadata.labels).length > 0 && (
        <div className="bg-white dark:bg-card rounded-xl p-6 shadow-lg border border-gray-200 dark:border-border">
          <h3 className="text-lg font-semibold mb-4 text-gray-900 dark:text-gray-100">Labels</h3>
          <div className="flex flex-wrap gap-2">
            {Object.entries(cluster.metadata.labels).map(([key, value]) => (
              <span
                key={key}
                className="px-3 py-1 bg-blue-100 dark:bg-blue-900/20 text-blue-800 dark:text-blue-200 rounded-full text-sm font-medium"
              >
                {key}: {value}
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
```

**Step 2: Run type check**

```bash
cd cmd/ui/frontend
npm run type-check
```

Expected: Compiles

**Step 3: Commit**

```bash
git add cmd/ui/frontend/src/components/explore/views/OSKClusterOverview.tsx
git commit -m "feat: create OSKClusterOverview component

Displays bootstrap servers, metadata, and labels for OSK clusters
in the Cluster tab.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 13: Create OSKClusterHeader Component

**Files:**
- Create: `cmd/ui/frontend/src/components/explore/views/OSKClusterHeader.tsx`

**Step 1: Create OSK cluster header**

Create `cmd/ui/frontend/src/components/explore/views/OSKClusterHeader.tsx`:

```typescript
import type { OSKCluster } from '@/types'

interface OSKClusterHeaderProps {
  cluster: OSKCluster
}

export const OSKClusterHeader = ({ cluster }: OSKClusterHeaderProps) => {
  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-4xl font-bold text-gray-900 dark:text-gray-100">
          Cluster: {cluster.id}
        </h1>
        {cluster.metadata.last_scanned && (
          <p className="text-sm text-gray-600 dark:text-gray-400 mt-1">
            Last scanned: {new Date(cluster.metadata.last_scanned).toLocaleString()}
          </p>
        )}
      </div>

      {/* Key Metrics */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        {cluster.metadata.kafka_version && (
          <div className="bg-white dark:bg-card p-6 rounded-lg border border-gray-200 dark:border-border shadow-sm">
            <div className="text-3xl font-bold text-gray-900 dark:text-gray-100">
              {cluster.metadata.kafka_version}
            </div>
            <div className="text-sm text-gray-600 dark:text-gray-400 mt-1">Kafka Version</div>
          </div>
        )}

        {cluster.metadata.environment && (
          <div className="bg-white dark:bg-card p-6 rounded-lg border border-gray-200 dark:border-border shadow-sm">
            <div className="text-3xl font-bold text-gray-900 dark:text-gray-100 capitalize">
              {cluster.metadata.environment}
            </div>
            <div className="text-sm text-gray-600 dark:text-gray-400 mt-1">Environment</div>
          </div>
        )}

        {cluster.metadata.location && (
          <div className="bg-white dark:bg-card p-6 rounded-lg border border-gray-200 dark:border-border shadow-sm">
            <div className="text-3xl font-bold text-gray-900 dark:text-gray-100">
              {cluster.metadata.location}
            </div>
            <div className="text-sm text-gray-600 dark:text-gray-400 mt-1">Location</div>
          </div>
        )}
      </div>
    </div>
  )
}
```

**Step 2: Run type check**

```bash
cd cmd/ui/frontend
npm run type-check
```

Expected: Compiles

**Step 3: Commit**

```bash
git add cmd/ui/frontend/src/components/explore/views/OSKClusterHeader.tsx
git commit -m "feat: create OSKClusterHeader component

Displays cluster name, last scanned timestamp, and key metadata
metrics at the top of OSK cluster detail views.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 14: Create OSKClusterReport Component

**Files:**
- Create: `cmd/ui/frontend/src/components/explore/views/OSKClusterReport.tsx`

**Step 1: Create OSK cluster report component**

Create `cmd/ui/frontend/src/components/explore/views/OSKClusterReport.tsx`:

```typescript
import { useAppStore } from '@/stores/store'
import { isOSKSource } from '@/lib/sourceUtils'
import { OSKClusterHeader } from './OSKClusterHeader'
import { OSKClusterOverview } from './OSKClusterOverview'
import { ClusterTopics } from '../clusters/ClusterTopics'
import { ClusterACLs } from '../clusters/ClusterACLs'
import { ClusterConnectors } from '../clusters/ClusterConnectors'
import { ClusterClients } from '../clusters/ClusterClients'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/common/Tabs'

export const OSKClusterReport = () => {
  const processedState = useAppStore((state) => state.processedState)
  const selectedOSKClusterId = useAppStore((state) => state.selectedOSKClusterId)

  // Find the OSK source
  const oskSource = processedState?.sources.find(isOSKSource)
  const cluster = oskSource?.osk_data?.clusters.find((c) => c.id === selectedOSKClusterId)

  if (!cluster) {
    return (
      <div className="p-6">
        <div className="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-border rounded-lg p-4">
          <p className="text-red-800 dark:text-red-200">
            Cluster not found. Please select a cluster from the sidebar.
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-6">
      <OSKClusterHeader cluster={cluster} />

      <Tabs defaultValue="cluster">
        <TabsList>
          <TabsTrigger value="cluster">Cluster</TabsTrigger>
          <TabsTrigger value="topics">Topics</TabsTrigger>
          <TabsTrigger value="acls">ACLs</TabsTrigger>
          <TabsTrigger value="connectors">Connectors</TabsTrigger>
          {cluster.discovered_clients && cluster.discovered_clients.length > 0 && (
            <TabsTrigger value="clients">Clients</TabsTrigger>
          )}
        </TabsList>

        <TabsContent value="cluster">
          <OSKClusterOverview cluster={cluster} />
        </TabsContent>

        <TabsContent value="topics">
          <ClusterTopics topics={cluster.kafka_admin_client_information?.topics} />
        </TabsContent>

        <TabsContent value="acls">
          <ClusterACLs acls={cluster.kafka_admin_client_information?.acls} />
        </TabsContent>

        <TabsContent value="connectors">
          <ClusterConnectors
            connectors={cluster.kafka_admin_client_information?.self_managed_connectors}
          />
        </TabsContent>

        {cluster.discovered_clients && cluster.discovered_clients.length > 0 && (
          <TabsContent value="clients">
            <ClusterClients clients={cluster.discovered_clients} />
          </TabsContent>
        )}
      </Tabs>
    </div>
  )
}
```

**Step 2: Run type check**

```bash
cd cmd/ui/frontend
npm run type-check
```

Expected: Compiles

**Step 3: Commit**

```bash
git add cmd/ui/frontend/src/components/explore/views/OSKClusterReport.tsx
git commit -m "feat: create OSKClusterReport component

Main OSK cluster detail view with tabs for Cluster, Topics, ACLs,
Connectors, and Clients. Reuses existing MSK components for Kafka
Admin API data.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 15: Update Explore Router

**Files:**
- Modify: `cmd/ui/frontend/src/components/explore/Explore.tsx`
- Modify: `cmd/ui/frontend/src/components/explore/views/ClusterReport.tsx` (rename to MSKClusterReport.tsx)

**Step 1: Rename ClusterReport to MSKClusterReport**

```bash
cd cmd/ui/frontend/src/components/explore/views
git mv ClusterReport.tsx MSKClusterReport.tsx
```

**Step 2: Update component name inside MSKClusterReport.tsx**

Find and replace in `MSKClusterReport.tsx`:
- `export const ClusterReport` → `export const MSKClusterReport`

**Step 3: Update Explore.tsx to route by source type**

Replace content of `cmd/ui/frontend/src/components/explore/Explore.tsx`:

```typescript
import { useAppStore } from '@/stores/store'
import { Summary } from './views/Summary'
import { RegionReport } from './views/RegionReport'
import { MSKClusterReport } from './views/MSKClusterReport'
import { OSKClusterReport } from './views/OSKClusterReport'
import { SchemaRegistries } from './views/SchemaRegistries'

export const Explore = () => {
  const selectedView = useAppStore((state) => state.selectedView)
  const selectedSourceType = useAppStore((state) => state.selectedSourceType)

  if (selectedView === 'summary') {
    return <Summary />
  }

  if (selectedView === 'region') {
    return <RegionReport />
  }

  if (selectedView === 'cluster') {
    // Route to appropriate cluster view based on source type
    if (selectedSourceType === 'msk') {
      return <MSKClusterReport />
    } else if (selectedSourceType === 'osk') {
      return <OSKClusterReport />
    }
    // Fallback for invalid state
    return (
      <div className="p-6">
        <div className="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-border rounded-lg p-4">
          <p className="text-yellow-800 dark:text-yellow-200">
            Unknown cluster source type. Please select a cluster from the sidebar.
          </p>
        </div>
      </div>
    )
  }

  if (selectedView === 'schema-registries') {
    return <SchemaRegistries />
  }

  // Default empty state
  return (
    <div className="p-6">
      <div className="text-center">
        <h1 className="text-4xl font-bold text-gray-900 dark:text-gray-100 mb-2">
          Explore Your Kafka Infrastructure
        </h1>
        <p className="text-lg text-gray-600 dark:text-gray-400">
          Upload a KCP state file or select a cluster from the sidebar
        </p>
      </div>
    </div>
  )
}
```

**Step 4: Run type check**

```bash
cd cmd/ui/frontend
npm run type-check
```

Expected: Compiles successfully

**Step 5: Build frontend**

```bash
cd cmd/ui/frontend
npm run build
```

Expected: Build succeeds

**Step 6: Commit**

```bash
git add cmd/ui/frontend/src/components/explore/
git commit -m "feat: update Explore router for OSK cluster support

Renames ClusterReport to MSKClusterReport and adds routing logic
to display OSKClusterReport when source type is OSK.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 16: Build and Manual Test

**Files:**
- None (testing)

**Step 1: Build full application**

```bash
make build
```

Expected: Build succeeds

**Step 2: Start UI server**

```bash
./bin/kcp ui --port 5556
```

**Step 3: Manual testing checklist**

Open browser to `http://localhost:5556`:

1. Upload MSK-only state file:
   - ✅ "AWS MSK" section appears
   - ✅ "Open Source Kafka" section does NOT appear
   - ✅ Can navigate to MSK clusters
   - ✅ MSK cluster detail shows Metrics tab

2. Upload OSK-only state file:
   - ✅ "Open Source Kafka" section appears
   - ✅ "AWS MSK" section does NOT appear
   - ✅ Can click OSK cluster
   - ✅ OSK cluster detail shows: Cluster, Topics, ACLs, Connectors tabs
   - ✅ OSK cluster detail does NOT show Metrics tab
   - ✅ Bootstrap servers displayed correctly
   - ✅ Metadata (environment, location) displayed

3. Upload state with both MSK and OSK:
   - ✅ Both sections appear
   - ✅ Can switch between MSK and OSK clusters
   - ✅ MSK cluster shows ARN
   - ✅ OSK cluster shows bootstrap servers

**Step 4: Document any issues found**

If issues found, create follow-up tasks to fix them.

---

## Task 17: Install Playwright

**Files:**
- Modify: `cmd/ui/frontend/package.json`
- Create: `cmd/ui/frontend/playwright.config.ts`

**Step 1: Install Playwright**

```bash
cd cmd/ui/frontend
npm install -D @playwright/test
npx playwright install chromium
```

**Step 2: Create Playwright config**

Create `cmd/ui/frontend/playwright.config.ts`:

```typescript
import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: 'html',

  use: {
    baseURL: 'http://localhost:5556',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],

  webServer: {
    command: 'cd ../../.. && ./bin/kcp ui --port 5556',
    url: 'http://localhost:5556',
    reuseExistingServer: !process.env.CI,
    timeout: 30_000,
  },
})
```

**Step 3: Add test scripts to package.json**

Add to `scripts` in `cmd/ui/frontend/package.json`:

```json
{
  "scripts": {
    "test:e2e": "playwright test",
    "test:e2e:ui": "playwright test --ui",
    "test:e2e:debug": "playwright test --debug",
    "test:e2e:headed": "playwright test --headed"
  }
}
```

**Step 4: Create test directories**

```bash
cd cmd/ui/frontend
mkdir -p tests/e2e tests/fixtures
```

**Step 5: Commit**

```bash
git add cmd/ui/frontend/package.json cmd/ui/frontend/package-lock.json cmd/ui/frontend/playwright.config.ts
git commit -m "test: install and configure Playwright

Adds Playwright for E2E testing with config to auto-start kcp ui
server during test runs.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 18: Create Test Fixtures

**Files:**
- Create: `cmd/ui/frontend/tests/fixtures/state-osk-only.json`
- Create: `cmd/ui/frontend/tests/fixtures/state-both.json`

**Step 1: Create OSK-only state fixture**

Create `cmd/ui/frontend/tests/fixtures/state-osk-only.json`:

```json
{
  "msk_sources": {
    "regions": []
  },
  "osk_sources": {
    "clusters": [
      {
        "id": "prod-kafka-cluster",
        "bootstrap_servers": [
          "broker1.example.com:9092",
          "broker2.example.com:9092"
        ],
        "kafka_admin_client_information": {
          "topics": {
            "summary": {
              "topics": 5,
              "total_partitions": 15,
              "internal_topics": 1,
              "compact_topics": 2
            },
            "details": [
              {
                "name": "orders",
                "partitions": 3,
                "replication_factor": 2,
                "configurations": {
                  "cleanup.policy": "delete"
                }
              }
            ]
          },
          "acls": [
            {
              "ResourceType": "TOPIC",
              "ResourceName": "orders",
              "ResourcePatternType": "LITERAL",
              "Principal": "User:alice",
              "Host": "*",
              "Operation": "READ",
              "PermissionType": "ALLOW"
            }
          ]
        },
        "discovered_clients": [],
        "metadata": {
          "environment": "production",
          "location": "datacenter-1",
          "kafka_version": "3.6.0",
          "labels": {
            "team": "platform",
            "cost-center": "engineering"
          },
          "last_scanned": "2026-03-06T10:00:00Z"
        }
      }
    ]
  },
  "schema_registries": [],
  "kcp_build_info": {
    "version": "dev",
    "commit": "abc123",
    "date": "2026-03-06"
  },
  "timestamp": "2026-03-06T10:00:00Z"
}
```

**Step 2: Create mixed MSK+OSK state fixture**

Create `cmd/ui/frontend/tests/fixtures/state-both.json`:

```json
{
  "msk_sources": {
    "regions": [
      {
        "name": "us-east-1",
        "clusters": [
          {
            "name": "msk-cluster-1",
            "arn": "arn:aws:kafka:us-east-1:123456789012:cluster/msk-cluster-1/uuid",
            "region": "us-east-1",
            "aws_client_information": {
              "msk_cluster_config": {
                "Provisioned": {
                  "NumberOfBrokerNodes": 3
                }
              }
            },
            "kafka_admin_client_information": {
              "topics": {
                "summary": {
                  "topics": 3,
                  "total_partitions": 9,
                  "internal_topics": 1,
                  "compact_topics": 1
                },
                "details": []
              }
            },
            "discovered_clients": []
          }
        ]
      }
    ]
  },
  "osk_sources": {
    "clusters": [
      {
        "id": "staging-kafka-cluster",
        "bootstrap_servers": ["broker1.staging.com:9092"],
        "kafka_admin_client_information": {
          "topics": {
            "summary": {
              "topics": 2,
              "total_partitions": 6,
              "internal_topics": 0,
              "compact_topics": 1
            },
            "details": []
          },
          "acls": []
        },
        "discovered_clients": [],
        "metadata": {
          "environment": "staging",
          "location": "us-west-2",
          "last_scanned": "2026-03-06T10:00:00Z"
        }
      }
    ]
  },
  "schema_registries": [],
  "kcp_build_info": {
    "version": "dev",
    "commit": "abc123",
    "date": "2026-03-06"
  },
  "timestamp": "2026-03-06T10:00:00Z"
}
```

**Step 3: Commit**

```bash
git add cmd/ui/frontend/tests/fixtures/
git commit -m "test: add Playwright test fixtures

Adds sample state files for testing OSK-only and mixed MSK+OSK
scenarios.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 19: Write OSK Sidebar Tests

**Files:**
- Create: `cmd/ui/frontend/tests/e2e/osk-sidebar.spec.ts`

**Step 1: Create OSK sidebar test**

Create `cmd/ui/frontend/tests/e2e/osk-sidebar.spec.ts`:

```typescript
import { test, expect } from '@playwright/test'
import stateOSKOnly from '../fixtures/state-osk-only.json'

test.describe('OSK Sidebar', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')

    // Upload OSK-only state file via file input
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'state-osk-only.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(stateOSKOnly)),
    })

    // Wait for state to be processed
    await page.waitForTimeout(500)
  })

  test('displays OSK section when OSK clusters present', async ({ page }) => {
    // Verify "OPEN SOURCE KAFKA" section appears
    await expect(page.locator('text=OPEN SOURCE KAFKA')).toBeVisible()

    // Verify OSK cluster is listed
    await expect(page.locator('text=prod-kafka-cluster')).toBeVisible()
  })

  test('does not display MSK section when no MSK clusters', async ({ page }) => {
    // Verify "AWS MSK" section is not present
    await expect(page.locator('text=AWS MSK')).not.toBeVisible()
  })

  test('selects OSK cluster on click', async ({ page }) => {
    // Click on OSK cluster
    await page.click('text=prod-kafka-cluster')

    // Verify cluster detail view appears
    await expect(page.locator('h1:has-text("Cluster: prod-kafka-cluster")')).toBeVisible()

    // Verify cluster is highlighted in sidebar
    const clusterButton = page.locator('button:has-text("prod-kafka-cluster")')
    await expect(clusterButton).toHaveClass(/bg-blue-100/)
  })
})
```

**Step 2: Run test**

```bash
cd cmd/ui/frontend
npm run test:e2e
```

Expected: Tests pass (or fail if there are bugs - fix them)

**Step 3: Commit**

```bash
git add cmd/ui/frontend/tests/e2e/osk-sidebar.spec.ts
git commit -m "test: add OSK sidebar Playwright tests

Tests OSK section visibility, cluster listing, and selection
behavior.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 20: Write OSK Cluster Detail Tests

**Files:**
- Create: `cmd/ui/frontend/tests/e2e/osk-cluster-detail.spec.ts`

**Step 1: Create OSK cluster detail test**

Create `cmd/ui/frontend/tests/e2e/osk-cluster-detail.spec.ts`:

```typescript
import { test, expect } from '@playwright/test'
import stateOSKOnly from '../fixtures/state-osk-only.json'

test.describe('OSK Cluster Detail View', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')

    // Upload state file
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'state-osk-only.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(stateOSKOnly)),
    })

    await page.waitForTimeout(500)

    // Navigate to OSK cluster
    await page.click('text=prod-kafka-cluster')
  })

  test('displays correct tabs for OSK cluster', async ({ page }) => {
    // Verify OSK-specific tabs are present
    await expect(page.locator('button[role="tab"]:has-text("Cluster")')).toBeVisible()
    await expect(page.locator('button[role="tab"]:has-text("Topics")')).toBeVisible()
    await expect(page.locator('button[role="tab"]:has-text("ACLs")')).toBeVisible()
    await expect(page.locator('button[role="tab"]:has-text("Connectors")')).toBeVisible()

    // Verify Metrics tab is NOT present
    await expect(page.locator('button[role="tab"]:has-text("Metrics")')).not.toBeVisible()
  })

  test('displays bootstrap servers in cluster tab', async ({ page }) => {
    // Cluster tab should be selected by default
    await expect(page.locator('text=Bootstrap Servers')).toBeVisible()
    await expect(page.locator('text=broker1.example.com:9092')).toBeVisible()
    await expect(page.locator('text=broker2.example.com:9092')).toBeVisible()
  })

  test('displays metadata fields', async ({ page }) => {
    // Verify metadata section
    await expect(page.locator('text=Cluster Metadata')).toBeVisible()
    await expect(page.locator('text=Environment')).toBeVisible()
    await expect(page.locator('text=production')).toBeVisible()
    await expect(page.locator('text=Location')).toBeVisible()
    await expect(page.locator('text=datacenter-1')).toBeVisible()
    await expect(page.locator('text=Kafka Version')).toBeVisible()
    await expect(page.locator('text=3.6.0')).toBeVisible()
  })

  test('displays labels', async ({ page }) => {
    await expect(page.locator('text=Labels')).toBeVisible()
    await expect(page.locator('text=team: platform')).toBeVisible()
    await expect(page.locator('text=cost-center: engineering')).toBeVisible()
  })

  test('Topics tab shows Kafka topics', async ({ page }) => {
    await page.click('button[role="tab"]:has-text("Topics")')

    // Verify topics appear (reusing MSK component)
    await expect(page.locator('text=orders')).toBeVisible()
  })

  test('ACLs tab shows Kafka ACLs', async ({ page }) => {
    await page.click('button[role="tab"]:has-text("ACLs")')

    // Verify ACLs appear
    await expect(page.locator('text=User:alice')).toBeVisible()
  })
})
```

**Step 2: Run tests**

```bash
cd cmd/ui/frontend
npm run test:e2e
```

Expected: Tests pass

**Step 3: Commit**

```bash
git add cmd/ui/frontend/tests/e2e/osk-cluster-detail.spec.ts
git commit -m "test: add OSK cluster detail Playwright tests

Tests tab structure, bootstrap servers display, metadata, labels,
and Kafka Admin API data rendering.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 21: Write Source Switching Tests

**Files:**
- Create: `cmd/ui/frontend/tests/e2e/source-switching.spec.ts`

**Step 1: Create source switching test**

Create `cmd/ui/frontend/tests/e2e/source-switching.spec.ts`:

```typescript
import { test, expect } from '@playwright/test'
import stateBoth from '../fixtures/state-both.json'

test.describe('Switching Between MSK and OSK', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')

    // Upload state with both MSK and OSK
    const fileInput = page.locator('input[type="file"]')
    await fileInput.setInputFiles({
      name: 'state-both.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(stateBoth)),
    })

    await page.waitForTimeout(500)
  })

  test('displays both MSK and OSK sections', async ({ page }) => {
    await expect(page.locator('text=AWS MSK')).toBeVisible()
    await expect(page.locator('text=OPEN SOURCE KAFKA')).toBeVisible()
  })

  test('can switch from MSK cluster to OSK cluster', async ({ page }) => {
    // Click MSK cluster
    await page.click('text=msk-cluster-1')
    await expect(
      page.locator('text=arn:aws:kafka:us-east-1:123456789012:cluster/msk-cluster-1')
    ).toBeVisible()

    // Click OSK cluster
    await page.click('text=staging-kafka-cluster')
    await expect(page.locator('text=Bootstrap Servers')).toBeVisible()

    // Verify ARN is no longer visible
    await expect(
      page.locator('text=arn:aws:kafka:us-east-1:123456789012:cluster/msk-cluster-1')
    ).not.toBeVisible()
  })

  test('MSK summary only shows MSK data', async ({ page }) => {
    await page.click('text=Summary')

    // Verify cost analysis appears (MSK-only feature)
    await expect(page.locator('text=Cost Analysis Summary')).toBeVisible()
  })

  test('OSK cluster does not have Metrics tab', async ({ page }) => {
    await page.click('text=staging-kafka-cluster')

    // Verify no Metrics tab
    await expect(page.locator('button[role="tab"]:has-text("Metrics")')).not.toBeVisible()
  })

  test('MSK cluster has Metrics tab', async ({ page }) => {
    await page.click('text=msk-cluster-1')

    // Verify Metrics tab exists
    await expect(page.locator('button[role="tab"]:has-text("Metrics")')).toBeVisible()
  })
})
```

**Step 2: Run tests**

```bash
cd cmd/ui/frontend
npm run test:e2e
```

Expected: All tests pass

**Step 3: Commit**

```bash
git add cmd/ui/frontend/tests/e2e/source-switching.spec.ts
git commit -m "test: add source switching Playwright tests

Tests navigation between MSK and OSK clusters, verifying correct
tab structure and data display for each source type.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 22: Update Documentation

**Files:**
- Modify: `cmd/ui/frontend/README.md` (create if doesn't exist)

**Step 1: Create or update frontend README**

Create/modify `cmd/ui/frontend/README.md`:

```markdown
# KCP Frontend

React + TypeScript frontend for the KCP web UI.

## Development

### Prerequisites

- Node.js 18+
- Yarn

### Setup

```bash
yarn install
```

### Development Server

```bash
yarn dev
```

Frontend runs on `http://localhost:5173` with hot reload.

### Build

```bash
yarn build
```

Built assets are embedded into the Go binary via `frontend.go`.

### Type Checking

```bash
yarn type-check
yarn type-check:watch
```

### Linting

```bash
yarn lint
```

## Testing

### E2E Tests with Playwright

Playwright tests run against the full Go backend + frontend.

**Run all tests:**

```bash
yarn test:e2e
```

**Run with UI mode (interactive):**

```bash
yarn test:e2e:ui
```

**Debug specific test:**

```bash
yarn test:e2e:debug osk-sidebar.spec.ts
```

**Run with browser visible:**

```bash
yarn test:e2e:headed
```

### Test Fixtures

Test fixtures are in `tests/fixtures/`:
- `state-osk-only.json` - OSK clusters only
- `state-both.json` - Both MSK and OSK clusters

## Architecture

### Source Types

The UI supports two Kafka source types:

**MSK (AWS Managed Streaming for Kafka)**
- Regions → Clusters hierarchy
- Displays: ARN, VPC, instance type, CloudWatch metrics, costs
- Tabs: Cluster, Metrics, Topics, ACLs, Connectors, Clients

**OSK (Open Source Kafka)**
- Flat cluster list
- Displays: Bootstrap servers, metadata, labels
- Tabs: Cluster, Topics, ACLs, Connectors, Clients (no Metrics)

### State Management

- Zustand for global state
- `useAppStore` hook provides access to:
  - `processedState` - unified source data
  - `selectedSourceType` - 'msk' | 'osk'
  - `selectOSKCluster(clusterId)` - navigate to OSK cluster
  - `selectCluster(region, arn)` - navigate to MSK cluster

### Type Guards

Use type guards from `lib/sourceUtils.ts` to safely access source-specific data:

```typescript
import { isMSKSource, isOSKSource } from '@/lib/sourceUtils'

const mskSource = processedState?.sources.find(isMSKSource)
const oskSource = processedState?.sources.find(isOSKSource)
```

## Tech Stack

- React 19
- TypeScript 5.8
- Vite 7
- Zustand (state management)
- Recharts (charts)
- Tailwind CSS 4
- Playwright (E2E testing)
```

**Step 2: Commit**

```bash
git add cmd/ui/frontend/README.md
git commit -m "docs: add frontend README with OSK testing info

Documents Playwright test setup, source types, and architecture.

Co-Authored-By: Claude Sonnet 4.5 <noreply@anthropic.com>"
```

---

## Task 23: Final Build and Verification

**Files:**
- None (verification)

**Step 1: Clean build**

```bash
make clean
make build
```

Expected: Build succeeds

**Step 2: Run all tests**

```bash
# Backend tests
make test

# Frontend E2E tests
cd cmd/ui/frontend
npm run test:e2e
cd ../../..
```

Expected: All tests pass

**Step 3: Manual smoke test**

```bash
./bin/kcp ui --port 5556
```

Open browser, verify:
- ✅ Upload OSK-only state → OSK section appears
- ✅ Upload MSK-only state → MSK section appears
- ✅ Upload mixed state → Both sections appear
- ✅ OSK cluster detail shows correct tabs
- ✅ Can navigate between MSK and OSK clusters

**Step 4: Final commit (if needed)**

If any fixes were required, commit them now.

---

## Implementation Complete

All tasks complete! The OSK UI support feature is now implemented and tested.

**Summary:**
- ✅ Backend unified source model with discriminated union
- ✅ Frontend TypeScript types and type guards
- ✅ Sidebar with separate MSK and OSK sections
- ✅ OSK cluster detail view with appropriate tabs
- ✅ Playwright E2E tests covering core scenarios
- ✅ State file always initializes both sources
- ✅ OSK credentials validation fixed

**Next Steps:**
- Consider adding OSK migration workflow (future iteration)
- Monitor for user feedback on OSK UI experience
