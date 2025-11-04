import { useState, useCallback } from 'react'
import { useRegions } from '@/stores/store'
import { findClusterInRegions, getClusterArn } from '@/lib/clusterUtils'
import { getMetricConfig } from '@/lib/tcoUtils'

interface TCOCluster {
  name: string
  regionName: string
  arn: string
  key: string
}

interface ModalCluster {
  name: string
  region: string
  arn: string
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

/**
 * Hook to manage TCO metrics modal state
 * Handles opening the modal with cluster metrics and preselected metric configuration
 *
 * @returns {Object} Modal state and control functions
 */
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

      const clusterObj = findClusterInRegions(regions, cluster.regionName, cluster.name)
      if (!clusterObj) return

      const metricConfig = getMetricConfig(metricType)

      const clusterArn = cluster.arn || getClusterArn(clusterObj)
      if (!clusterArn) {
        console.error(`Cluster "${clusterObj.name}" missing ARN`)
        return
      }

      setModalState({
        isOpen: true,
        cluster: {
          name: clusterObj.name,
          region: cluster.regionName,
          arn: clusterArn,
          metrics: clusterObj.metrics,
        },
        preselectedMetric: metricConfig.metric,
        workloadAssumption: metricConfig.workloadAssumption,
      })
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
