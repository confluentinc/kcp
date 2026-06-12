import { useMemo } from 'react'
import { useAppStore } from '@/stores/store'
import { getOSKSource } from '@/lib/sourceUtils'
import { SOURCE_TYPES } from '@/constants'
import type { TCOCluster } from './useTCOClusters'

export const useOSKTCOClusters = (): TCOCluster[] => {
  const sources = useAppStore((state) => state.kcpState?.sources)

  return useMemo(() => {
    if (!sources) return []
    const oskSource = getOSKSource(sources)
    if (!oskSource) return []

    return oskSource.clusters.map((cluster) => ({
      name: cluster.id,
      key: cluster.id,
      sourceType: SOURCE_TYPES.OSK,
      regionName: '',
      metadata: cluster.metadata,
    }))
  }, [sources])
}
