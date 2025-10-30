/**
 * Utility functions for metrics processing and conversion
 */

/**
 * Convert bytes/sec to MB/s
 */
export function convertBytesToMB(bytesPerSec: number): string {
  const mbPerSec = bytesPerSec / (1024 * 1024)
  return mbPerSec.toFixed(5)
}

/**
 * Map metric names to workload assumption names
 */
export function getWorkloadAssumptionName(metricName: string): string {
  switch (metricName) {
    case 'BytesInPerSec':
      return 'Ingress Throughput'
    case 'BytesOutPerSec':
      return 'Egress Throughput'
    case 'GlobalPartitionCount':
      return 'Partitions'
    default:
      return 'Metric'
  }
}

/**
 * Valid TCO workload field names
 */
export type TCOWorkloadField =
  | 'avgIngressThroughput'
  | 'peakIngressThroughput'
  | 'avgEgressThroughput'
  | 'peakEgressThroughput'
  | 'retentionDays'
  | 'partitions'
  | 'replicationFactor'
  | 'localRetentionHours'

/**
 * Map modal workload assumption to TCO field
 */
export function getTCOFieldFromWorkloadAssumption(workloadAssumption: string): TCOWorkloadField {
  switch (workloadAssumption) {
    case 'Avg Ingress Throughput (MB/s)':
      return 'avgIngressThroughput'
    case 'Peak Ingress Throughput (MB/s)':
      return 'peakIngressThroughput'
    case 'Avg Egress Throughput (MB/s)':
      return 'avgEgressThroughput'
    case 'Peak Egress Throughput (MB/s)':
      return 'peakEgressThroughput'
    case 'Partitions':
      return 'partitions'
    default:
      return 'avgIngressThroughput' // fallback
  }
}

