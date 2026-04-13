import type { OSKCluster } from '@/types'
import { useAppStore } from '@/stores/store'
import { Server } from 'lucide-react'

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
      <h3 className="text-xs font-semibold text-muted-foreground uppercase tracking-wider px-2 border-l-2 border-accent ml-2">
        Open Source Kafka
      </h3>

      {/* OSK Clusters - Flat List */}
      <div className="space-y-0.5">
        {clusters.map((cluster) => {
          const isSelected = selectedView === 'cluster' && selectedOSKClusterId === cluster.id

          return (
            <button
              key={cluster.id}
              onClick={() => selectOSKCluster(cluster.id)}
              className={`w-full text-left flex items-center px-2.5 py-2 text-sm rounded-lg transition-all duration-150 ${
                isSelected
                  ? 'bg-accent/10 text-accent border-l-[3px] border-accent'
                  : 'text-muted-foreground hover:text-foreground hover:bg-secondary'
              }`}
            >
              <Server className={`w-4 h-4 mr-2.5 flex-shrink-0 ${isSelected ? 'text-accent' : 'text-muted-foreground'}`} />
              <span className="truncate">{cluster.id}</span>
            </button>
          )
        })}
      </div>
    </div>
  )
}
