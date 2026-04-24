import { useMemo } from 'react'
import { useRegions } from '@/stores/store'
import { getClusterArn } from '@/lib/clusterUtils'
import type { OSKClusterMetadata } from '@/types'

export interface TCOCluster {
  name: string
  key: string
  sourceType: 'msk' | 'osk'
  regionName: string
  metadata?: OSKClusterMetadata
}

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
            key: arn,
            sourceType: 'msk',
            regionName: region.name,
          })
        }
      })
    })
    return clusters
  }, [regions])
}
