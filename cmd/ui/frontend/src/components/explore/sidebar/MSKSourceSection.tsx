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
