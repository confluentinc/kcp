/**
 * AWS MSK (Managed Streaming for Apache Kafka) Type Definitions
 * Based on AWS SDK and actual usage in the codebase
 */

/**
 * EBS Storage Information
 */
export interface EbsStorageInfo {
  VolumeSize?: number
  ProvisionedThroughput?: {
    Enabled?: boolean
    VolumeThroughput?: number
  }
}

/**
 * Storage Information
 */
export interface StorageInfo {
  EbsStorageInfo?: EbsStorageInfo
}

/**
 * Connectivity Information
 */
export interface ConnectivityInfo {
  PublicAccess?: {
    Type?: string
  }
}

/**
 * Broker Node Group Information
 */
export interface BrokerNodeGroupInfo {
  InstanceType?: string
  ClientSubnets?: string[]
  SecurityGroups?: string[]
  BrokerAZDistribution?: string
  ZoneIds?: string[]
  StorageInfo?: StorageInfo
  ConnectivityInfo?: ConnectivityInfo
}

/**
 * Current Broker Software Information
 */
export interface CurrentBrokerSoftwareInfo {
  KafkaVersion?: string
  ConfigurationArn?: string | null
  ConfigurationRevision?: number | null
}

/**
 * Client Authentication Configuration
 */
export interface ClientAuthentication {
  Unauthenticated?: {
    Enabled?: boolean
  }
  Sasl?: {
    Iam?: {
      Enabled?: boolean
    }
    Scram?: {
      Enabled?: boolean
    }
  }
  Tls?: {
    Enabled?: boolean
    CertificateAuthorityArnList?: string[]
  }
}

/**
 * Encryption at Rest Configuration
 */
export interface EncryptionAtRest {
  DataVolumeKMSKeyId?: string
}

/**
 * Encryption in Transit Configuration
 */
export interface EncryptionInTransit {
  ClientBroker?: string
  InCluster?: boolean
}

/**
 * Encryption Information
 */
export interface EncryptionInfo {
  EncryptionAtRest?: EncryptionAtRest
  EncryptionInTransit?: EncryptionInTransit
}

/**
 * CloudWatch Logs Configuration
 */
export interface CloudWatchLogs {
  Enabled?: boolean
  LogGroup?: string
}

/**
 * Firehose Configuration
 */
export interface Firehose {
  Enabled?: boolean
  DeliveryStream?: string
}

/**
 * S3 Configuration
 */
export interface S3 {
  Enabled?: boolean
  Bucket?: string
  Prefix?: string
}

/**
 * Broker Logs Configuration
 */
export interface BrokerLogs {
  CloudWatchLogs?: CloudWatchLogs
  Firehose?: Firehose
  S3?: S3
}

/**
 * Logging Information
 */
export interface LoggingInfo {
  BrokerLogs?: BrokerLogs
}

/**
 * Provisioned Cluster Configuration
 */
export interface MSKProvisionedCluster {
  NumberOfBrokerNodes?: number
  BrokerNodeGroupInfo?: BrokerNodeGroupInfo
  CurrentBrokerSoftwareInfo?: CurrentBrokerSoftwareInfo
  ClientAuthentication?: ClientAuthentication
  EncryptionInfo?: EncryptionInfo
  EnhancedMonitoring?: string
  LoggingInfo?: LoggingInfo
}

/**
 * Cluster Networking Configuration
 */
export interface ClusterNetworking {
  vpc_id?: string
  subnet_ids?: string[]
  security_groups?: string[]
  subnets?: SubnetInfo[]
}

export interface SubnetInfo {
  subnet_msk_broker_id: number
  subnet_id: string
  availability_zone: string
  private_ip_address: string
  cidr_block: string
}

/**
 * Broker Node Information
 */
export interface BrokerNodeInfo {
  AttachedENIId?: string
  BrokerId?: number
  ClientSubnet?: string
  ClientVpcIpAddress?: string
  CurrentBrokerSoftwareInfo?: CurrentBrokerSoftwareInfo
  Endpoints?: string[]
}

/**
 * Controller Node Information
 */
export interface ControllerNodeInfo {
  Endpoints?: string[]
}

/**
 * Zookeeper Node Information (for legacy Zookeeper-based clusters)
 */
export interface ZookeeperNodeInfo {
  // Add fields if needed for Zookeeper nodes
}

/**
 * Node Information
 */
export interface NodeInfo {
  AddedToClusterTime?: string | null
  BrokerNodeInfo?: BrokerNodeInfo | null
  ControllerNodeInfo?: ControllerNodeInfo | null
  InstanceType?: string | null
  NodeARN?: string | null
  NodeType?: 'BROKER' | 'CONTROLLER' | 'ZOOKEEPER'
  ZookeeperNodeInfo?: ZookeeperNodeInfo | null
}

/**
 * Nodes Configuration (array of NodeInfo)
 */
export type Nodes = NodeInfo[]

/**
 * MSK Cluster Configuration (from AWS DescribeCluster response)
 */
export interface MSKClusterConfig {
  ClusterArn?: string
  ClusterName?: string
  ClusterType?: string
  CreationTime?: string
  CurrentVersion?: string
  Provisioned?: MSKProvisionedCluster
  State?: string
  [key: string]: unknown
}

/**
 * MSK Connector (MSK Connect)
 */
export interface MSKConnector {
  connector_arn: string
  connector_name: string
  connector_state: string
  creation_time: string
  kafka_cluster: {
    BootstrapServers: string
    Vpc: {
      SecurityGroups: string[]
      Subnets: string[]
    }
  }
  kafka_cluster_client_authentication: {
    AuthenticationType: string
  }
  capacity: {
    AutoScaling?: {
      MaxWorkerCount: number
      McuCount: number
      MinWorkerCount: number
      ScaleInPolicy: { CpuUtilizationPercentage: number }
      ScaleOutPolicy: { CpuUtilizationPercentage: number }
    }
    ProvisionedCapacity?: {
      WorkerCount?: number
      McuCount?: number
    }
  }
  plugins: Array<{
    CustomPlugin: {
      CustomPluginArn: string
      Revision: number
    }
  }>
  connector_configuration: Record<string, string>
}

/**
 * Kafka ACL Entry
 */
export interface KafkaACL {
  ResourceType: string
  ResourceName: string
  ResourcePatternType: string
  Principal: string
  Host: string
  Operation: string
  PermissionType: string
}

/**
 * Topic Summary
 */
export interface TopicSummary {
  topics: number
  total_partitions: number
  internal_topics: number
  compact_topics: number
}

/**
 * Topic Configuration
 */
export interface TopicConfiguration {
  [key: string]: string
}

/**
 * Topic Detail
 */
export interface Topic {
  name: string
  partitions: number
  replication_factor: number
  configurations: TopicConfiguration
}

/**
 * Topics Information
 */
export interface TopicsInfo {
  summary: TopicSummary
  details: Topic[]
}

/**
 * Self-Managed Connector
 */
export interface SelfManagedConnector {
  name: string
  config: Record<string, string>
  state: string
  connect_host: string
}

/**
 * Self-Managed Connectors
 */
export interface SelfManagedConnectors {
  connectors: SelfManagedConnector[]
}

/**
 * Kafka Admin Client Information
 */
export interface KafkaAdminInfo {
  topics?: TopicsInfo
  acls?: KafkaACL[]
  self_managed_connectors?: SelfManagedConnectors
  [key: string]: unknown
}

/**
 * MSK Configuration (from DescribeConfiguration response)
 */
export interface MSKConfiguration {
  Arn: string
  CreationTime: string
  Description?: string
  KafkaVersions?: string[]
  LatestRevision?: {
    Revision?: number
    CreationTime?: string
  }
  Name?: string
  Revision?: number
  ServerProperties?: string
  State?: string
}

/**
 * Region Data (contains configurations and other region-specific data)
 */
export interface RegionData {
  configurations?: MSKConfiguration[]
  [key: string]: unknown
}
