/**
 * Application-wide constants
 */

/**
 * Tab IDs for metrics and costs views
 */
export const TAB_IDS = {
  CHART: 'chart',
  TABLE: 'table',
  JSON: 'json',
  CSV: 'csv',
} as const

/**
 * Top-level application tabs
 */
export const TOP_LEVEL_TABS = {
  EXPLORE: 'explore',
  TCO_INPUTS: 'tco-inputs',
  MIGRATION_ASSETS: 'migration-assets',
} as const

/**
 * Cluster report tab IDs
 */
export const CLUSTER_REPORT_TABS = {
  CLUSTER: 'cluster',
  METRICS: 'metrics',
  TOPICS: 'topics',
  CONNECTORS: 'connectors',
  ACLS: 'acls',
} as const

/**
 * Connector type tabs
 */
export const CONNECTOR_TABS = {
  MSK: 'msk',
  SELF_MANAGED: 'selfManaged',
} as const

/**
 * Wizard types for migration workflows
 */
export const WIZARD_TYPES = {
  TARGET_INFRA: 'target-infra',
  MIGRATION_INFRA: 'migration-infra',
  MIGRATION_SCRIPTS: 'migration-scripts',
} as const

/**
 * Cost types available in the application
 */
export const COST_TYPES = {
  UNBLENDED_COST: 'unblended_cost',
  BLENDED_COST: 'blended_cost',
  AMORTIZED_COST: 'amortized_cost',
  NET_AMORTIZED_COST: 'net_amortized_cost',
  NET_UNBLENDED_COST: 'net_unblended_cost',
  USAGE_QUANTITY: 'usage_quantity',
} as const

/**
 * Default tab selections
 */
export const DEFAULT_TABS = {
  METRICS: TAB_IDS.CHART,
  COSTS: TAB_IDS.CHART,
} as const

/**
 * API endpoint paths
 */
export const API_ENDPOINTS = {
  METRICS: '/metrics',
  COSTS: '/costs',
  UPLOAD_STATE: '/upload-state',
} as const

/**
 * Default values
 */
export const DEFAULTS = {
  PARTITIONS: '1000',
  REPLICATION_FACTOR: '3',
  REGION_FALLBACK: 'unknown',
} as const

/**
 * AWS Service Names
 */
export const AWS_SERVICES = {
  MSK: 'Amazon Managed Streaming for Apache Kafka',
} as const

/**
 * Request timeout (in milliseconds)
 */
export const REQUEST_TIMEOUT = 30000 // 30 seconds

/**
 * Bootstrap Broker Type Labels
 * Direct mapping from AWS state file keys to human-readable labels
 */
export const BOOTSTRAP_BROKER_LABELS: Record<string, string> = {
  // VPC Connectivity endpoints
  BootstrapBrokerStringVpcConnectivitySaslIam: 'VPC Connectivity SASL IAM',
  BootstrapBrokerStringVpcConnectivitySaslScram: 'VPC Connectivity SASL SCRAM',
  BootstrapBrokerStringVpcConnectivityTls: 'VPC Connectivity TLS',
  // Public endpoints
  BootstrapBrokerStringPublicSaslIam: 'Public SASL IAM',
  BootstrapBrokerStringPublicSaslScram: 'Public SASL SCRAM',
  BootstrapBrokerStringPublicTls: 'Public TLS',
  // Private endpoints
  BootstrapBrokerStringSaslIam: 'SASL IAM',
  BootstrapBrokerStringSaslScram: 'SASL SCRAM',
  BootstrapBrokerStringTls: 'TLS',
  // Plaintext
  BootstrapBrokerString: 'Plaintext',
}
