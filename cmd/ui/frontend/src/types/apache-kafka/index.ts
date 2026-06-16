/**
 * Apache Kafka (Apache Kafka) Type Definitions
 * Types for Apache Kafka clusters discovered via Kafka Admin API
 */

import type { KafkaAdminInfo, DiscoveredClient } from '@/types'

/**
 * Apache Kafka Cluster Metadata
 */
export interface ApacheKafkaClusterMetadata {
  environment?: string
  location?: string
  kafka_version?: string
  labels?: Record<string, string>
  last_scanned?: string
}

/**
 * Apache Kafka Cluster (raw state structure)
 */
export interface ApacheKafkaCluster {
  id: string
  bootstrap_servers: string[]
  kafka_admin_client_information: KafkaAdminInfo
  discovered_clients: DiscoveredClient[]
  metadata: ApacheKafkaClusterMetadata
}

/**
 * Processed Apache Kafka Cluster (API response structure)
 */
export interface ProcessedApacheKafkaCluster {
  id: string
  bootstrap_servers: string[]
  kafka_admin_client_information: KafkaAdminInfo
  metrics?: {
    metadata?: {
      start_date?: string
      end_date?: string
      period?: number
    }
    results?: Array<{
      start: string
      end: string
      label: string
      value: number | null
    }>
    aggregates?: Record<string, { avg?: number; min?: number; max?: number }>
  }
  discovered_clients: DiscoveredClient[]
  metadata: ApacheKafkaClusterMetadata
}

/**
 * Processed Apache Kafka Source (contains Apache Kafka clusters)
 */
export interface ProcessedApacheKafkaSource {
  clusters: ProcessedApacheKafkaCluster[]
}
