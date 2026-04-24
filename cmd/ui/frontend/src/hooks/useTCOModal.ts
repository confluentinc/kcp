import { useState, useCallback } from 'react'
import { useRegions } from '@/stores/store'
import { getOSKClusterDataById } from '@/stores/store'
import { findClusterInRegions, getClusterArn } from '@/lib/clusterUtils'
import { getMetricConfig } from '@/lib/tcoUtils'
import type { TCOCluster } from './useTCOClusters'

interface ModalCluster {
  name: string
  region: string
  arn: string
  sourceType: 'msk' | 'osk'
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
      metricType: 'avg-ingress' | 'peak-ingress' | 'avg-egress' | 'peak-egress' | 'partitions',
      sourceType: 'msk' | 'osk'
    ) => {
      const cluster = allClusters.find((c) => c.key === clusterKey)
      if (!cluster) return

      const metricConfig = getMetricConfig(metricType)

      if (sourceType === 'osk') {
        const oskCluster = getOSKClusterDataById(clusterKey)
        if (!oskCluster) return

        setModalState({
          isOpen: true,
          cluster: {
            name: oskCluster.id,
            region: '',
            arn: oskCluster.id,
            sourceType: 'osk',
            metrics: oskCluster.metrics,
          },
          preselectedMetric: metricConfig.metric,
          workloadAssumption: metricConfig.workloadAssumption,
        })
      } else {
        const clusterObj = findClusterInRegions(regions, cluster.regionName, cluster.name)
        if (!clusterObj) return

        const clusterArn = cluster.key || getClusterArn(clusterObj)
        if (!clusterArn) return

        setModalState({
          isOpen: true,
          cluster: {
            name: clusterObj.name,
            region: cluster.regionName,
            arn: clusterArn,
            sourceType: 'msk',
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
