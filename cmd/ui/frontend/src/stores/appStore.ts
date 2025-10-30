import { create } from 'zustand'
import { devtools } from 'zustand/middleware'
import type { Cluster, Region } from '@/types'
import type { TerraformFiles } from '@/components/wizards/types'

// ============================================================================
// CONSTANTS
// ============================================================================
const DEFAULT_ACTIVE_COSTS_TAB = 'chart'
const DEFAULT_METRICS_TAB = 'chart'

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
    'target-infra': TerraformFiles | null
    'migration-infra': TerraformFiles | null
    'migration-scripts': TerraformFiles | null
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
// HELPER FUNCTIONS - Reduce duplication
// ============================================================================
const createClusterKey = (region: string, cluster: string): string => `${region}:${cluster}`

const createDefaultDateFilters = (): DateFilters => ({
  startDate: undefined,
  endDate: undefined,
})

const createDefaultRegionState = (): RegionState => ({
  ...createDefaultDateFilters(),
  activeCostsTab: DEFAULT_ACTIVE_COSTS_TAB,
})

/**
 * Updates a date filter object, preserving existing values
 */
const updateDateFilter = <T extends DateFilters>(
  current: T | undefined,
  updates: Partial<DateFilters>
): T => {
  const base = (current || {}) as T
  return {
    ...base,
    ...updates,
  } as T
}

/**
 * Creates a selection state object with all flags properly set
 */
const createSelectionState = (selection: {
  cluster?: { cluster: Cluster; regionName: string } | null
  region?: Region | null
  summary?: boolean
  tcoInputs?: boolean
  schemaRegistries?: boolean
  preselectedMetric?: string | null
}) => ({
  selectedCluster: selection.cluster ?? null,
  selectedRegion: selection.region ?? null,
  selectedSummary: selection.summary ?? false,
  selectedTCOInputs: selection.tcoInputs ?? false,
  selectedSchemaRegistries: selection.schemaRegistries ?? false,
  preselectedMetric: selection.preselectedMetric ?? null,
})

interface AppState {
  // Data
  regions: Region[]
  schemaRegistries: SchemaRegistry[]

  // Selection state
  selectedCluster: { cluster: Cluster; regionName: string } | null
  selectedRegion: Region | null
  selectedSummary: boolean
  selectedTCOInputs: boolean
  selectedSchemaRegistries: boolean
  preselectedMetric: string | null

  // TCO Inputs data
  tcoWorkloadData: WorkloadData

  // Migration assets (terraform files per cluster per wizard type)
  migrationAssets: MigrationAssets

  // Date filters (per cluster) - using unified DateFilters
  clusterDateFilters: Record<string, DateFilters> // Key: "region:cluster"

  // Region-specific state for costs (per region)
  regionState: Record<string, RegionState> // Key: "region"

  // Summary-specific state for date filters
  summaryDateFilters: DateFilters

  // UI state
  isProcessing: boolean
  error: string | null
  activeMetricsTab: string

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

  // Migration assets actions
  setTerraformFiles: (
    clusterKey: string,
    wizardType: 'target-infra' | 'migration-infra' | 'migration-scripts',
    files: TerraformFiles
  ) => void
  getTerraformFiles: (
    clusterKey: string,
    wizardType: 'target-infra' | 'migration-infra' | 'migration-scripts'
  ) => TerraformFiles | null
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
      preselectedMetric: null,
      tcoWorkloadData: {},
      migrationAssets: {},
      clusterDateFilters: {},
      regionState: {},
      summaryDateFilters: createDefaultDateFilters(),
      isProcessing: false,
      error: null,
      activeMetricsTab: DEFAULT_METRICS_TAB,

      // Data actions
      setRegions: (regions) => set({ regions }, false, 'setRegions'),
      setSchemaRegistries: (schemaRegistries) =>
        set({ schemaRegistries }, false, 'setSchemaRegistries'),

      // Selection actions - using helper function to eliminate duplication
      setSelectedCluster: (cluster, regionName, preselectedMetric) =>
        set(
          createSelectionState({
            cluster: { cluster, regionName },
            preselectedMetric: preselectedMetric || null,
          }),
          false,
          'setSelectedCluster'
        ),

      setSelectedRegion: (region) =>
        set(
          (state) => ({
            ...createSelectionState({ region }),
            // Initialize region state with default values if it doesn't exist
            regionState: {
              ...state.regionState,
              [region.name]: state.regionState[region.name] || createDefaultRegionState(),
            },
          }),
          false,
          'setSelectedRegion'
        ),

      setSelectedSummary: () =>
        set(createSelectionState({ summary: true }), false, 'setSelectedSummary'),

      setSelectedTCOInputs: () =>
        set(createSelectionState({ tcoInputs: true }), false, 'setSelectedTCOInputs'),

      setSelectedSchemaRegistries: () =>
        set(createSelectionState({ schemaRegistries: true }), false, 'setSelectedSchemaRegistries'),

      clearSelection: () => set(createSelectionState({}), false, 'clearSelection'),

      // Cluster-specific date filter actions - using unified helpers
      setClusterStartDate: (region, cluster, date) =>
        set(
          (state) => {
            const key = createClusterKey(region, cluster)
            return {
              clusterDateFilters: {
                ...state.clusterDateFilters,
                [key]: updateDateFilter(state.clusterDateFilters[key], { startDate: date }),
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
                [key]: updateDateFilter(state.clusterDateFilters[key], { endDate: date }),
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
          (state) => ({
            regionState: {
              ...state.regionState,
              [region]: updateDateFilter(state.regionState[region], { startDate: date }),
            },
          }),
          false,
          'setRegionStartDate'
        ),

      setRegionEndDate: (region, date) =>
        set(
          (state) => ({
            regionState: {
              ...state.regionState,
              [region]: updateDateFilter(state.regionState[region], { endDate: date }),
            },
          }),
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
                activeCostsTab:
                  state.regionState[region]?.activeCostsTab || DEFAULT_ACTIVE_COSTS_TAB,
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
            summaryDateFilters: updateDateFilter(state.summaryDateFilters, { startDate: date }),
          }),
          false,
          'setSummaryStartDate'
        ),

      setSummaryEndDate: (date) =>
        set(
          (state) => ({
            summaryDateFilters: updateDateFilter(state.summaryDateFilters, { endDate: date }),
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

      // UI actions
      setIsProcessing: (processing) => set({ isProcessing: processing }, false, 'setIsProcessing'),

      setError: (error) => set({ error }, false, 'setError'),

      setActiveMetricsTab: (tab) => set({ activeMetricsTab: tab }, false, 'setActiveMetricsTab'),

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
                partitions: '1000',
                replicationFactor: '3',
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
