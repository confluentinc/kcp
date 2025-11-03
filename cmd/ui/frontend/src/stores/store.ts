import { create } from 'zustand'
import { devtools } from 'zustand/middleware'
import type { Cluster, Region } from '@/types'
import type { TerraformFiles } from '@/components/migration/wizards/types'
import { DEFAULT_TABS, DEFAULTS, WIZARD_TYPES } from '@/constants'
import type { WizardType } from '@/types'

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
  }
}

interface SchemaRegistry {
  type: string
  url: string
  subjects: Array<{
    name: string
    schema_type: string
    versions: Array<{
      schema: string
      id: number
      subject: string
      version: number
      schemaType?: string
    }>
    latest_schema: {
      schema: string
      id: number
      subject: string
      version: number
      schemaType?: string
    }
  }>
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

function createClusterKey(region: string, cluster: string): string {
  return `${region}-${cluster}`
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

interface AppState {
  // Data state
  regions: Region[]
  schemaRegistries: SchemaRegistry[]
  selectedCluster: { cluster: Cluster; regionName: string } | null
  selectedRegion: Region | null
  selectedSummary: boolean
  selectedTCOInputs: boolean
  selectedSchemaRegistries: boolean

  // TCO workload data
  tcoWorkloadData: WorkloadData

  // Date filters (cluster-specific)
  clusterDateFilters: Record<string, DateFilters>

  // Region-specific state
  regionState: Record<string, RegionState>

  // Summary-specific date filters
  summaryDateFilters: DateFilters

  // Migration assets
  migrationAssets: MigrationAssets

  // UI state
  isProcessing: boolean
  error: string | null
  activeMetricsTab: string
  expandedMigrationCluster: string | null
  migrationAssetTabs: Record<string, string> // Key: clusterKey, Value: tab id (migration-infra | target-infra | migration-scripts)
  preselectedMetric: string | null

  // Actions
  setRegions: (regions: Region[]) => void
  setSchemaRegistries: (schemaRegistries: SchemaRegistry[]) => void
  setSelectedCluster: (cluster: Cluster, regionName: string, preselectedMetric?: string) => void
  setSelectedRegion: (region: Region) => void
  setSelectedSummary: () => void
  setSelectedTCOInputs: () => void
  setSelectedSchemaRegistries: () => void
  clearSelection: () => void

  // TCO Actions
  setTCOWorkloadValue: (
    clusterKey: string,
    field: keyof WorkloadData[string],
    value: string
  ) => void
  initializeTCOData: (clusters: Array<{ name: string; regionName: string; key: string }>) => void

  // Date filter actions (cluster-specific)
  setClusterStartDate: (region: string, cluster: string, date: Date | undefined) => void
  setClusterEndDate: (region: string, cluster: string, date: Date | undefined) => void
  clearClusterDates: (region: string, cluster: string) => void
  getClusterDateFilters: (region: string, cluster: string) => DateFilters

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
      regions: [],
      schemaRegistries: [],
      selectedCluster: null,
      selectedRegion: null,
      selectedSummary: false,
      selectedTCOInputs: false,
      selectedSchemaRegistries: false,
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
      setRegions: (regions) => set({ regions }, false, 'setRegions'),

      setSchemaRegistries: (schemaRegistries) =>
        set({ schemaRegistries }, false, 'setSchemaRegistries'),

      setSelectedCluster: (cluster, regionName, preselectedMetric) =>
        set(
          {
            selectedCluster: { cluster, regionName },
            selectedRegion: null,
            selectedSummary: false,
            selectedTCOInputs: false,
            selectedSchemaRegistries: false,
            preselectedMetric: preselectedMetric || null,
          },
          false,
          'setSelectedCluster'
        ),

      setSelectedRegion: (region) =>
        set(
          {
            selectedRegion: region,
            selectedCluster: null,
            selectedSummary: false,
            selectedTCOInputs: false,
            selectedSchemaRegistries: false,
          },
          false,
          'setSelectedRegion'
        ),

      setSelectedSummary: () =>
        set(
          {
            selectedSummary: true,
            selectedCluster: null,
            selectedRegion: null,
            selectedTCOInputs: false,
            selectedSchemaRegistries: false,
          },
          false,
          'setSelectedSummary'
        ),

      setSelectedTCOInputs: () =>
        set(
          {
            selectedTCOInputs: true,
            selectedCluster: null,
            selectedRegion: null,
            selectedSummary: false,
            selectedSchemaRegistries: false,
          },
          false,
          'setSelectedTCOInputs'
        ),

      setSelectedSchemaRegistries: () =>
        set(
          {
            selectedSchemaRegistries: true,
            selectedCluster: null,
            selectedRegion: null,
            selectedSummary: false,
            selectedTCOInputs: false,
          },
          false,
          'setSelectedSchemaRegistries'
        ),

      clearSelection: () =>
        set(
          {
            selectedCluster: null,
            selectedRegion: null,
            selectedSummary: false,
            selectedTCOInputs: false,
            selectedSchemaRegistries: false,
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

      // Cluster-specific date filter actions - using unified helpers
      setClusterStartDate: (region, cluster, date) =>
        set(
          (state) => {
            const key = createClusterKey(region, cluster)
            return {
              clusterDateFilters: {
                ...state.clusterDateFilters,
                [key]: updateDateFilter(
                  state.clusterDateFilters[key] || createDefaultDateFilters(),
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

      setClusterEndDate: (region, cluster, date) =>
        set(
          (state) => {
            const key = createClusterKey(region, cluster)
            return {
              clusterDateFilters: {
                ...state.clusterDateFilters,
                [key]: updateDateFilter(
                  state.clusterDateFilters[key] || createDefaultDateFilters(),
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

      clearClusterDates: (region, cluster) =>
        set(
          (state) => {
            const key = createClusterKey(region, cluster)
            const { [key]: removed, ...rest } = state.clusterDateFilters
            // removed is intentionally unused - we only need rest
            void removed
            return { clusterDateFilters: rest }
          },
          false,
          'clearClusterDates'
        ),

      getClusterDateFilters: (region, cluster) => {
        const state = get()
        const key = createClusterKey(region, cluster)
        return state.clusterDateFilters[key] || createDefaultDateFilters()
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

// Selector helpers for common patterns
export const useSelectedCluster = () => useAppStore((state) => state.selectedCluster)
export const useSelectedRegion = () => useAppStore((state) => state.selectedRegion)
export const useRegions = () => useAppStore((state) => state.regions)
export const useUIState = () =>
  useAppStore((state) => ({
    isProcessing: state.isProcessing,
    error: state.error,
  }))

// Hook to get date filters for the currently selected cluster
export const useCurrentClusterDateFilters = () => {
  return useAppStore((state) => {
    if (!state.selectedCluster) {
      return createDefaultDateFilters()
    }

    const key = createClusterKey(
      state.selectedCluster.regionName,
      state.selectedCluster.cluster.name
    )
    return state.clusterDateFilters[key] || createDefaultDateFilters()
  })
}

// Hook to get cluster-specific date filters and actions
export const useClusterDateFilters = (region: string, cluster: string) => {
  const { clusterDateFilters, setClusterStartDate, setClusterEndDate, clearClusterDates } =
    useAppStore()

  const key = createClusterKey(region, cluster)
  const filters = clusterDateFilters[key] || createDefaultDateFilters()

  return {
    startDate: filters.startDate,
    endDate: filters.endDate,
    setStartDate: (date: Date | undefined) => setClusterStartDate(region, cluster, date),
    setEndDate: (date: Date | undefined) => setClusterEndDate(region, cluster, date),
    clearDates: () => clearClusterDates(region, cluster),
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

  const state = regionState[region] || createDefaultRegionState()

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
