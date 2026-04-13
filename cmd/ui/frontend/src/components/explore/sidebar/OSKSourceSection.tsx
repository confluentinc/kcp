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
    <div className="space-y-1">
      {/* Section Label */}
      <div className="flex items-center justify-between px-2 py-1">
        <span className="text-xs font-bold text-muted-foreground uppercase tracking-wider">
          Open Source Kafka
        </span>
        <span className="text-[11px] text-muted-foreground bg-secondary rounded-full px-1.5 py-0.5">
          {clusters.length}
        </span>
      </div>

      {/* Clusters */}
      <div className="space-y-0.5">
        {clusters.map((cluster) => {
          const isSelected = selectedView === 'cluster' && selectedOSKClusterId === cluster.id

          return (
            <button
              key={cluster.id}
              onClick={() => selectOSKCluster(cluster.id)}
              className={`w-full text-left flex items-center px-2.5 py-2 text-sm rounded-md transition-all duration-150 ${
                isSelected
                  ? 'bg-accent/10 text-accent'
                  : 'text-muted-foreground hover:text-foreground hover:bg-secondary'
              }`}
            >
              <span className={`w-1.5 h-1.5 rounded-full mr-2.5 flex-shrink-0 ${isSelected ? 'bg-accent' : 'bg-muted-foreground/40'}`} />
              <span className="truncate">{cluster.id}</span>
            </button>
          )
        })}
      </div>
    </div>
  )
}
