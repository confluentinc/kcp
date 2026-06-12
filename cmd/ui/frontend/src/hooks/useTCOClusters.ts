import { useMemo } from 'react'
import { useRegions } from '@/stores/store'
import { getClusterArn } from '@/lib/clusterUtils'
import { SOURCE_TYPES } from '@/constants'
import type { SourceType, OSKClusterMetadata } from '@/types'

export interface TCOCluster {
  name: string
  key: string
  sourceType: SourceType
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
            sourceType: SOURCE_TYPES.MSK,
            regionName: region.name,
          })
        }
      })
    })
    return clusters
  }, [regions])
}
