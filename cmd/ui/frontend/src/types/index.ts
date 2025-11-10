import type { MSKClusterConfig, MSKConnector, KafkaAdminInfo, MSKConfiguration, ClusterNetworking, Nodes } from './aws/msk'
import type { CostsApiResponse } from './api/costs'
import type { ApiMetadata } from './api/common'

// Shared types for the application
export interface Cluster {
  name: string
  arn?: string
  region: string
  metrics?: {
    metadata: ApiMetadata
    results: Array<{
      start: string
      end: string
      label: string
      value: number | null
    }>
  }
  aws_client_information: {
    cluster_networking?: ClusterNetworking
    msk_cluster_config?: MSKClusterConfig
    nodes?: Nodes
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
