import { DEFAULTS, METRIC_TYPE_MAP, SOURCE_TYPES } from '@/constants'
import { findClusterInRegions } from './clusterUtils'
import type { Region } from '@/types'
import type { WorkloadData } from '@/stores/store'
import type { TCOCluster } from '@/hooks/useTCOClusters'

export const generateTCOCSV = (
  allClusters: TCOCluster[],
  tcoWorkloadData: WorkloadData,
  regions: Region[]
): string => {
  if (allClusters.length === 0) {
    return 'No clusters available. Please load a KCP state file first.'
  }

  const headers = allClusters.map((cluster) => cluster.name)

  const getReadOnlyValue = (cluster: TCOCluster, field: 'follower_fetching' | 'tiered_storage'): string => {
    if (cluster.sourceType === SOURCE_TYPES.OSK) {
      return 'N/A'
    }
    const clusterObj = findClusterInRegions(regions, cluster.regionName, cluster.name)
    const value = clusterObj?.metrics?.metadata?.[field]
    return value !== undefined ? value.toString().toUpperCase() : 'N/A'
  }

  const rows = [
    allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.avgIngressThroughput || ''),
    allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.peakIngressThroughput || ''),
    allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.avgEgressThroughput || ''),
    allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.peakEgressThroughput || ''),
    allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.retentionDays || ''),
    allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.partitions || DEFAULTS.PARTITIONS),
    allClusters.map(
      (cluster) => tcoWorkloadData[cluster.key]?.replicationFactor || DEFAULTS.REPLICATION_FACTOR
    ),
    allClusters.map((cluster) => getReadOnlyValue(cluster, 'follower_fetching')),
    allClusters.map((cluster) => getReadOnlyValue(cluster, 'tiered_storage')),
    allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.localRetentionHours || ''),
  ]

  const escapeCSV = (val: string) =>
    val.includes(',') || val.includes('"') || val.includes('\n')
      ? `"${val.replace(/"/g, '""')}"`
      : val

  return [headers, ...rows].map((row) => row.map(escapeCSV).join(',')).join('\n')
}

export const getMetricConfig = (
  metricType: 'avg-ingress' | 'peak-ingress' | 'avg-egress' | 'peak-egress' | 'partitions'
) => {
  return (
    METRIC_TYPE_MAP[metricType] || {
      metric: 'BytesInPerSec',
      workloadAssumption: 'Ingress Throughput',
    }
  )
}
