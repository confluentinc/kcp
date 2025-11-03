import type { Cluster } from '@/types'

/**
 * Extracts cluster ARN from a cluster object
 * @param cluster - The cluster object
 * @returns The cluster ARN or undefined if not available
 */
export function getClusterArn(cluster: Cluster): string | undefined {
  return cluster.aws_client_information?.msk_cluster_config?.ClusterArn
}

/**
 * Decode a base64 string to plain text
 * @param base64String - The base64 encoded string
 * @returns The decoded string or 'Unable to decode' if decoding fails
 */
export function decodeBase64(base64String: string): string {
  try {
    return atob(base64String)
  } catch {
    return 'Unable to decode'
  }
}

/**
 * Calculate total storage across all broker nodes
 * @param volumeSize - Storage size per broker in GB
 * @param brokerNodes - Number of broker nodes
 * @returns Total storage in GB
 */
export function calculateTotalStorage(volumeSize: number, brokerNodes: number): number {
  return volumeSize * brokerNodes
}
