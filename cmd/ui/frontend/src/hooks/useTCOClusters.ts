import { useMemo } from 'react'
import { useRegions } from '@/stores/store'
import { getClusterArn } from '@/lib/clusterUtils'

interface TCOCluster {
  name: string
  regionName: string
  arn: string
  key: string
}

/**
 * Hook to flatten all clusters from all regions into a single array
 * Each cluster includes its region name and ARN for identification
 *
 * @returns {TCOCluster[]} Array of clusters with region and ARN information
 */
export const useTCOClusters = (): TCOCluster[] => {
  const regions = useRegions()

  return useMemo(() => {
    const clusters: TCOCluster[] = []
    regions.forEach((region) => {
      region.clusters?.forEach((cluster) => {
        const arn = getClusterArn(cluster)
        if (arn) {
          clusters.push({
            name: cluster.name,
            regionName: region.name,
            arn: arn,
            key: arn,
          })
        }
      })
    })
    return clusters
  }, [regions])
}
