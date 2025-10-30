import type {
  MSKClusterConfig,
  MSKConnector,
  KafkaAdminInfo,
  MSKConfiguration,
} from './aws/msk'
import type { CostsApiResponse } from './api/costs'

// Shared types for the application
export interface Cluster {
  name: string
  metrics?: {
    metadata: {
      cluster_type: string
      follower_fetching: boolean
      tiered_storage: boolean
      instance_type: string
      broker_az_distribution: string
      kafka_version: string
      enhanced_monitoring: string
      start_date: string
      end_date: string
      period: number
    }
    results: Array<{
      start: string
      end: string
      label: string
      value: number | null
    }>
  }
  aws_client_information: {
    msk_cluster_config?: MSKClusterConfig
    connectors?: MSKConnector[]
    bootstrap_brokers?: {
      [key: string]: string | null
    }
  }
  kafka_admin_client_information: KafkaAdminInfo
  timestamp?: string
}

export interface Region {
  name: string
  configurations?: MSKConfiguration[]
  costs?: CostsApiResponse
  clusters?: Array<Cluster>
}

// Re-export AWS MSK types for convenience
export type {
  MSKClusterConfig,
  MSKProvisionedCluster,
  BrokerNodeGroupInfo,
  MSKConnector,
  KafkaACL,
  KafkaAdminInfo,
  Topic,
  TopicsInfo,
  SelfManagedConnector,
  MSKConfiguration,
  RegionData,
} from './aws/msk'

// Re-export constants types
export type {
  TabId,
  TopLevelTab,
  CostType,
  ClusterReportTab,
  ConnectorTab,
  WizardType,
} from './constants'
