import { useState, useCallback } from 'react'
import { useRegions } from '@/stores/store'
import { getApacheKafkaClusterDataById } from '@/stores/store'
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

      if (cluster.sourceType === SOURCE_TYPES.APACHE_KAFKA) {
        const apacheKafkaCluster = getApacheKafkaClusterDataById(clusterKey)
        if (!apacheKafkaCluster) return

        setModalState({
          isOpen: true,
          cluster: {
            name: apacheKafkaCluster.id,
            key: apacheKafkaCluster.id,
            sourceType: SOURCE_TYPES.APACHE_KAFKA,
            metrics: apacheKafkaCluster.metrics,
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
