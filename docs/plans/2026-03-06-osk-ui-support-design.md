# OSK UI Support Design

**Date:** 2026-03-06
**Status:** Approved
**Scope:** Explore tab only (Migration tab in future iteration)

## Overview

Add Open Source Kafka (OSK) cluster support to the KCP web UI. This enables users to visualize OSK clusters alongside MSK clusters in the Explore tab, with appropriate UI adaptations for OSK's different data model (no AWS-specific details, costs, or metrics).

## Goals

1. Display OSK clusters in the UI sidebar with clear separation from MSK
2. Show OSK cluster details with appropriate tabs (no Metrics/Costs)
3. Support state files containing MSK only, OSK only, or both
4. Implement Playwright for automated UI testing
5. Ensure consistent state file structure (always initialize both source types)

## Non-Goals

- Migration workflow for OSK (deferred to future work)
- OSK cost/metrics collection (OSK has no CloudWatch equivalent)
- Client discovery for OSK clusters

## Architecture

### Unified Source Model

The backend and frontend will use a unified source abstraction that treats MSK and OSK as different source types within a common structure.

**Key Principle:** The state file always contains both `msk_sources` and `osk_sources`, even if one is empty. This eliminates nil checks and ensures consistent structure.

---

## Backend Design

### Data Model

#### State File Structure

```go
type State struct {
    MSKSources       *MSKSourcesState            `json:"msk_sources"`
    OSKSources       *OSKSourcesState            `json:"osk_sources"`
    SchemaRegistries []SchemaRegistryInformation `json:"schema_registries"`
    KcpBuildInfo     KcpBuildInfo                `json:"kcp_build_info"`
    Timestamp        time.Time                   `json:"timestamp"`
}

type MSKSourcesState struct {
    Regions []DiscoveredRegion `json:"regions"`
}

type OSKSourcesState struct {
    Clusters []OSKDiscoveredCluster `json:"clusters"`
}
```

**Guarantee:** Both `MSKSources` and `OSKSources` are always initialized, even when empty:

```json
{
  "msk_sources": { "regions": [] },
  "osk_sources": { "clusters": [] },
  "schema_registries": [],
  "kcp_build_info": {...},
  "timestamp": "2026-03-06T10:00:00Z"
}
```

#### Processed State (API Response)

```go
type ProcessedState struct {
    Sources          []ProcessedSource
    SchemaRegistries []SchemaRegistry
    KcpBuildInfo     KcpBuildInfo
    Timestamp        time.Time
}

type ProcessedSource struct {
    Type     SourceType              `json:"type"`      // "msk" | "osk"
    MSKData  *ProcessedMSKSource     `json:"msk_data,omitempty"`
    OSKData  *ProcessedOSKSource     `json:"osk_data,omitempty"`
}

type ProcessedMSKSource struct {
    Regions []ProcessedRegion `json:"regions"`
}

type ProcessedOSKSource struct {
    Clusters []ProcessedOSKCluster `json:"clusters"`
}

type ProcessedOSKCluster struct {
    ID                          string
    BootstrapServers            []string
    KafkaAdminClientInformation types.KafkaAdminClientInformation
    DiscoveredClients           []types.DiscoveredClient
    Metadata                    types.OSKClusterMetadata
}
```

### ProcessState Logic

```go
func (rs *ReportService) ProcessState(state types.State) types.ProcessedState {
    sources := []types.ProcessedSource{}

    // Process MSK if present
    if state.MSKSources != nil && len(state.MSKSources.Regions) > 0 {
        mskSource := types.ProcessedSource{
            Type: types.SourceTypeMSK,
            MSKData: &types.ProcessedMSKSource{
                Regions: rs.processMSKRegions(state.MSKSources.Regions),
            },
        }
        sources = append(sources, mskSource)
    }

    // Process OSK if present
    if state.OSKSources != nil && len(state.OSKSources.Clusters) > 0 {
        oskSource := types.ProcessedSource{
            Type: types.SourceTypeOSK,
            OSKData: &types.ProcessedOSKSource{
                Clusters: rs.processOSKClusters(state.OSKSources.Clusters),
            },
        }
        sources = append(sources, oskSource)
    }

    return types.ProcessedState{
        Sources:          sources,
        SchemaRegistries: state.SchemaRegistries,
        KcpBuildInfo:     state.KcpBuildInfo,
        Timestamp:        state.Timestamp,
    }
}
```

### State File Merge Logic

#### OSK Credentials Validation Fix

**Current Issue:** OSK credentials validation requires at least one auth method with `use: true`, preventing users from skipping clusters (unlike MSK).

**Fix:** Remove this validation check:
```go
// internal/types/osk_credentials.go - line 90-97
// REMOVE:
if len(enabledMethods) == 0 {
    errs = append(errs, fmt.Errorf("%s (id=%s): no authentication method enabled", clusterRef, cluster.ID))
}
```

**New Behavior:**
- Clusters with all auth methods set to `use: false` are skipped during scan
- Matches MSK behavior
- Allows selective rescanning

#### Merge Strategy

**MSK Clusters:**
- Identifier: Cluster ARN
- Match: If ARN exists in `state.msk_sources.regions[].clusters[]`
- Action: Replace entire cluster object if matched, append if new

**OSK Clusters:**
- Identifier: Cluster ID (from credentials file)
- Match: If ID exists in `state.osk_sources.clusters[]`
- Action: Replace entire cluster object if matched, append if new

#### Merge Scenarios

**Scenario 1: First OSK scan (no state file)**
```bash
kcp scan clusters --source-type osk --credentials-file osk-credentials.yaml
```
Creates:
```json
{
  "msk_sources": { "regions": [] },
  "osk_sources": { "clusters": [...scanned clusters...] }
}
```

**Scenario 2: First MSK scan**
```bash
kcp discover --region us-east-1
kcp scan clusters --source-type msk --state-file kcp-state.json
```
Creates:
```json
{
  "msk_sources": { "regions": [...discovered regions...] },
  "osk_sources": { "clusters": [] }
}
```

**Scenario 3: Add OSK to existing MSK state**
```bash
kcp scan clusters --source-type osk --credentials-file osk-credentials.yaml --state-file kcp-state.json
```
- Loads existing MSK data
- Creates/updates `osk_sources`
- Does not modify `msk_sources`

**Scenario 4: Rescan existing OSK clusters**
```bash
kcp scan clusters --source-type osk --credentials-file osk-credentials.yaml --state-file kcp-state.json
```
- For each cluster in credentials with `use: true`:
  - If cluster ID exists → replace with fresh scan
  - If cluster ID is new → append
- Preserves clusters not in current credentials file

**Scenario 5: Partial OSK rescan**
```yaml
clusters:
  - id: prod-cluster
    auth_method:
      sasl_scram: { use: true }  # Scanned
  - id: staging-cluster
    auth_method:
      sasl_scram: { use: false } # Skipped
```
- Only `prod-cluster` gets scanned and updated
- `staging-cluster` data in state remains unchanged

---

## Frontend Design

### TypeScript Types

```typescript
// types/api/state.ts

export type SourceType = 'msk' | 'osk'

export interface ProcessedSource {
  type: SourceType
  msk_data?: ProcessedMSKSource
  osk_data?: ProcessedOSKSource
}

export interface ProcessedMSKSource {
  regions: Region[]
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

export interface ProcessedState {
  sources: ProcessedSource[]
  schema_registries?: SchemaRegistry[]
  kcp_build_info?: unknown
  timestamp?: string
}
```

### Type Guards

```typescript
// lib/sourceUtils.ts

export function isMSKSource(source: ProcessedSource): source is ProcessedSource & { msk_data: ProcessedMSKSource } {
  return source.type === 'msk' && source.msk_data !== undefined
}

export function isOSKSource(source: ProcessedSource): source is ProcessedSource & { osk_data: ProcessedOSKSource } {
  return source.type === 'osk' && source.osk_data !== undefined
}
```

### Zustand Store

```typescript
// stores/store.ts

interface AppState {
  processedState: ProcessedState | null

  // View state
  selectedView: 'summary' | 'region' | 'cluster' | 'schema-registries'
  selectedSourceType: SourceType | null  // NEW: track which source type
  selectedRegionName: string | null      // For MSK
  selectedClusterArn: string | null      // For MSK
  selectedOSKClusterId: string | null    // NEW: For OSK

  // Actions
  selectMSKCluster: (regionName: string, clusterArn: string) => void
  selectOSKCluster: (clusterId: string) => void  // NEW
  selectRegion: (regionName: string) => void
  selectSummary: () => void
}
```

### Component Structure

#### Sidebar Layout

```
┌─ Explore Sidebar ──────────────┐
│                                 │
│ AWS MSK                         │
│   ● Summary                     │
│   ● us-east-1                   │
│      • msk-cluster-1            │
│      • msk-cluster-2            │
│   ● us-west-2                   │
│      • msk-cluster-3            │
│                                 │
│ OPEN SOURCE KAFKA               │
│   • prod-kafka-cluster          │
│   • staging-kafka-cluster       │
│   • dev-kafka-cluster           │
│                                 │
│ Schema Registries               │
│   ● Schema Registries           │
│                                 │
└─────────────────────────────────┘
```

#### Component Hierarchy

```
Sidebar
├── MSKSourceSection (if MSK source exists)
│   ├── SummaryButton
│   └── RegionList
│       └── RegionItem (for each region)
│           └── ClusterItem (for each cluster)
│
├── OSKSourceSection (if OSK source exists)
│   └── OSKClusterItem (for each OSK cluster)
│
└── SchemaRegistriesSection
    └── SchemaRegistriesButton
```

#### Sidebar Component

```typescript
export const Sidebar = () => {
  const processedState = useAppStore((state) => state.processedState)

  const mskSource = processedState?.sources.find(isMSKSource)
  const oskSource = processedState?.sources.find(isOSKSource)

  return (
    <div className="h-full flex flex-col">
      <div className="flex-1 overflow-y-auto p-4 space-y-6">
        {mskSource && <MSKSourceSection regions={mskSource.msk_data.regions} />}
        {oskSource && <OSKSourceSection clusters={oskSource.osk_data.clusters} />}
        {!mskSource && !oskSource && <EmptyState />}
      </div>
      <SchemaRegistriesSection />
    </div>
  )
}
```

### OSK Cluster Detail View

#### Tab Structure

**OSK Clusters show these tabs:**
- ✅ **Cluster** - Bootstrap servers, metadata, labels
- ✅ **Topics** - Kafka Admin API data (reuses existing component)
- ✅ **ACLs** - Kafka Admin API data (reuses existing component)
- ✅ **Connectors** - Kafka Admin API data (reuses existing component)
- ✅ **Clients** - Discovered clients (if available)

**OSK Clusters hide these tabs:**
- ❌ **Metrics** - No CloudWatch data for OSK

**MSK Clusters keep all tabs:**
- Cluster, Metrics, Topics, ACLs, Connectors, Clients

#### OSK Cluster Report

```typescript
export const OSKClusterReport = () => {
  const processedState = useAppStore((state) => state.processedState)
  const selectedOSKClusterId = useAppStore((state) => state.selectedOSKClusterId)

  const oskSource = processedState?.sources.find(isOSKSource)
  const cluster = oskSource?.osk_data.clusters.find(c => c.id === selectedOSKClusterId)

  if (!cluster) return <div>Cluster not found</div>

  return (
    <div className="p-6 space-y-6">
      <OSKClusterHeader cluster={cluster} />

      <Tabs defaultValue="cluster">
        <TabsList>
          <TabsTrigger value="cluster">Cluster</TabsTrigger>
          <TabsTrigger value="topics">Topics</TabsTrigger>
          <TabsTrigger value="acls">ACLs</TabsTrigger>
          <TabsTrigger value="connectors">Connectors</TabsTrigger>
          {cluster.discovered_clients?.length > 0 && (
            <TabsTrigger value="clients">Clients</TabsTrigger>
          )}
        </TabsList>

        <TabsContent value="cluster">
          <OSKClusterOverview cluster={cluster} />
        </TabsContent>

        <TabsContent value="topics">
          <ClusterTopics topics={cluster.kafka_admin_client_information.topics} />
        </TabsContent>

        <TabsContent value="acls">
          <ClusterACLs acls={cluster.kafka_admin_client_information.acls} />
        </TabsContent>

        <TabsContent value="connectors">
          <ClusterConnectors
            connectors={cluster.kafka_admin_client_information.self_managed_connectors}
          />
        </TabsContent>

        {cluster.discovered_clients?.length > 0 && (
          <TabsContent value="clients">
            <ClusterClients clients={cluster.discovered_clients} />
          </TabsContent>
        )}
      </Tabs>
    </div>
  )
}
```

#### OSK Cluster Overview Tab

```typescript
export const OSKClusterOverview = ({ cluster }: { cluster: OSKCluster }) => {
  return (
    <div className="space-y-6">
      {/* Bootstrap Servers */}
      <div className="bg-white dark:bg-card rounded-xl p-6 border">
        <h3 className="text-lg font-semibold mb-4">Bootstrap Servers</h3>
        <div className="space-y-2">
          {cluster.bootstrap_servers.map((server, idx) => (
            <div key={idx} className="font-mono text-sm bg-gray-50 dark:bg-gray-800 p-2 rounded">
              {server}
            </div>
          ))}
        </div>
      </div>

      {/* Metadata */}
      <div className="bg-white dark:bg-card rounded-xl p-6 border">
        <h3 className="text-lg font-semibold mb-4">Cluster Metadata</h3>
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
        <div className="bg-white dark:bg-card rounded-xl p-6 border">
          <h3 className="text-lg font-semibold mb-4">Labels</h3>
          <div className="flex flex-wrap gap-2">
            {Object.entries(cluster.metadata.labels).map(([key, value]) => (
              <span
                key={key}
                className="px-3 py-1 bg-blue-100 dark:bg-blue-900/20 text-blue-800 dark:text-blue-200 rounded-full text-sm"
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

---

## Testing Strategy

### Playwright Setup

**Location:** `cmd/ui/frontend/` (frontend directory)

**Installation:**
```bash
cd cmd/ui/frontend
npm install -D @playwright/test
npx playwright install
```

**Configuration:** `cmd/ui/frontend/playwright.config.ts`

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
    command: 'cd ../../.. && go run main.go ui --port 5556',
    url: 'http://localhost:5556',
    reuseExistingServer: !process.env.CI,
    timeout: 30_000,
  },
})
```

### Test Structure

```
cmd/ui/frontend/
├── tests/
│   ├── e2e/
│   │   ├── osk-sidebar.spec.ts
│   │   ├── osk-cluster-detail.spec.ts
│   │   ├── msk-sidebar.spec.ts
│   │   ├── source-switching.spec.ts
│   │   └── upload-state.spec.ts
│   └── fixtures/
│       ├── state-msk-only.json
│       ├── state-osk-only.json
│       └── state-both.json
├── playwright.config.ts
└── package.json
```

### Test Fixtures

**state-osk-only.json** - Sample state with OSK clusters only:
```json
{
  "msk_sources": { "regions": [] },
  "osk_sources": {
    "clusters": [
      {
        "id": "prod-kafka-cluster",
        "bootstrap_servers": ["broker1.example.com:9092"],
        "kafka_admin_client_information": { "topics": {...}, "acls": [...] },
        "discovered_clients": [],
        "metadata": {
          "environment": "production",
          "location": "datacenter-1",
          "kafka_version": "3.6.0",
          "last_scanned": "2026-03-06T10:00:00Z"
        }
      }
    ]
  },
  "schema_registries": [],
  "kcp_build_info": {...},
  "timestamp": "2026-03-06T10:00:00Z"
}
```

### Core Tests

**OSK Sidebar Test:**
```typescript
// tests/e2e/osk-sidebar.spec.ts
import { test, expect } from '@playwright/test'
import stateOSKOnly from '../fixtures/state-osk-only.json'

test.describe('OSK Sidebar', () => {
  test('displays OSK section when OSK clusters present', async ({ page }) => {
    await page.goto('/')
    // Upload state file
    // ...

    await expect(page.locator('text=OPEN SOURCE KAFKA')).toBeVisible()
    await expect(page.locator('text=prod-kafka-cluster')).toBeVisible()
  })

  test('does not display MSK section when no MSK clusters', async ({ page }) => {
    await page.goto('/')
    // Upload OSK-only state
    // ...

    await expect(page.locator('text=AWS MSK')).not.toBeVisible()
  })
})
```

**OSK Cluster Detail Test:**
```typescript
// tests/e2e/osk-cluster-detail.spec.ts
test.describe('OSK Cluster Detail View', () => {
  test('displays correct tabs for OSK cluster', async ({ page }) => {
    // Navigate to OSK cluster
    // ...

    await expect(page.locator('text=Cluster')).toBeVisible()
    await expect(page.locator('text=Topics')).toBeVisible()
    await expect(page.locator('text=ACLs')).toBeVisible()
    await expect(page.locator('text=Metrics')).not.toBeVisible()
  })

  test('displays bootstrap servers in cluster tab', async ({ page }) => {
    await page.click('text=Cluster')

    await expect(page.locator('text=Bootstrap Servers')).toBeVisible()
    await expect(page.locator('text=broker1.example.com:9092')).toBeVisible()
  })
})
```

**Source Switching Test:**
```typescript
// tests/e2e/source-switching.spec.ts
test.describe('Switching Between MSK and OSK', () => {
  test('displays both MSK and OSK sections', async ({ page }) => {
    // Upload state with both sources
    // ...

    await expect(page.locator('text=AWS MSK')).toBeVisible()
    await expect(page.locator('text=OPEN SOURCE KAFKA')).toBeVisible()
  })

  test('can switch from MSK cluster to OSK cluster', async ({ page }) => {
    await page.click('text=msk-cluster-1')
    await expect(page.locator('text=ARN:')).toBeVisible()

    await page.click('text=prod-kafka-cluster')
    await expect(page.locator('text=Bootstrap Servers')).toBeVisible()
    await expect(page.locator('text=ARN:')).not.toBeVisible()
  })
})
```

### Running Tests

**Package.json scripts:**
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

**Commands:**
```bash
# Run all tests headless
npm run test:e2e

# Run with UI mode (interactive)
npm run test:e2e:ui

# Debug specific test
npm run test:e2e:debug osk-sidebar.spec.ts

# Run with visible browser
npm run test:e2e:headed
```

---

## Implementation Checklist

### Backend

- [ ] Fix OSK credentials validation (remove "no auth enabled" error)
- [ ] Update state initialization to always create both `msk_sources` and `osk_sources`
- [ ] Implement `ProcessState` to handle unified source model
- [ ] Add `ProcessedSource`, `ProcessedMSKSource`, `ProcessedOSKSource` types
- [ ] Implement state merge logic for OSK clusters (merge by ID)
- [ ] Update OSK scan logic to skip clusters with all auth methods `use: false`
- [ ] Test state file merge scenarios (MSK only, OSK only, both, partial rescans)

### Frontend

- [ ] Add TypeScript types (`ProcessedSource`, `OSKCluster`, etc.)
- [ ] Add type guard functions (`isMSKSource`, `isOSKSource`)
- [ ] Update Zustand store (add `selectedSourceType`, `selectedOSKClusterId`, actions)
- [ ] Create `MSKSourceSection` component
- [ ] Create `OSKSourceSection` component
- [ ] Update `Sidebar` to conditionally render both sections
- [ ] Create `OSKClusterReport` component
- [ ] Create `OSKClusterHeader` component
- [ ] Create `OSKClusterOverview` component (Cluster tab)
- [ ] Update `Explore` router to handle OSK cluster view
- [ ] Reuse existing components for Topics/ACLs/Connectors tabs
- [ ] Update Summary view to only show MSK data (with note)

### Testing

- [ ] Install Playwright in frontend directory
- [ ] Create `playwright.config.ts`
- [ ] Create test fixtures (state-msk-only, state-osk-only, state-both)
- [ ] Write OSK sidebar tests
- [ ] Write OSK cluster detail tests
- [ ] Write source switching tests
- [ ] Write upload state tests
- [ ] Add test npm scripts to package.json
- [ ] Verify tests pass locally

### Documentation

- [ ] Update CLAUDE.md with OSK UI details
- [ ] Update README with Playwright testing instructions
- [ ] Add screenshots of OSK UI to docs (post-implementation)

---

## Future Work (Out of Scope)

- OSK migration workflow (Migrate tab)
- OSK client discovery
- OSK-specific metrics/monitoring integration
- Multiple OSK credential file support
- OSK cluster health checks
- Browser automation MCP server for assisted UI design

---

## Success Criteria

1. ✅ OSK clusters appear in sidebar under "OPEN SOURCE KAFKA" section
2. ✅ Clicking OSK cluster shows detail view with appropriate tabs
3. ✅ OSK cluster detail shows bootstrap servers, metadata, topics, ACLs, connectors
4. ✅ State files with MSK only, OSK only, or both render correctly
5. ✅ Playwright tests cover core OSK UI functionality
6. ✅ All existing MSK functionality remains unchanged
7. ✅ State file always contains both `msk_sources` and `osk_sources`
