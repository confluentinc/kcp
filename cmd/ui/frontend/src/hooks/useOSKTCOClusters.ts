import { useMemo } from 'react'
import { useAppStore } from '@/stores/store'
import { getOSKSource } from '@/lib/sourceUtils'
import type { TCOCluster } from './useTCOClusters'

export const useOSKTCOClusters = (): TCOCluster[] => {
  const kcpState = useAppStore((state) => state.kcpState)

  return useMemo(() => {
    if (!kcpState) return []
    const oskSource = getOSKSource(kcpState.sources)
    if (!oskSource) return []

    return oskSource.clusters.map((cluster) => ({
      name: cluster.id,
      key: cluster.id,
      sourceType: 'osk' as const,
      regionName: '',
      metadata: cluster.metadata,
    }))
  }, [kcpState])
}
