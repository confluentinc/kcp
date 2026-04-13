import type { Region } from '@/types'
import { useAppStore } from '@/stores/store'
import { getClusterArn } from '@/lib/clusterUtils'
import { LayoutDashboard, Globe, Server } from 'lucide-react'

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
      <h3 className="text-xs font-semibold text-muted-foreground uppercase tracking-wider px-2 border-l-2 border-accent ml-2">
        AWS MSK
      </h3>

      {/* Summary Button */}
      <button
        onClick={selectSummary}
        className={`w-full text-left flex items-center p-2.5 rounded-lg transition-all duration-150 ${
          isSummarySelected
            ? 'bg-accent/10 text-accent border-l-[3px] border-accent'
            : 'hover:bg-secondary text-foreground'
        }`}
      >
        <LayoutDashboard className={`w-4 h-4 mr-2.5 flex-shrink-0 ${isSummarySelected ? 'text-accent' : 'text-muted-foreground'}`} />
        <span className="text-sm font-medium">Summary</span>
      </button>

      {/* Regions List */}
      <div className="space-y-1">
        {regions.map((region) => {
          const isRegionSelected = selectedView === 'region' && selectedRegionName === region.name

          return (
            <div key={region.name} className="space-y-0.5">
              <button
                onClick={() => selectRegion(region.name)}
                className={`w-full text-left flex items-center p-2.5 rounded-lg transition-all duration-150 ${
                  isRegionSelected
                    ? 'bg-accent/10 text-accent border-l-[3px] border-accent'
                    : 'hover:bg-secondary text-foreground'
                }`}
              >
                <Globe className={`w-4 h-4 mr-2.5 flex-shrink-0 ${isRegionSelected ? 'text-accent' : 'text-muted-foreground'}`} />
                <span className="text-sm font-medium">{region.name}</span>
              </button>

              {/* Clusters under each region */}
              <div className="ml-4 border-l border-border pl-3 space-y-0.5">
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
                        onClick={() => clusterArn && selectCluster(region.name, clusterArn)}
                        className={`w-full text-left flex items-center px-2.5 py-1.5 text-sm rounded-md transition-all duration-150 ${
                          isSelected
                            ? 'bg-accent/10 text-accent border-l-[3px] border-accent -ml-px'
                            : 'text-muted-foreground hover:text-foreground hover:bg-secondary'
                        }`}
                      >
                        <Server className={`w-3.5 h-3.5 mr-2 flex-shrink-0 ${isSelected ? 'text-accent' : 'text-muted-foreground'}`} />
                        <span className="truncate">{cluster.name}</span>
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
