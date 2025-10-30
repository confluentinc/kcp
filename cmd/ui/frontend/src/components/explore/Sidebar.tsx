import type { Cluster, Region } from '@/types'
import { useAppStore } from '@/stores/store'

export default function Sidebar() {
  const regions = useAppStore((state) => state.regions)
  const selectedCluster = useAppStore((state) => state.selectedCluster)
  const selectedRegion = useAppStore((state) => state.selectedRegion)
  const selectedSummary = useAppStore((state) => state.selectedSummary)
  const selectedSchemaRegistries = useAppStore((state) => state.selectedSchemaRegistries)

  const setSelectedCluster = useAppStore((state) => state.setSelectedCluster)
  const setSelectedRegion = useAppStore((state) => state.setSelectedRegion)
  const setSelectedSummary = useAppStore((state) => state.setSelectedSummary)
  const setSelectedSchemaRegistries = useAppStore((state) => state.setSelectedSchemaRegistries)

  const handleClusterSelect = (cluster: Cluster, regionName: string) => {
    setSelectedCluster(cluster, regionName)
  }

  const handleRegionSelect = (region: Region) => {
    setSelectedRegion(region)
  }

  const handleSummarySelect = () => {
    setSelectedSummary()
  }

  const handleSchemaRegistriesSelect = () => {
    setSelectedSchemaRegistries()
  }
  return (
    <div className="h-full flex flex-col">
      <div className="p-4 border-b border-gray-200 dark:border-border">
        <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">Navigation</h2>
        <p className="text-sm text-gray-600 dark:text-gray-400 mt-1">
          Explore regions and clusters
        </p>
      </div>

      <div className="flex-1 overflow-y-auto p-4">
        {regions.length > 0 ? (
          <div className="space-y-3">
            {/* Summary Section */}
            <div className="space-y-2">
              <button
                onClick={handleSummarySelect}
                className={`w-full text-left flex items-center justify-between p-3 rounded-lg transition-colors ${
                  selectedSummary
                    ? 'bg-blue-100 dark:bg-accent/20 border border-blue-200 dark:border-accent'
                    : 'hover:bg-gray-100 dark:hover:bg-gray-600'
                }`}
              >
                <div className="flex items-center space-x-2 min-w-0 flex-1">
                  <div
                    className={`w-2 h-2 rounded-full flex-shrink-0 ${
                      selectedSummary ? 'bg-blue-600' : 'bg-gray-500'
                    }`}
                  ></div>
                  <h4
                    className={`text-sm font-medium whitespace-nowrap ${
                      selectedSummary
                        ? 'text-blue-900 dark:text-accent'
                        : 'text-gray-800 dark:text-gray-200'
                    }`}
                  >
                    Summary
                  </h4>
                </div>
              </button>
            </div>

            {/* Regions under Summary */}
            <div className="ml-4 space-y-2">
              {regions.map((region) => {
                const isRegionSelected = selectedRegion?.name === region.name

                return (
                  <div
                    key={region.name}
                    className="space-y-1"
                  >
                    <button
                      onClick={() => handleRegionSelect(region)}
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
                        ></div>
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

                    {/* Clusters under each region - only show provisioned clusters */}
                    <div className="ml-4 space-y-1">
                      {(region.clusters || [])
                        .filter(
                          (cluster) =>
                            cluster.aws_client_information?.msk_cluster_config?.Provisioned
                        )
                        .map((cluster) => {
                          const isSelected =
                            selectedCluster?.cluster.name === cluster.name &&
                            selectedCluster?.regionName === region.name
                          return (
                            <button
                              key={cluster.name}
                              onClick={() => handleClusterSelect(cluster, region.name)}
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
                                ></div>
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
        ) : (
          <div className="bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-border rounded-lg p-4">
            <p className="text-sm text-yellow-800 dark:text-yellow-200">
              No regions available. Please upload a KCP state file to explore your infrastructure.
            </p>
          </div>
        )}

        {/* Schema Registries Section */}
        <div className="space-y-2 mt-6 pt-4 border-t border-gray-200 dark:border-border">
          <button
            onClick={handleSchemaRegistriesSelect}
            className={`w-full text-left flex items-center justify-between p-3 rounded-lg transition-colors ${
              selectedSchemaRegistries
                ? 'bg-blue-100 dark:bg-accent/20 border border-blue-200 dark:border-accent'
                : 'hover:bg-gray-100 dark:hover:bg-gray-600'
            }`}
          >
            <div className="flex items-center space-x-2 min-w-0 flex-1">
              <div
                className={`w-2 h-2 rounded-full flex-shrink-0 ${
                  selectedSchemaRegistries ? 'bg-blue-600' : 'bg-gray-500'
                }`}
              ></div>
              <h4
                className={`text-base font-medium whitespace-nowrap ${
                  selectedSchemaRegistries
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

