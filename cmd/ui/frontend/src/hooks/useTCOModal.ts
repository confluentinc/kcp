import { useState, useCallback } from 'react'
import { useRegions } from '@/stores/store'
import { getOSKClusterDataById } from '@/stores/store'
import { findClusterInRegions } from '@/lib/clusterUtils'
import { SOURCE_TYPES } from '@/constants'
import { getMetricConfig } from '@/lib/tcoUtils'
import type { SourceType } from '@/types'
import type { TCOCluster } from './useTCOClusters'

interface ModalCluster {
  name: string
  key: string
  region?: string
  sourceType: SourceType
  metrics?: {
    metadata?: {
      start_date?: string
      end_date?: string
    }
  }
}

interface ModalState {
  isOpen: boolean
  cluster: ModalCluster | null
  preselectedMetric: string | null
  workloadAssumption: string | null
}

export const useTCOModal = (allClusters: TCOCluster[]) => {
  const regions = useRegions()

  const [modalState, setModalState] = useState<ModalState>({
    isOpen: false,
    cluster: null,
    preselectedMetric: null,
    workloadAssumption: null,
  })

  const openModal = useCallback(
    (
      clusterKey: string,
      metricType: 'avg-ingress' | 'peak-ingress' | 'avg-egress' | 'peak-egress' | 'partitions'
    ) => {
      const cluster = allClusters.find((c) => c.key === clusterKey)
      if (!cluster) return

      const metricConfig = getMetricConfig(metricType)

      if (cluster.sourceType === SOURCE_TYPES.OSK) {
        const oskCluster = getOSKClusterDataById(clusterKey)
        if (!oskCluster) return

        setModalState({
          isOpen: true,
          cluster: {
            name: oskCluster.id,
            key: oskCluster.id,
            sourceType: SOURCE_TYPES.OSK,
            metrics: oskCluster.metrics,
          },
          preselectedMetric: metricConfig.metric,
          workloadAssumption: metricConfig.workloadAssumption,
        })
      } else {
        const clusterObj = findClusterInRegions(regions, cluster.regionName, cluster.name)
        if (!clusterObj) return

        setModalState({
          isOpen: true,
          cluster: {
            name: clusterObj.name,
            key: cluster.key,
            region: cluster.regionName,
            sourceType: SOURCE_TYPES.MSK,
            metrics: clusterObj.metrics,
          },
          preselectedMetric: metricConfig.metric,
          workloadAssumption: metricConfig.workloadAssumption,
        })
      }
    },
    [allClusters, regions]
  )

  const closeModal = useCallback(() => {
    setModalState({
      isOpen: false,
      cluster: null,
      preselectedMetric: null,
      workloadAssumption: null,
    })
  }, [])

  return {
    modalState,
    openModal,
    closeModal,
  }
}
