import { create } from 'zustand'
import { devtools } from 'zustand/middleware'
import type { Cluster, Region } from '@/types'

// Cluster-specific date filters
interface ClusterDateFilters {
  startDate: Date | undefined
  endDate: Date | undefined
}

// Region-specific state for costs
interface RegionState {
  startDate: Date | undefined
  endDate: Date | undefined
  activeCostsTab: string
}

// Summary-specific state for date filters
interface SummaryDateFilters {
  startDate: Date | undefined
  endDate: Date | undefined
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

  // Date filters (per cluster)
  clusterDateFilters: Record<string, ClusterDateFilters> // Key: "region:cluster"

  // Region-specific state for costs (per region)
  regionState: Record<string, RegionState> // Key: "region"

  // Summary-specific state for date filters
  summaryDateFilters: SummaryDateFilters

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
  getClusterDateFilters: (region: string, cluster: string) => ClusterDateFilters

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
  getSummaryDateFilters: () => SummaryDateFilters

  // UI actions
  setIsProcessing: (processing: boolean) => void
  setError: (error: string | null) => void
  setActiveMetricsTab: (tab: string) => void
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
      clusterDateFilters: {},
      regionState: {},
      summaryDateFilters: {
        startDate: undefined,
        endDate: undefined,
      },
      isProcessing: false,
      error: null,
      activeMetricsTab: 'chart',

      // Data actions
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
            preselectedMetric: null,
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
            preselectedMetric: null,
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
            preselectedMetric: null,
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
            preselectedMetric: null,
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
            preselectedMetric: null,
          },
          false,
          'clearSelection'
        ),

      // Cluster-specific date filter actions
      setClusterStartDate: (region, cluster, date) => {
        const key = `${region}:${cluster}`
        const state = get()
        set(
          {
            clusterDateFilters: {
              ...state.clusterDateFilters,
              [key]: {
                ...state.clusterDateFilters[key],
                startDate: date,
                endDate: state.clusterDateFilters[key]?.endDate,
              },
            },
          },
          false,
          'setClusterStartDate'
        )
      },

      setClusterEndDate: (region, cluster, date) => {
        const key = `${region}:${cluster}`
        const state = get()
        set(
          {
            clusterDateFilters: {
              ...state.clusterDateFilters,
              [key]: {
                ...state.clusterDateFilters[key],
                startDate: state.clusterDateFilters[key]?.startDate,
                endDate: date,
              },
            },
          },
          false,
          'setClusterEndDate'
        )
      },

      clearClusterDates: (region, cluster) => {
        const key = `${region}:${cluster}`
        const state = get()
        const newFilters = { ...state.clusterDateFilters }
        delete newFilters[key]
        set(
          {
            clusterDateFilters: newFilters,
          },
          false,
          'clearClusterDates'
        )
      },

      getClusterDateFilters: (region, cluster) => {
        const state = get()
        const key = `${region}:${cluster}`
        return state.clusterDateFilters[key] || { startDate: undefined, endDate: undefined }
      },

      // Region-specific actions for costs
      setRegionStartDate: (region, date) => {
        const state = get()
        set(
          {
            regionState: {
              ...state.regionState,
              [region]: {
                ...state.regionState[region],
                startDate: date,
                endDate: state.regionState[region]?.endDate,
                activeCostsTab: state.regionState[region]?.activeCostsTab || 'chart',
              },
            },
          },
          false,
          'setRegionStartDate'
        )
      },

      setRegionEndDate: (region, date) => {
        const state = get()
        set(
          {
            regionState: {
              ...state.regionState,
              [region]: {
                ...state.regionState[region],
                startDate: state.regionState[region]?.startDate,
                endDate: date,
                activeCostsTab: state.regionState[region]?.activeCostsTab || 'chart',
              },
            },
          },
          false,
          'setRegionEndDate'
        )
      },

      clearRegionDates: (region) => {
        const state = get()
        set(
          {
            regionState: {
              ...state.regionState,
              [region]: {
                ...state.regionState[region],
                startDate: undefined,
                endDate: undefined,
                activeCostsTab: state.regionState[region]?.activeCostsTab || 'chart',
              },
            },
          },
          false,
          'clearRegionDates'
        )
      },

      setRegionActiveCostsTab: (region, tab) => {
        const state = get()
        set(
          {
            regionState: {
              ...state.regionState,
              [region]: {
                ...state.regionState[region],
                startDate: state.regionState[region]?.startDate,
                endDate: state.regionState[region]?.endDate,
                activeCostsTab: tab,
              },
            },
          },
          false,
          'setRegionActiveCostsTab'
        )
      },

      getRegionState: (region) => {
        const state = get()
        return (
          state.regionState[region] || {
            startDate: undefined,
            endDate: undefined,
            activeCostsTab: 'chart',
          }
        )
      },

      // Summary-specific date filter actions
      setSummaryStartDate: (date) => {
        set(
          (state) => ({
            summaryDateFilters: {
              ...state.summaryDateFilters,
              startDate: date,
            },
          }),
          false,
          'setSummaryStartDate'
        )
      },

      setSummaryEndDate: (date) => {
        set(
          (state) => ({
            summaryDateFilters: {
              ...state.summaryDateFilters,
              endDate: date,
            },
          }),
          false,
          'setSummaryEndDate'
        )
      },

      clearSummaryDates: () => {
        set(
          {
            summaryDateFilters: {
              startDate: undefined,
              endDate: undefined,
            },
          },
          false,
          'clearSummaryDates'
        )
      },

      getSummaryDateFilters: () => {
        const state = get()
        return state.summaryDateFilters
      },

      // UI actions
      setIsProcessing: (processing: boolean) =>
        set({ isProcessing: processing }, false, 'setIsProcessing'),

      setError: (error: string | null) => set({ error }, false, 'setError'),

      setActiveMetricsTab: (tab: string) =>
        set({ activeMetricsTab: tab }, false, 'setActiveMetricsTab'),

      // TCO Actions
      setTCOWorkloadValue: (clusterKey: string, field: keyof WorkloadData[string], value: string) =>
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

      initializeTCOData: (clusters: Array<{ name: string; regionName: string; key: string }>) =>
        set(
          (state) => {
            const newData: WorkloadData = {}
            clusters.forEach((cluster) => {
              // Keep existing data if it exists, otherwise initialize with zeros
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
      return { startDate: undefined, endDate: undefined }
    }

    const key = `${state.selectedCluster.regionName}:${state.selectedCluster.cluster.name}`
    return state.clusterDateFilters[key] || { startDate: undefined, endDate: undefined }
  })
}

// Hook to get cluster-specific date filters and actions
export const useClusterDateFilters = (region: string, cluster: string) => {
  const { clusterDateFilters, setClusterStartDate, setClusterEndDate, clearClusterDates } =
    useAppStore()

  const key = `${region}:${cluster}`
  const filters = clusterDateFilters[key] || { startDate: undefined, endDate: undefined }

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

  const state = regionState[region] || {
    startDate: undefined,
    endDate: undefined,
    activeCostsTab: 'chart',
  }

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

// Hook to get summary-specific date filters and acciones
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
