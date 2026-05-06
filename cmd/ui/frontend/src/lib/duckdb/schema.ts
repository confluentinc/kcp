/**
 * schema.ts — Workstream A: SQL Workbench schema contracts
 *
 * Single source of truth for:
 *   - Row-type interfaces (one per DuckDB table)
 *   - NormalizedTables payload shape
 *   - Column / table metadata (TABLE_METADATA)
 *   - CREATE TABLE SQL derived from metadata (CREATE_STATEMENTS)
 *   - Query result + saved-query interfaces
 *
 * No implementation logic lives here — pure types and constants.
 */

// ---------------------------------------------------------------------------
// Internal helper: derive a CREATE TABLE statement from a TableMeta entry.
// Not exported — CREATE_STATEMENTS is the public surface.
// ---------------------------------------------------------------------------

function buildCreateStatement(table: TableMeta): string {
  const cols = table.columns
    .map((c) => {
      const notNull = c.nullable ? '' : ' NOT NULL'
      return `  ${c.name} ${c.type}${notNull}`
    })
    .join(',\n')
  return `CREATE TABLE ${table.name} (\n${cols}\n);`
}

// ---------------------------------------------------------------------------
// Row-type interfaces (one per table, field names match DuckDB column names)
//
// Nullability rules:
//   NOT NULL in DuckDB → field type has no "| null"
//   nullable in DuckDB → field type is "T | null"
//
// TIMESTAMP / DATE columns are returned as ISO-8601 strings by the query
// layer before being handed to consumers.
// ---------------------------------------------------------------------------

export interface ClusterRow {
  cluster_key: string
  source_type: string
  name: string
  region: string | null
  kafka_version: string | null
  cluster_type: string | null
  broker_type: string | null
  instance_type: string | null
  number_of_brokers: number | null
  enhanced_monitoring: string | null
  tiered_storage: boolean | null
  follower_fetching: boolean | null
  environment: string | null
  location: string | null
  last_scanned: string | null // ISO 8601 timestamp
}

export interface BrokerRow {
  cluster_key: string
  broker_id: number | null
  bootstrap_host: string | null
  availability_zone: string | null
  private_ip: string | null
  subnet_id: string | null
  cidr_block: string | null
}

export interface BootstrapEndpointRow {
  cluster_key: string
  auth_type: string
  visibility: string | null
  endpoints: string | null
}

export interface TopicRow {
  cluster_key: string
  source_type: string
  name: string
  is_internal: boolean
  partitions: number
  replication_factor: number
  cleanup_policy: string | null
  retention_ms: number | null
  retention_bytes: number | null
  min_insync_replicas: number | null
  remote_storage_enable: boolean | null
  compression_type: string | null
}

export interface TopicConfigRow {
  cluster_key: string
  topic_name: string
  config_key: string
  config_value: string | null
}

export interface AclRow {
  cluster_key: string
  source_type: string
  resource_type: string
  resource_name: string
  pattern_type: string
  principal: string
  host: string
  operation: string
  permission_type: string
}

export interface ConnectorRow {
  cluster_key: string
  source_type: string
  connector_type: string
  name: string
  arn: string | null
  state: string | null
  connector_class: string | null
  tasks_max: number | null
  topics_pattern: string | null
  worker_count: number | null
  mcu_count: number | null
  autoscaling_enabled: boolean | null
  creation_time: string | null // ISO 8601 timestamp
  connect_host: string | null
}

export interface ConnectorConfigRow {
  cluster_key: string
  connector_name: string
  config_key: string
  config_value: string | null
}

export interface DiscoveredClientRow {
  cluster_key: string
  source_type: string
  kafka_client_id: string | null
  role: string | null
  topic: string | null
  auth: string | null
  principal: string | null
  observed_at: string | null // ISO 8601 timestamp
}

export interface MetricsTimeseriesRow {
  cluster_key: string
  source_type: string
  metric_name: string
  window_start: string // ISO 8601 timestamp, NOT NULL
  window_end: string // ISO 8601 timestamp, NOT NULL
  value: number | null
}

export interface MetricAggregateRow {
  cluster_key: string
  source_type: string
  metric_name: string
  avg_value: number | null
  min_value: number | null
  max_value: number | null
}

export interface RegionCostRow {
  region: string
  start_date: string // ISO 8601 date, NOT NULL
  end_date: string // ISO 8601 date, NOT NULL
  service: string
  usage_type: string | null
  unblended_cost: number | null
  blended_cost: number | null
  amortized_cost: number | null
  net_amortized_cost: number | null
  net_unblended_cost: number | null
}

export interface SchemaRegistryRow {
  registry_key: string
  registry_type: string
  url: string | null
  registry_arn: string | null
  registry_name: string | null
  region: string | null
  default_compatibility: string | null
}

export interface SchemaSubjectRow {
  registry_key: string
  subject_name: string
  schema_type: string | null
  compatibility: string | null
  version_count: number | null
  latest_version: number | null
  latest_schema_id: number | null
}

export interface SchemaVersionRow {
  registry_key: string
  subject_name: string
  version: number
  schema_id: number | null
  schema_type: string | null
  schema_definition: string | null
  status: string | null
  created_at: string | null // ISO 8601 timestamp
}

export interface RegionRow {
  name: string
  cluster_count: number
}

// ---------------------------------------------------------------------------
// NormalizedTables — payload the normalizer produces / ingestor consumes
// ---------------------------------------------------------------------------

export interface NormalizedTables {
  clusters: ClusterRow[]
  brokers: BrokerRow[]
  bootstrap_endpoints: BootstrapEndpointRow[]
  topics: TopicRow[]
  topic_configs: TopicConfigRow[]
  acls: AclRow[]
  connectors: ConnectorRow[]
  connector_configs: ConnectorConfigRow[]
  discovered_clients: DiscoveredClientRow[]
  metrics_timeseries: MetricsTimeseriesRow[]
  metric_aggregates: MetricAggregateRow[]
  region_costs: RegionCostRow[]
  schema_registries: SchemaRegistryRow[]
  schema_subjects: SchemaSubjectRow[]
  schema_versions: SchemaVersionRow[]
  regions: RegionRow[]
}

// ---------------------------------------------------------------------------
// Column / table metadata — drives CREATE_STATEMENTS and schema browser
// ---------------------------------------------------------------------------

export interface ColumnMeta {
  name: string
  type: string // DuckDB type — e.g. 'TEXT', 'INTEGER', 'DOUBLE', 'BOOLEAN', 'TIMESTAMP'
  nullable: boolean
  description?: string // optional human label used in tooltips
}

export interface TableMeta {
  name: string
  description: string
  columns: ColumnMeta[]
}

export const TABLE_METADATA: TableMeta[] = [
  // ------------------------------------------------------------------
  {
    name: 'clusters',
    description: 'One row per Kafka cluster discovered from MSK or OSK sources.',
    columns: [
      {
        name: 'cluster_key',
        type: 'TEXT',
        nullable: false,
        description: 'Synthetic primary key — ARN for MSK clusters, id for OSK clusters.',
      },
      {
        name: 'source_type',
        type: 'TEXT',
        nullable: false,
        description: "Source system: 'msk' (AWS Managed Streaming) or 'osk' (Open Source Kafka).",
      },
      { name: 'name', type: 'TEXT', nullable: false, description: 'Human-readable cluster name.' },
      {
        name: 'region',
        type: 'TEXT',
        nullable: true,
        description: 'AWS region for MSK clusters; NULL for OSK.',
      },
      { name: 'kafka_version', type: 'TEXT', nullable: true, description: 'Kafka software version string.' },
      {
        name: 'cluster_type',
        type: 'TEXT',
        nullable: true,
        description: "Cluster tier: PROVISIONED, SERVERLESS, or OSS.",
      },
      {
        name: 'broker_type',
        type: 'TEXT',
        nullable: true,
        description: "MSK broker tier: 'express' or 'standard'.",
      },
      { name: 'instance_type', type: 'TEXT', nullable: true, description: 'EC2 instance type for MSK brokers.' },
      {
        name: 'number_of_brokers',
        type: 'INTEGER',
        nullable: true,
        description: 'Total broker count in the cluster.',
      },
      {
        name: 'enhanced_monitoring',
        type: 'TEXT',
        nullable: true,
        description: 'MSK enhanced monitoring level (DEFAULT, PER_BROKER, PER_TOPIC_PER_BROKER, etc.).',
      },
      {
        name: 'tiered_storage',
        type: 'BOOLEAN',
        nullable: true,
        description: 'True if MSK tiered storage is enabled.',
      },
      {
        name: 'follower_fetching',
        type: 'BOOLEAN',
        nullable: true,
        description: 'True if MSK follower fetching is enabled.',
      },
      {
        name: 'environment',
        type: 'TEXT',
        nullable: true,
        description: 'OSK metadata.environment label (e.g. production).',
      },
      {
        name: 'location',
        type: 'TEXT',
        nullable: true,
        description: 'OSK metadata.location label (e.g. datacenter-1).',
      },
      {
        name: 'last_scanned',
        type: 'TEXT',
        nullable: true,
        description: 'ISO 8601 timestamp of the most recent scan.',
      },
    ],
  },

  // ------------------------------------------------------------------
  {
    name: 'brokers',
    description: 'One row per broker node; MSK rows come from subnet data, OSK rows from bootstrap servers.',
    columns: [
      {
        name: 'cluster_key',
        type: 'TEXT',
        nullable: false,
        description: 'Foreign key to clusters.cluster_key.',
      },
      {
        name: 'broker_id',
        type: 'INTEGER',
        nullable: true,
        description: 'MSK broker ID integer; NULL for OSK.',
      },
      {
        name: 'bootstrap_host',
        type: 'TEXT',
        nullable: true,
        description: 'host:port bootstrap address, primarily used for OSK.',
      },
      {
        name: 'availability_zone',
        type: 'TEXT',
        nullable: true,
        description: 'AWS availability zone (MSK only).',
      },
      {
        name: 'private_ip',
        type: 'TEXT',
        nullable: true,
        description: 'Private IP address of the broker (MSK only).',
      },
      { name: 'subnet_id', type: 'TEXT', nullable: true, description: 'VPC subnet ID (MSK only).' },
      { name: 'cidr_block', type: 'TEXT', nullable: true, description: 'CIDR block of the subnet (MSK only).' },
    ],
  },

  // ------------------------------------------------------------------
  {
    name: 'bootstrap_endpoints',
    description: 'One row per distinct auth/visibility combination of bootstrap broker strings.',
    columns: [
      {
        name: 'cluster_key',
        type: 'TEXT',
        nullable: false,
        description: 'Foreign key to clusters.cluster_key.',
      },
      {
        name: 'auth_type',
        type: 'TEXT',
        nullable: false,
        description: 'Authentication type: PLAINTEXT, TLS, SASL_SCRAM, SASL_IAM, PUBLIC_TLS, osk_bootstrap, etc.',
      },
      {
        name: 'visibility',
        type: 'TEXT',
        nullable: true,
        description: "Network visibility: PRIVATE, PUBLIC, or VPC. NULL for OSK.",
      },
      {
        name: 'endpoints',
        type: 'TEXT',
        nullable: true,
        description: 'Comma-separated list of host:port broker addresses.',
      },
    ],
  },

  // ------------------------------------------------------------------
  {
    name: 'topics',
    description: 'One row per Kafka topic with common config fields hoisted to dedicated columns.',
    columns: [
      {
        name: 'cluster_key',
        type: 'TEXT',
        nullable: false,
        description: 'Foreign key to clusters.cluster_key.',
      },
      {
        name: 'source_type',
        type: 'TEXT',
        nullable: false,
        description: "Source system: 'msk' or 'osk'.",
      },
      { name: 'name', type: 'TEXT', nullable: false, description: 'Kafka topic name.' },
      {
        name: 'is_internal',
        type: 'BOOLEAN',
        nullable: false,
        description: "True if the topic name starts with '__' (internal Kafka topic).",
      },
      { name: 'partitions', type: 'INTEGER', nullable: false, description: 'Number of partitions.' },
      {
        name: 'replication_factor',
        type: 'INTEGER',
        nullable: false,
        description: 'Replication factor across brokers.',
      },
      {
        name: 'cleanup_policy',
        type: 'TEXT',
        nullable: true,
        description: "Value of the cleanup.policy config (e.g. 'delete', 'compact').",
      },
      {
        name: 'retention_ms',
        type: 'BIGINT',
        nullable: true,
        description: 'retention.ms config value in milliseconds.',
      },
      {
        name: 'retention_bytes',
        type: 'BIGINT',
        nullable: true,
        description: 'retention.bytes config value; -1 means unlimited.',
      },
      {
        name: 'min_insync_replicas',
        type: 'INTEGER',
        nullable: true,
        description: 'min.insync.replicas config value.',
      },
      {
        name: 'remote_storage_enable',
        type: 'BOOLEAN',
        nullable: true,
        description: 'True if remote/tiered storage is enabled for this topic.',
      },
      {
        name: 'compression_type',
        type: 'TEXT',
        nullable: true,
        description: 'compression.type config value (e.g. producer, gzip, lz4).',
      },
    ],
  },

  // ------------------------------------------------------------------
  {
    name: 'topic_configs',
    description: 'Full raw configuration map for every topic — one row per config key.',
    columns: [
      {
        name: 'cluster_key',
        type: 'TEXT',
        nullable: false,
        description: 'Foreign key to clusters.cluster_key.',
      },
      {
        name: 'topic_name',
        type: 'TEXT',
        nullable: false,
        description: 'Kafka topic name; join with topics.name.',
      },
      { name: 'config_key', type: 'TEXT', nullable: false, description: 'Kafka topic configuration key.' },
      { name: 'config_value', type: 'TEXT', nullable: true, description: 'Configuration value (may be null).' },
    ],
  },

  // ------------------------------------------------------------------
  {
    name: 'acls',
    description: 'One row per Kafka ACL entry across all clusters.',
    columns: [
      {
        name: 'cluster_key',
        type: 'TEXT',
        nullable: false,
        description: 'Foreign key to clusters.cluster_key.',
      },
      {
        name: 'source_type',
        type: 'TEXT',
        nullable: false,
        description: "Source system: 'msk' or 'osk'.",
      },
      {
        name: 'resource_type',
        type: 'TEXT',
        nullable: false,
        description: 'Kafka resource type: Topic, Group, Cluster, TransactionalId, etc.',
      },
      {
        name: 'resource_name',
        type: 'TEXT',
        nullable: false,
        description: "Resource name or '*' for wildcard.",
      },
      {
        name: 'pattern_type',
        type: 'TEXT',
        nullable: false,
        description: 'ACL pattern type: LITERAL, PREFIXED, or MATCH.',
      },
      {
        name: 'principal',
        type: 'TEXT',
        nullable: false,
        description: 'Kafka principal (e.g. User:alice).',
      },
      {
        name: 'host',
        type: 'TEXT',
        nullable: false,
        description: "Allowed host IP or '*' for any host.",
      },
      {
        name: 'operation',
        type: 'TEXT',
        nullable: false,
        description: 'Kafka operation: Read, Write, Create, Delete, Describe, All, etc.',
      },
      {
        name: 'permission_type',
        type: 'TEXT',
        nullable: false,
        description: 'Permission: ALLOW or DENY.',
      },
    ],
  },

  // ------------------------------------------------------------------
  {
    name: 'connectors',
    description: 'One row per connector — both MSK Connect and self-managed connectors.',
    columns: [
      {
        name: 'cluster_key',
        type: 'TEXT',
        nullable: false,
        description: 'Foreign key to clusters.cluster_key.',
      },
      {
        name: 'source_type',
        type: 'TEXT',
        nullable: false,
        description: "Source system: 'msk' or 'osk'.",
      },
      {
        name: 'connector_type',
        type: 'TEXT',
        nullable: false,
        description: "Connector category: 'msk_connect' or 'self_managed'.",
      },
      { name: 'name', type: 'TEXT', nullable: false, description: 'Connector name.' },
      { name: 'arn', type: 'TEXT', nullable: true, description: 'ARN for MSK Connect connectors; NULL otherwise.' },
      { name: 'state', type: 'TEXT', nullable: true, description: 'Connector runtime state (e.g. RUNNING, PAUSED).' },
      {
        name: 'connector_class',
        type: 'TEXT',
        nullable: true,
        description: "Java class from the connector.class config property.",
      },
      {
        name: 'tasks_max',
        type: 'INTEGER',
        nullable: true,
        description: 'tasks.max config value — maximum parallelism.',
      },
      {
        name: 'topics_pattern',
        type: 'TEXT',
        nullable: true,
        description: "Value of the 'topics' or 'topics.regex' config.",
      },
      {
        name: 'worker_count',
        type: 'INTEGER',
        nullable: true,
        description: 'Number of MSK Connect provisioned workers.',
      },
      {
        name: 'mcu_count',
        type: 'INTEGER',
        nullable: true,
        description: 'MSK Connect MCU (MSK Connector Unit) count.',
      },
      {
        name: 'autoscaling_enabled',
        type: 'BOOLEAN',
        nullable: true,
        description: 'True if MSK Connect autoscaling is configured.',
      },
      {
        name: 'creation_time',
        type: 'TEXT',
        nullable: true,
        description: 'ISO 8601 timestamp when the connector was created.',
      },
      {
        name: 'connect_host',
        type: 'TEXT',
        nullable: true,
        description: 'REST API host for self-managed connectors; NULL for MSK Connect.',
      },
    ],
  },

  // ------------------------------------------------------------------
  {
    name: 'connector_configs',
    description: 'Full raw configuration map for every connector — one row per config key.',
    columns: [
      {
        name: 'cluster_key',
        type: 'TEXT',
        nullable: false,
        description: 'Foreign key to clusters.cluster_key.',
      },
      {
        name: 'connector_name',
        type: 'TEXT',
        nullable: false,
        description: 'Connector name; join with connectors.name.',
      },
      { name: 'config_key', type: 'TEXT', nullable: false, description: 'Connector configuration key.' },
      { name: 'config_value', type: 'TEXT', nullable: true, description: 'Configuration value (may be null).' },
    ],
  },

  // ------------------------------------------------------------------
  {
    name: 'discovered_clients',
    description: 'Kafka client observations scraped from broker logs.',
    columns: [
      {
        name: 'cluster_key',
        type: 'TEXT',
        nullable: false,
        description: 'Foreign key to clusters.cluster_key.',
      },
      {
        name: 'source_type',
        type: 'TEXT',
        nullable: false,
        description: "Source system: 'msk' or 'osk'.",
      },
      {
        name: 'kafka_client_id',
        type: 'TEXT',
        nullable: true,
        description: 'Client-supplied Kafka client.id string.',
      },
      {
        name: 'role',
        type: 'TEXT',
        nullable: true,
        description: 'Observed client role: Producer, Consumer, Admin, etc.',
      },
      {
        name: 'topic',
        type: 'TEXT',
        nullable: true,
        description: 'Topic the client was observed accessing.',
      },
      {
        name: 'auth',
        type: 'TEXT',
        nullable: true,
        description: 'Authentication method observed: IAM, SASL, etc.',
      },
      {
        name: 'principal',
        type: 'TEXT',
        nullable: true,
        description: 'Kafka principal associated with the client.',
      },
      {
        name: 'observed_at',
        type: 'TEXT',
        nullable: true,
        description: 'ISO 8601 timestamp when the client was first observed.',
      },
    ],
  },

  // ------------------------------------------------------------------
  {
    name: 'metrics_timeseries',
    description: 'Flattened time-series metrics — one row per (cluster, metric, time window).',
    columns: [
      {
        name: 'cluster_key',
        type: 'TEXT',
        nullable: false,
        description: 'Foreign key to clusters.cluster_key.',
      },
      {
        name: 'source_type',
        type: 'TEXT',
        nullable: false,
        description: "Source system: 'msk' (CloudWatch) or 'osk' (Jolokia/Prometheus).",
      },
      {
        name: 'metric_name',
        type: 'TEXT',
        nullable: false,
        description: 'Metric name: BytesInPerSec, BytesOutPerSec, MessagesInPerSec, PartitionCount, etc.',
      },
      {
        name: 'window_start',
        type: 'TEXT',
        nullable: false,
        description: 'ISO 8601 start of the measurement window.',
      },
      {
        name: 'window_end',
        type: 'TEXT',
        nullable: false,
        description: 'ISO 8601 end of the measurement window.',
      },
      {
        name: 'value',
        type: 'DOUBLE',
        nullable: true,
        description: 'Metric value; NULL where CloudWatch data is absent.',
      },
    ],
  },

  // ------------------------------------------------------------------
  {
    name: 'metric_aggregates',
    description: 'Pre-computed aggregate statistics per (cluster, metric) over the full metric window.',
    columns: [
      {
        name: 'cluster_key',
        type: 'TEXT',
        nullable: false,
        description: 'Foreign key to clusters.cluster_key.',
      },
      {
        name: 'source_type',
        type: 'TEXT',
        nullable: false,
        description: "Source system: 'msk' or 'osk'.",
      },
      {
        name: 'metric_name',
        type: 'TEXT',
        nullable: false,
        description: 'Metric name matching metrics_timeseries.metric_name.',
      },
      {
        name: 'avg_value',
        type: 'DOUBLE',
        nullable: true,
        description: 'Average metric value across the window (NULLs excluded).',
      },
      {
        name: 'min_value',
        type: 'DOUBLE',
        nullable: true,
        description: 'Minimum observed metric value.',
      },
      {
        name: 'max_value',
        type: 'DOUBLE',
        nullable: true,
        description: 'Maximum (peak) observed metric value.',
      },
    ],
  },

  // ------------------------------------------------------------------
  {
    name: 'region_costs',
    description: 'AWS Cost Explorer results — one row per (region, date range, service, usage type).',
    columns: [
      {
        name: 'region',
        type: 'TEXT',
        nullable: false,
        description: 'AWS region code (e.g. us-east-1).',
      },
      {
        name: 'start_date',
        type: 'TEXT',
        nullable: false,
        description: 'ISO 8601 start date of the cost period.',
      },
      {
        name: 'end_date',
        type: 'TEXT',
        nullable: false,
        description: 'ISO 8601 end date of the cost period.',
      },
      {
        name: 'service',
        type: 'TEXT',
        nullable: false,
        description: "AWS service name, e.g. 'Amazon Managed Streaming for Apache Kafka'.",
      },
      {
        name: 'usage_type',
        type: 'TEXT',
        nullable: true,
        description: 'Cost Explorer usage type dimension.',
      },
      {
        name: 'unblended_cost',
        type: 'DOUBLE',
        nullable: true,
        description: 'Unblended cost in USD.',
      },
      {
        name: 'blended_cost',
        type: 'DOUBLE',
        nullable: true,
        description: 'Blended cost in USD (amortized across accounts).',
      },
      {
        name: 'amortized_cost',
        type: 'DOUBLE',
        nullable: true,
        description: 'Amortized cost in USD (upfront RI/SP fees spread over term).',
      },
      {
        name: 'net_amortized_cost',
        type: 'DOUBLE',
        nullable: true,
        description: 'Net amortized cost after discounts.',
      },
      {
        name: 'net_unblended_cost',
        type: 'DOUBLE',
        nullable: true,
        description: 'Net unblended cost after discounts.',
      },
    ],
  },

  // ------------------------------------------------------------------
  {
    name: 'schema_registries',
    description: 'One row per Schema Registry instance — Confluent or AWS Glue.',
    columns: [
      {
        name: 'registry_key',
        type: 'TEXT',
        nullable: false,
        description: 'Unique key — URL for Confluent registries, ARN for Glue registries.',
      },
      {
        name: 'registry_type',
        type: 'TEXT',
        nullable: false,
        description: "Registry implementation: 'confluent' or 'glue'.",
      },
      {
        name: 'url',
        type: 'TEXT',
        nullable: true,
        description: 'HTTP URL of the Confluent Schema Registry; NULL for Glue.',
      },
      {
        name: 'registry_arn',
        type: 'TEXT',
        nullable: true,
        description: 'AWS ARN of the Glue registry; NULL for Confluent.',
      },
      {
        name: 'registry_name',
        type: 'TEXT',
        nullable: true,
        description: 'Human-readable registry name.',
      },
      {
        name: 'region',
        type: 'TEXT',
        nullable: true,
        description: 'AWS region for Glue registries; NULL for Confluent.',
      },
      {
        name: 'default_compatibility',
        type: 'TEXT',
        nullable: true,
        description: 'Default schema compatibility mode for Confluent registries (e.g. BACKWARD).',
      },
    ],
  },

  // ------------------------------------------------------------------
  {
    name: 'schema_subjects',
    description: 'One row per schema subject with rollup version statistics.',
    columns: [
      {
        name: 'registry_key',
        type: 'TEXT',
        nullable: false,
        description: 'Foreign key to schema_registries.registry_key.',
      },
      {
        name: 'subject_name',
        type: 'TEXT',
        nullable: false,
        description: 'Schema Registry subject name (e.g. orders-value).',
      },
      {
        name: 'schema_type',
        type: 'TEXT',
        nullable: true,
        description: 'Schema format: AVRO, JSON, or PROTOBUF.',
      },
      {
        name: 'compatibility',
        type: 'TEXT',
        nullable: true,
        description: 'Subject-level compatibility override (e.g. FULL).',
      },
      {
        name: 'version_count',
        type: 'INTEGER',
        nullable: true,
        description: 'Total number of versions registered for this subject.',
      },
      {
        name: 'latest_version',
        type: 'INTEGER',
        nullable: true,
        description: 'Highest version number currently registered.',
      },
      {
        name: 'latest_schema_id',
        type: 'INTEGER',
        nullable: true,
        description: 'Schema ID of the latest version.',
      },
    ],
  },

  // ------------------------------------------------------------------
  {
    name: 'schema_versions',
    description: 'One row per individual schema version with the raw schema definition.',
    columns: [
      {
        name: 'registry_key',
        type: 'TEXT',
        nullable: false,
        description: 'Foreign key to schema_registries.registry_key.',
      },
      {
        name: 'subject_name',
        type: 'TEXT',
        nullable: false,
        description: 'Schema subject name; join with schema_subjects.subject_name.',
      },
      {
        name: 'version',
        type: 'INTEGER',
        nullable: false,
        description: 'Schema version number.',
      },
      {
        name: 'schema_id',
        type: 'INTEGER',
        nullable: true,
        description: 'Global schema ID assigned by the registry.',
      },
      {
        name: 'schema_type',
        type: 'TEXT',
        nullable: true,
        description: 'Schema format: AVRO, JSON, or PROTOBUF.',
      },
      {
        name: 'schema_definition',
        type: 'TEXT',
        nullable: true,
        description: 'Raw schema text (JSON for Avro/JSON Schema, Protobuf IDL for PROTOBUF).',
      },
      {
        name: 'status',
        type: 'TEXT',
        nullable: true,
        description: 'Glue schema version status (e.g. AVAILABLE); NULL for Confluent.',
      },
      {
        name: 'created_at',
        type: 'TEXT',
        nullable: true,
        description: 'ISO 8601 creation timestamp; populated for Glue, NULL for Confluent.',
      },
    ],
  },

  // ------------------------------------------------------------------
  {
    name: 'regions',
    description: 'One row per AWS region observed in the MSK discovery scan.',
    columns: [
      {
        name: 'name',
        type: 'TEXT',
        nullable: false,
        description: 'AWS region code (e.g. us-east-1).',
      },
      {
        name: 'cluster_count',
        type: 'INTEGER',
        nullable: false,
        description: 'Number of MSK clusters discovered in this region.',
      },
    ],
  },
]

// ---------------------------------------------------------------------------
// CREATE_STATEMENTS — derived programmatically from TABLE_METADATA.
// Guaranteed to stay in sync with the interfaces and metadata above.
// ---------------------------------------------------------------------------

export const CREATE_STATEMENTS: readonly string[] = TABLE_METADATA.map(buildCreateStatement)

// ---------------------------------------------------------------------------
// TABLE_NAMES — const tuple for literal typing
// ---------------------------------------------------------------------------

export const TABLE_NAMES = [
  'clusters',
  'brokers',
  'bootstrap_endpoints',
  'topics',
  'topic_configs',
  'acls',
  'connectors',
  'connector_configs',
  'discovered_clients',
  'metrics_timeseries',
  'metric_aggregates',
  'region_costs',
  'schema_registries',
  'schema_subjects',
  'schema_versions',
  'regions',
] as const

// ---------------------------------------------------------------------------
// Query result interfaces
// ---------------------------------------------------------------------------

export interface QueryResultColumn {
  name: string
  type: string // Arrow type name — e.g. 'Utf8', 'Int32', 'Float64', 'TimestampMicrosecond'
  jsType: 'string' | 'number' | 'bigint' | 'boolean' | 'date' | 'null' | 'unknown'
}

export interface QueryResult {
  columns: QueryResultColumn[]
  rows: Record<string, unknown>[]
  rowCount: number
  durationMs: number
  sql: string
}

export interface QueryError {
  message: string
  sql: string
  durationMs: number
}

// ---------------------------------------------------------------------------
// Saved query
// ---------------------------------------------------------------------------

export interface SavedQuery {
  id: string
  name: string
  description: string
  category: 'topics' | 'acls' | 'metrics' | 'costs' | 'connectors' | 'clients' | 'schemas' | 'overview'
  sql: string
  builtIn: boolean // true for seeded examples (cannot be deleted)
}
