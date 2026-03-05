import { create } from 'zustand'
import { devtools } from 'zustand/middleware'
import { useShallow } from 'zustand/react/shallow'
import type { Cluster, Region } from '@/types'
import type { TerraformFiles } from '@/components/migration/wizards/types'
import type { ProcessedState, SchemaRegistry } from '@/types/api/state'
import { DEFAULT_TABS, DEFAULTS, WIZARD_TYPES } from '@/constants'
import type { WizardType } from '@/types'
import { getClusterArn } from '@/lib/clusterUtils'

// ============================================================================
// INTERFACES - Unified date filter pattern
// ============================================================================
interface DateFilters {
  startDate: Date | undefined
  endDate: Date | undefined
}

interface RegionState extends DateFilters {
  activeCostsTab: string
}

interface WorkloadData {
  [clusterKey: string]: {
    avgIngressThroughput: string
    peakIngressThroughput: string
    avgEgressThroughput: string
    peakEgressThroughput: string
    retentionDays: string
    partitions: string
    replicationFactor: string
    localRetentionHours: string
  }
}

interface MigrationAssets {
  [clusterKey: string]: {
    [WIZARD_TYPES.TARGET_INFRA]: TerraformFiles | null
    [WIZARD_TYPES.MIGRATION_INFRA]: TerraformFiles | null
    [WIZARD_TYPES.MIGRATION_SCRIPTS]: TerraformFiles | null
    [WIZARD_TYPES.MIGRATE_SCHEMAS]: TerraformFiles | null
    [WIZARD_TYPES.MIGRATE_TOPICS]: TerraformFiles | null
    [WIZARD_TYPES.MIGRATE_ACLS]: TerraformFiles | null
  }
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

function createDefaultDateFilters(): DateFilters {
  return {
    startDate: undefined,
    endDate: undefined,
  }
}

function createDefaultRegionState(): RegionState {
  return {
    ...createDefaultDateFilters(),
    activeCostsTab: DEFAULT_TABS.COSTS,
  }
}

function updateDateFilter(current: DateFilters, update: Partial<DateFilters>): DateFilters {
  return {
    ...current,
    ...update,
  }
}

// ============================================================================
// STORE INTERFACE
// ============================================================================

type ViewType = 'summary' | 'region' | 'cluster' | 'tco-inputs' | 'schema-registries'

interface AppState {
  // Session ID - Generated once per page load for multi-session support
  sessionId: string

  // Data state - Single root object from backend
  kcpState: ProcessedState | null

  // Selection state
  selectedView: ViewType | null
  selectedRegionName: string | null
  selectedClusterArn: string | null

  // TCO workload data (keyed by ARN)
  tcoWorkloadData: WorkloadData

  // Date filters (cluster-specific, keyed by ARN)
  clusterDateFilters: Record<string, DateFilters>

  // Region-specific state
  regionState: Record<string, RegionState>

  // Summary-specific date filters
  summaryDateFilters: DateFilters

  // Migration assets (keyed by ARN)
  migrationAssets: MigrationAssets

  // UI state
  isProcessing: boolean
  error: string | null
  activeMetricsTab: string
  expandedMigrationCluster: string | null
  migrationAssetTabs: Record<string, string> // Key: ARN, Value: tab id
  preselectedMetric: string | null

  // Actions
  getSessionId: () => string
  setKcpState: (state: ProcessedState) => void
  selectSummary: () => void
  selectRegion: (regionName: string) => void
  selectCluster: (regionName: string, clusterArn: string, preselectedMetric?: string) => void
  selectTCOInputs: () => void
  selectSchemaRegistries: () => void
  clearSelection: () => void

  // TCO Actions
  setTCOWorkloadValue: (
    clusterKey: string,
    field: keyof WorkloadData[string],
    value: string
  ) => void
  initializeTCOData: (clusters: Array<{ arn: string; key: string }>) => void

  // Date filter actions (cluster-specific, using ARN)
  setClusterStartDate: (clusterArn: string, date: Date | undefined) => void
  setClusterEndDate: (clusterArn: string, date: Date | undefined) => void
  clearClusterDates: (clusterArn: string) => void
  getClusterDateFilters: (clusterArn: string) => DateFilters

  // Region-specific actions for costs
  setRegionStartDate: (region: string, date: Date | undefined) => void
  setRegionEndDate: (region: string, date: Date | undefined) => void
  clearRegionDates: (region: string) => void
  setRegionActiveCostsTab: (region: string, tab: string) => void
  getRegionState: (region: string) => RegionState

  // Summary-specific actions for date filters
  setSummaryStartDate: (date: Date | undefined) => void
  setSummaryEndDate: (date: Date | undefined) => void
  clearSummaryDates: () => void
  getSummaryDateFilters: () => DateFilters

  // UI actions
  setIsProcessing: (processing: boolean) => void
  setError: (error: string | null) => void
  setActiveMetricsTab: (tab: string) => void
  setExpandedMigrationCluster: (clusterKey: string | null) => void
  setMigrationAssetTab: (clusterKey: string, tabId: string) => void
  getMigrationAssetTab: (clusterKey: string) => string | undefined

  // Migration assets actions
  setTerraformFiles: (clusterKey: string, wizardType: WizardType, files: TerraformFiles) => void
  getTerraformFiles: (clusterKey: string, wizardType: WizardType) => TerraformFiles | null
}

export const useAppStore = create<AppState>()(
  devtools(
    (set, get) => ({
      // Initial state
      sessionId: crypto.randomUUID(),
      kcpState: null,
      selectedView: null,
      selectedRegionName: null,
      selectedClusterArn: null,
      tcoWorkloadData: {},
      clusterDateFilters: {},
      regionState: {},
      summaryDateFilters: createDefaultDateFilters(),
      migrationAssets: {},
      isProcessing: false,
      error: null,
      activeMetricsTab: DEFAULT_TABS.METRICS,
      expandedMigrationCluster: null,
      migrationAssetTabs: {},
      preselectedMetric: null,

      // Actions
      getSessionId: () => get().sessionId,

      setKcpState: (kcpState) => set({ kcpState }, false, 'setKcpState'),

      selectSummary: () =>
        set(
          {
            selectedView: 'summary',
            selectedRegionName: null,
            selectedClusterArn: null,
          },
          false,
          'selectSummary'
        ),

      selectRegion: (regionName) =>
        set(
          {
            selectedView: 'region',
            selectedRegionName: regionName,
            selectedClusterArn: null,
          },
          false,
          'selectRegion'
        ),

      selectCluster: (regionName, clusterArn, preselectedMetric) =>
        set(
          {
            selectedView: 'cluster',
            selectedRegionName: regionName,
            selectedClusterArn: clusterArn,
            preselectedMetric: preselectedMetric || null,
          },
          false,
          'selectCluster'
        ),

      selectTCOInputs: () =>
        set(
          {
            selectedView: 'tco-inputs',
            selectedRegionName: null,
            selectedClusterArn: null,
          },
          false,
          'selectTCOInputs'
        ),

      selectSchemaRegistries: () =>
        set(
          {
            selectedView: 'schema-registries',
            selectedRegionName: null,
            selectedClusterArn: null,
          },
          false,
          'selectSchemaRegistries'
        ),

      clearSelection: () =>
        set(
          {
            selectedView: null,
            selectedRegionName: null,
            selectedClusterArn: null,
          },
          false,
          'clearSelection'
        ),

      setIsProcessing: (processing) => set({ isProcessing: processing }, false, 'setIsProcessing'),

      setError: (error) => set({ error }, false, 'setError'),

      setActiveMetricsTab: (tab) => set({ activeMetricsTab: tab }, false, 'setActiveMetricsTab'),

      setExpandedMigrationCluster: (clusterKey) =>
        set({ expandedMigrationCluster: clusterKey }, false, 'setExpandedMigrationCluster'),

      setMigrationAssetTab: (clusterKey, tabId) =>
        set(
          (state) => ({
            migrationAssetTabs: {
              ...state.migrationAssetTabs,
              [clusterKey]: tabId,
            },
          }),
          false,
          'setMigrationAssetTab'
        ),

      getMigrationAssetTab: (clusterKey) => {
        const state = get()
        return state.migrationAssetTabs[clusterKey]
      },

      // TCO Actions
      setTCOWorkloadValue: (clusterKey, field, value) =>
        set(
          (state) => ({
            tcoWorkloadData: {
              ...state.tcoWorkloadData,
              [clusterKey]: {
                ...state.tcoWorkloadData[clusterKey],
                [field]: value,
              },
            },
          }),
          false,
          'setTCOWorkloadValue'
        ),

      initializeTCOData: (clusters) =>
        set(
          (state) => {
            const newData: WorkloadData = {}
            clusters.forEach((cluster) => {
              // Keep existing data if it exists, otherwise initialize with defaults
              newData[cluster.key] = state.tcoWorkloadData[cluster.key] || {
                avgIngressThroughput: '',
                peakIngressThroughput: '',
                avgEgressThroughput: '',
                peakEgressThroughput: '',
                retentionDays: '',
                partitions: DEFAULTS.PARTITIONS,
                replicationFactor: DEFAULTS.REPLICATION_FACTOR,
                localRetentionHours: '',
              }
            })
            return { tcoWorkloadData: newData }
          },
          false,
          'initializeTCOData'
        ),

      // Migration assets actions
      setTerraformFiles: (clusterKey, wizardType, files) =>
        set(
          (state) => ({
            migrationAssets: {
              ...state.migrationAssets,
              [clusterKey]: {
                ...state.migrationAssets[clusterKey],
                [wizardType]: files,
              },
            },
          }),
          false,
          'setTerraformFiles'
        ),

      getTerraformFiles: (clusterKey, wizardType) => {
        const state = get()
        return state.migrationAssets[clusterKey]?.[wizardType] || null
      },

      // Cluster-specific date filter actions - using ARN as key
      setClusterStartDate: (clusterArn, date) =>
        set(
          (state) => {
            return {
              clusterDateFilters: {
                ...state.clusterDateFilters,
                [clusterArn]: updateDateFilter(
                  state.clusterDateFilters[clusterArn] || createDefaultDateFilters(),
                  {
                    startDate: date,
                  }
                ),
              },
            }
          },
          false,
          'setClusterStartDate'
        ),

      setClusterEndDate: (clusterArn, date) =>
        set(
          (state) => {
            return {
              clusterDateFilters: {
                ...state.clusterDateFilters,
                [clusterArn]: updateDateFilter(
                  state.clusterDateFilters[clusterArn] || createDefaultDateFilters(),
                  {
                    endDate: date,
                  }
                ),
              },
            }
          },
          false,
          'setClusterEndDate'
        ),

      clearClusterDates: (clusterArn) =>
        set(
          (state) => {
            const { [clusterArn]: removed, ...rest } = state.clusterDateFilters
            // removed is intentionally unused - we only need rest
            void removed
            return { clusterDateFilters: rest }
          },
          false,
          'clearClusterDates'
        ),

      getClusterDateFilters: (clusterArn) => {
        const state = get()
        return state.clusterDateFilters[clusterArn] || createDefaultDateFilters()
      },

      // Region-specific actions for costs - using unified helpers
      setRegionStartDate: (region, date) =>
        set(
          (state) => {
            const current = state.regionState[region] || createDefaultRegionState()
            return {
              regionState: {
                ...state.regionState,
                [region]: {
                  ...current,
                  startDate: date,
                },
              },
            }
          },
          false,
          'setRegionStartDate'
        ),

      setRegionEndDate: (region, date) =>
        set(
          (state) => {
            const current = state.regionState[region] || createDefaultRegionState()
            return {
              regionState: {
                ...state.regionState,
                [region]: {
                  ...current,
                  endDate: date,
                },
              },
            }
          },
          false,
          'setRegionEndDate'
        ),

      clearRegionDates: (region) =>
        set(
          (state) => ({
            regionState: {
              ...state.regionState,
              [region]: {
                ...createDefaultRegionState(),
                activeCostsTab: state.regionState[region]?.activeCostsTab || DEFAULT_TABS.COSTS,
              },
            },
          }),
          false,
          'clearRegionDates'
        ),

      setRegionActiveCostsTab: (region, tab) =>
        set(
          (state) => ({
            regionState: {
              ...state.regionState,
              [region]: {
                ...(state.regionState[region] || createDefaultRegionState()),
                activeCostsTab: tab,
              },
            },
          }),
          false,
          'setRegionActiveCostsTab'
        ),

      getRegionState: (region) => {
        const state = get()
        return state.regionState[region] || createDefaultRegionState()
      },

      // Summary-specific date filter actions - using unified helpers
      setSummaryStartDate: (date) =>
        set(
          (state) => ({
            summaryDateFilters: updateDateFilter(
              state.summaryDateFilters || createDefaultDateFilters(),
              { startDate: date }
            ),
          }),
          false,
          'setSummaryStartDate'
        ),

      setSummaryEndDate: (date) =>
        set(
          (state) => ({
            summaryDateFilters: updateDateFilter(
              state.summaryDateFilters || createDefaultDateFilters(),
              { endDate: date }
            ),
          }),
          false,
          'setSummaryEndDate'
        ),

      clearSummaryDates: () =>
        set({ summaryDateFilters: createDefaultDateFilters() }, false, 'clearSummaryDates'),

      getSummaryDateFilters: () => {
        const state = get()
        return state.summaryDateFilters
      },
    }),
    {
      name: 'kcp-app-store',
    }
  )
)

// ============================================================================
// SELECTOR HOOKS - Computed state from the store
// ============================================================================

// Stable empty arrays and objects to prevent infinite re-renders
const EMPTY_REGIONS: Region[] = []
const EMPTY_SCHEMA_REGISTRIES: SchemaRegistry[] = []
const DEFAULT_DATE_FILTERS: DateFilters = {
  startDate: undefined,
  endDate: undefined,
}
const DEFAULT_REGION_STATE: RegionState = {
  startDate: undefined,
  endDate: undefined,
  activeCostsTab: DEFAULT_TABS.COSTS,
}

/**
 * Get the session ID for this browser session
 */
export const useSessionId = () => useAppStore((state) => state.sessionId)

/**
 * Get the full KCP state
 */
export const useKcpState = () => useAppStore((state) => state.kcpState)

/**
 * Get all regions from KCP state
 */
export const useRegions = () => useAppStore((state) => state.kcpState?.regions ?? EMPTY_REGIONS)

/**
 * Get schema registries from KCP state
 */
export const useSchemaRegistries = () =>
  useAppStore((state) => state.kcpState?.schema_registries ?? EMPTY_SCHEMA_REGISTRIES)

/**
 * Get the currently selected cluster with its region name
 * Returns null if no cluster is selected or cluster not found
 */
export const useSelectedCluster = () => {
  return useAppStore(
    useShallow((state) => {
      if (!state.selectedClusterArn || !state.kcpState || !state.kcpState.regions) return null

      // Search through all regions to find the cluster with matching ARN
      for (const region of state.kcpState.regions) {
        if (!region.clusters) continue
        const cluster = region.clusters.find((c) => {
          const arn = getClusterArn(c)
          return arn && arn === state.selectedClusterArn
        })
        if (cluster) {
          return { cluster, regionName: region.name }
        }
      }

      return null
    })
  )
}

/**
 * Get the currently selected region
 * Returns null if no region is selected or region not found
 */
export const useSelectedRegion = () => {
  return useAppStore(
    useShallow((state) => {
      if (!state.selectedRegionName || !state.kcpState || !state.kcpState.regions) return null
      return state.kcpState.regions.find((r) => r.name === state.selectedRegionName) || null
    })
  )
}

/**
 * Get UI state (processing and error)
 */
export const useUIState = () =>
  useAppStore(
    useShallow((state) => ({
      isProcessing: state.isProcessing,
      error: state.error,
    }))
  )

/**
 * Hook to get date filters for the currently selected cluster
 */
export const useCurrentClusterDateFilters = () => {
  return useAppStore(
    useShallow((state) => {
      if (!state.selectedClusterArn) {
        return DEFAULT_DATE_FILTERS
      }
      return state.clusterDateFilters[state.selectedClusterArn] || DEFAULT_DATE_FILTERS
    })
  )
}

/**
 * Hook to get cluster-specific date filters and actions by ARN
 */
export const useClusterDateFilters = (clusterArn: string) => {
  const { clusterDateFilters, setClusterStartDate, setClusterEndDate, clearClusterDates } =
    useAppStore()

  const filters = clusterDateFilters[clusterArn] || DEFAULT_DATE_FILTERS

  return {
    startDate: filters.startDate,
    endDate: filters.endDate,
    setStartDate: (date: Date | undefined) => setClusterStartDate(clusterArn, date),
    setEndDate: (date: Date | undefined) => setClusterEndDate(clusterArn, date),
    clearDates: () => clearClusterDates(clusterArn),
  }
}

// Hook to get region-specific state and actions for costs
export const useRegionCostFilters = (region: string) => {
  const {
    regionState,
    setRegionStartDate,
    setRegionEndDate,
    clearRegionDates,
    setRegionActiveCostsTab,
  } = useAppStore()

  const state = regionState[region] || DEFAULT_REGION_STATE

  return {
    startDate: state.startDate,
    endDate: state.endDate,
    activeCostsTab: state.activeCostsTab,
    setStartDate: (date: Date | undefined) => setRegionStartDate(region, date),
    setEndDate: (date: Date | undefined) => setRegionEndDate(region, date),
    clearDates: () => clearRegionDates(region),
    setActiveCostsTab: (tab: string) => setRegionActiveCostsTab(region, tab),
  }
}

// Hook to get summary-specific date filters and actions
export const useSummaryDateFilters = () => {
  const { summaryDateFilters, setSummaryStartDate, setSummaryEndDate, clearSummaryDates } =
    useAppStore()

  return {
    startDate: summaryDateFilters.startDate,
    endDate: summaryDateFilters.endDate,
    setStartDate: (date: Date | undefined) => setSummaryStartDate(date),
    setEndDate: (date: Date | undefined) => setSummaryEndDate(date),
    clearDates: () => clearSummaryDates(),
  }
}

// Utility function to get cluster data by ARN
export const getClusterDataByArn = (arn: string): Cluster | null => {
  const state = useAppStore.getState()
  const kcpState = state.kcpState

  if (!kcpState?.regions || !arn) {
    return null
  }

  for (const region of kcpState.regions) {
    const cluster = region.clusters?.find(
      (c) => c.arn === arn || c.aws_client_information?.msk_cluster_config?.ClusterArn === arn
    )
    if (cluster) {
      return cluster
    }
  }

  return null
}

// Utility function to get all schema registries from state
export const getAllSchemaRegistries = (): SchemaRegistry[] => {
  const state = useAppStore.getState()
  const kcpState = state.kcpState

  if (!kcpState?.schema_registries) {
    return []
  }

  return kcpState.schema_registries
}
