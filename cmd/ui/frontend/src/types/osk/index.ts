/**
 * Open Source Kafka (OSK) Type Definitions
 * Types for OSK clusters discovered via Kafka Admin API
 */

import type { KafkaAdminInfo, DiscoveredClient } from '@/types'

/**
 * OSK Cluster Metadata
 */
export interface OSKClusterMetadata {
  environment?: string
  location?: string
  kafka_version?: string
  labels?: Record<string, string>
  last_scanned?: string
}

/**
 * OSK Cluster (raw state structure)
 */
export interface OSKCluster {
  id: string
  bootstrap_servers: string[]
  kafka_admin_client_information: KafkaAdminInfo
  discovered_clients: DiscoveredClient[]
  metadata: OSKClusterMetadata
}

/**
 * Processed OSK Cluster (API response structure)
 */
export interface ProcessedOSKCluster {
  id: string
  bootstrap_servers: string[]
  kafka_admin_client_information: KafkaAdminInfo
  discovered_clients: DiscoveredClient[]
  metadata: OSKClusterMetadata
}

/**
 * Processed OSK Source (contains OSK clusters)
 */
export interface ProcessedOSKSource {
  clusters: ProcessedOSKCluster[]
}
