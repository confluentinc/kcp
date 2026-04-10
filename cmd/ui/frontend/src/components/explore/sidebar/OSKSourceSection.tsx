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
