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
    <div className="space-y-1">
      {/* Section Label */}
      <div className="px-2 py-1">
        <span className="text-sm font-bold text-foreground uppercase tracking-wider" style={{ fontFamily: "'IBM Plex Sans', sans-serif" }}>
          Open Source Kafka
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
              className={`w-full text-left flex items-center px-2.5 py-2 text-sm rounded-md transition-all duration-150 group ${
                isSelected
                  ? 'bg-accent/10 text-accent'
                  : 'text-muted-foreground hover:text-accent hover:bg-secondary'
              }`}
            >
              <Server className={`w-3.5 h-3.5 mr-2 flex-shrink-0 ${isSelected ? 'text-accent' : 'text-muted-foreground/40 group-hover:text-accent'}`} />
              <span className="truncate">{cluster.id}</span>
            </button>
          )
        })}
      </div>
    </div>
  )
}
