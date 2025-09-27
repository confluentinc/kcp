import { create } from 'zustand'
import { devtools } from 'zustand/middleware'
import type { Cluster, Region } from '@/types'

// Cluster-specific date filters
interface ClusterDateFilters {
  startDate: Date | undefined
  endDate: Date | undefined
}

interface AppState {
  // Data
  regions: Region[]

  // Selection state
  selectedCluster: { cluster: Cluster; regionName: string } | null
  selectedRegion: Region | null

  // Date filters (per cluster)
  clusterDateFilters: Record<string, ClusterDateFilters> // Key: "region:cluster"

  // UI state
  isProcessing: boolean
  error: string | null

  // Actions
  setRegions: (regions: Region[]) => void
  setSelectedCluster: (cluster: Cluster, regionName: string) => void
  setSelectedRegion: (region: Region) => void
  clearSelection: () => void

  // Date filter actions (cluster-specific)
  setClusterStartDate: (region: string, cluster: string, date: Date | undefined) => void
  setClusterEndDate: (region: string, cluster: string, date: Date | undefined) => void
  clearClusterDates: (region: string, cluster: string) => void
  getClusterDateFilters: (region: string, cluster: string) => ClusterDateFilters

  // UI actions
  setIsProcessing: (processing: boolean) => void
  setError: (error: string | null) => void
}

export const useAppStore = create<AppState>()(
  devtools(
    (set, get) => ({
      // Initial state
      regions: [],
      selectedCluster: null,
      selectedRegion: null,
      clusterDateFilters: {},
      isProcessing: false,
      error: null,

      // Data actions
      setRegions: (regions) => set({ regions }, false, 'setRegions'),

      setSelectedCluster: (cluster, regionName) =>
        set(
          {
            selectedCluster: { cluster, regionName },
            selectedRegion: null,
          },
          false,
          'setSelectedCluster'
        ),

      setSelectedRegion: (region) =>
        set(
          {
            selectedRegion: region,
            selectedCluster: null,
          },
          false,
          'setSelectedRegion'
        ),

      clearSelection: () =>
        set(
          {
            selectedCluster: null,
            selectedRegion: null,
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

      // UI actions
      setIsProcessing: (processing: boolean) =>
        set({ isProcessing: processing }, false, 'setIsProcessing'),

      setError: (error: string | null) => set({ error }, false, 'setError'),
    }),
    {
      name: 'kcp-app-store', // Name for Redux DevTools
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
