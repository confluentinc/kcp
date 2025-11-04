import type { Cluster, Region } from '@/types'

/**
 * Extracts cluster ARN from a cluster object
 * @param cluster - The cluster object
 * @returns The cluster ARN or undefined if not available
 */
export const getClusterArn = (cluster: Cluster): string | undefined => {
  return cluster.aws_client_information?.msk_cluster_config?.ClusterArn
}

/**
 * Finds a cluster object from the regions array by region name and cluster name
 * @param regions - Array of regions to search
 * @param regionName - Name of the region
 * @param clusterName - Name of the cluster
 * @returns The cluster object or undefined if not found
 */
export const findClusterInRegions = (
  regions: Region[],
  regionName: string,
  clusterName: string
): Cluster | undefined => {
  const region = regions.find((r) => r.name === regionName)
  return region?.clusters?.find((c) => c.name === clusterName)
}

/**
 * Decode a base64 string to plain text
 * @param base64String - The base64 encoded string
 * @returns The decoded string or 'Unable to decode' if decoding fails
 */
export const decodeBase64 = (base64String: string): string => {
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
export const calculateTotalStorage = (volumeSize: number, brokerNodes: number): number => {
  return volumeSize * brokerNodes
}
