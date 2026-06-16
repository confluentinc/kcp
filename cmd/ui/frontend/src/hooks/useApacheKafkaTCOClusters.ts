import { useMemo } from 'react'
import { useAppStore } from '@/stores/store'
import { getApacheKafkaSource } from '@/lib/sourceUtils'
import { SOURCE_TYPES } from '@/constants'
import type { TCOCluster } from './useTCOClusters'

export const useApacheKafkaTCOClusters = (): TCOCluster[] => {
  const sources = useAppStore((state) => state.kcpState?.sources)

  return useMemo(() => {
    if (!sources) return []
    const apacheKafkaSource = getApacheKafkaSource(sources)
    if (!apacheKafkaSource) return []

    return apacheKafkaSource.clusters.map((cluster) => ({
      name: cluster.id,
      key: cluster.id,
      sourceType: SOURCE_TYPES.APACHE_KAFKA,
      regionName: '',
      metadata: cluster.metadata,
    }))
  }, [sources])
}
