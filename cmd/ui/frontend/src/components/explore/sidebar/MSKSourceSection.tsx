import type { Region } from '@/types'
import { useAppStore } from '@/stores/store'
import { getClusterArn } from '@/lib/clusterUtils'
import { BarChart3, Globe, Server } from 'lucide-react'

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
    <div className="space-y-1">
      {/* Section Label */}
      <div className="px-2 py-1">
        <span className="text-sm font-bold text-foreground uppercase tracking-wider" style={{ fontFamily: "'IBM Plex Sans', sans-serif" }}>
          AWS MSK
        </span>
      </div>

      {/* All Regions overview */}
      <button
        onClick={selectSummary}
        className={`w-full text-left flex items-center px-2.5 py-2 rounded-md transition-all duration-150 group ${
          isSummarySelected
            ? 'bg-accent/10 text-accent'
            : 'text-foreground hover:text-accent hover:bg-secondary'
        }`}
      >
        <BarChart3 className={`w-4 h-4 mr-2.5 flex-shrink-0 ${isSummarySelected ? 'text-accent' : 'text-muted-foreground group-hover:text-accent'}`} />
        <span className="text-sm font-medium">All Regions</span>
      </button>

      {/* Regions - indented under All Regions */}
      <div className="ml-4 space-y-0.5">
        {regions.map((region) => {
          const isRegionSelected = selectedView === 'region' && selectedRegionName === region.name
          const provisionedClusters = (region.clusters || []).filter(
            (cluster) => cluster.aws_client_information?.msk_cluster_config?.Provisioned
          )

          return (
            <div key={region.name}>
              {/* Region header */}
              <button
                onClick={() => selectRegion(region.name)}
                className={`w-full text-left flex items-center justify-between px-2.5 py-2 rounded-md transition-all duration-150 group ${
                  isRegionSelected
                    ? 'bg-accent/10 text-accent'
                    : 'text-foreground hover:text-accent hover:bg-secondary'
                }`}
              >
                <div className="flex items-center min-w-0">
                  <Globe className={`w-3.5 h-3.5 mr-2 flex-shrink-0 ${isRegionSelected ? 'text-accent' : 'text-muted-foreground group-hover:text-accent'}`} />
                  <span className="text-sm font-medium truncate">{region.name}</span>
                </div>
              </button>

              {/* Clusters */}
              <div className="ml-4 mt-0.5 space-y-0.5">
                {provisionedClusters.map((cluster) => {
                  const clusterArn = getClusterArn(cluster)
                  const isSelected =
                    selectedView === 'cluster' && selectedClusterArn === clusterArn

                  return (
                    <button
                      key={cluster.name}
                      onClick={() => clusterArn && selectCluster(region.name, clusterArn)}
                      className={`w-full text-left flex items-center px-2.5 py-1.5 text-sm rounded-md transition-all duration-150 group ${
                        isSelected
                          ? 'bg-accent/10 text-accent'
                          : 'text-muted-foreground hover:text-accent hover:bg-secondary'
                      }`}
                    >
                      <Server className={`w-3.5 h-3.5 mr-2 flex-shrink-0 ${isSelected ? 'text-accent' : 'text-muted-foreground/40 group-hover:text-accent'}`} />
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
