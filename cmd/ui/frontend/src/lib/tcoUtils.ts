import { DEFAULTS, METRIC_TYPE_MAP } from '@/constants'
import { findClusterInRegions } from './clusterUtils'
import type { Region } from '@/types'

interface TCOCluster {
  name: string
  regionName: string
  arn: string
  key: string
}

interface TCOWorkloadData {
  [clusterKey: string]: {
    avgIngressThroughput?: string
    peakIngressThroughput?: string
    avgEgressThroughput?: string
    peakEgressThroughput?: string
    retentionDays?: string
    partitions?: string
    replicationFactor?: string
    localRetentionHours?: string
  }
}

/**
 * Generates CSV content from TCO workload data
 * @param allClusters - Array of all clusters
 * @param tcoWorkloadData - TCO workload data by cluster key
 * @param regions - Array of regions (for metadata lookup)
 * @returns CSV content as string
 */
export const generateTCOCSV = (
  allClusters: TCOCluster[],
  tcoWorkloadData: TCOWorkloadData,
  regions: Region[]
): string => {
  if (allClusters.length === 0) {
    return 'No clusters available. Please load a KCP state file first.'
  }

  // Create header row (just cluster names)
  const headers = allClusters.map((cluster) => cluster.name)

  // Create data rows
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
    allClusters.map((cluster) => {
      const clusterObj = findClusterInRegions(regions, cluster.regionName, cluster.name)
      const followerFetching = clusterObj?.metrics?.metadata?.follower_fetching
      return followerFetching !== undefined ? followerFetching.toString().toUpperCase() : 'N/A'
    }),
    allClusters.map((cluster) => {
      const clusterObj = findClusterInRegions(regions, cluster.regionName, cluster.name)
      const tieredStorage = clusterObj?.metrics?.metadata?.tiered_storage
      return tieredStorage !== undefined ? tieredStorage.toString().toUpperCase() : 'N/A'
    }),
    allClusters.map((cluster) => tcoWorkloadData[cluster.key]?.localRetentionHours || ''),
  ]

  // Combine headers and rows
  const csvContent = [headers, ...rows].map((row) => row.join(',')).join('\n')

  return csvContent
}

/**
 * Get metric configuration from metric type
 * @param metricType - The metric type identifier
 * @returns Metric configuration with metric name and workload assumption label
 */
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
