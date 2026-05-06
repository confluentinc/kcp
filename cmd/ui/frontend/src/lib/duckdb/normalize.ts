/**
 * normalize.ts — Workstream B
 *
 * Pure function that transforms a ProcessedState (from the KCP API) into
 * NormalizedTables ready for DuckDB ingestion.
 *
 * Rules:
 *  - Pure: no side effects, no I/O, no console.log.
 *  - Null-safe: missing/optional fields produce null in rows, never undefined.
 *  - Deterministic: source order is preserved.
 *  - Timestamps: ISO 8601 strings throughout.
 *  - Number parsing: Kafka topic config strings are parsed via Number(); NaN → null.
 *
 * Import notes:
 *  - All `import type` statements use relative paths (types stripped at runtime by
 *    --experimental-strip-types; no actual module loading occurs for type-only imports).
 *  - The single runtime value import (BOOTSTRAP_BROKER_LABELS) uses a relative path
 *    to constants/index.ts which has no @/ dependencies, making this file runnable
 *    directly under `node --experimental-strip-types`.
 */

// Runtime import — constants/index.ts has no @/ deps so Node can load it directly.
import { BOOTSTRAP_BROKER_LABELS } from '../../constants/index.ts'

// Type-only imports — stripped at runtime, relative paths for Node compatibility.
import type { ProcessedState } from '../../types/api/state.ts'
import type { ProcessedOSKCluster } from '../../types/osk/index.ts'
import type {
  NormalizedTables,
  ClusterRow,
  BrokerRow,
  BootstrapEndpointRow,
  TopicRow,
  TopicConfigRow,
  AclRow,
  ConnectorRow,
  ConnectorConfigRow,
  DiscoveredClientRow,
  MetricsTimeseriesRow,
  MetricAggregateRow,
  RegionCostRow,
  SchemaRegistryRow,
  SchemaSubjectRow,
  SchemaVersionRow,
  RegionRow,
} from './schema.ts'

// ---------------------------------------------------------------------------
// Local type aliases (avoid importing types that transitively use @/ in values)
// ---------------------------------------------------------------------------

/** Minimum shape of a Cluster as delivered in ProcessedState.msk_data.regions[].clusters[] */
interface MSKClusterLike {
  name: string
  arn?: string
  region?: string
  aws_client_information?: {
    cluster_networking?: {
      subnets?: Array<{
        subnet_msk_broker_id?: number
        subnet_id?: string
        availability_zone?: string
        private_ip_address?: string
        cidr_block?: string
      }>
    }
    msk_cluster_config?: {
      ClusterName?: string
      ClusterArn?: string
      ClusterType?: string
      Provisioned?: {
        NumberOfBrokerNodes?: number
        BrokerNodeGroupInfo?: {
          InstanceType?: string
        }
        CurrentBrokerSoftwareInfo?: {
          KafkaVersion?: string
        }
        EnhancedMonitoring?: string
      }
      [key: string]: unknown
    }
    connectors?: Array<{
      connector_arn?: string
      connector_name?: string
      connector_state?: string
      creation_time?: string
      capacity?: {
        AutoScaling?: unknown
        ProvisionedCapacity?: {
          WorkerCount?: number
          McuCount?: number
        }
      }
      connector_configuration?: Record<string, string>
      [key: string]: unknown
    }>
    bootstrap_brokers?: Record<string, string | null | undefined>
    [key: string]: unknown
  }
  kafka_admin_client_information?: KafkaAdminLike
  metrics?: {
    metadata?: {
      period?: number
      instance_type?: string
      broker_type?: string
      kafka_version?: string
      enhanced_monitoring?: string
      tiered_storage?: boolean
      follower_fetching?: boolean
    }
    results?: Array<{
      // Flattened ProcessedMetric format (from Go ProcessState)
      start?: string
      end?: string
      label?: string
      value?: number | null
      // Raw CloudWatch format (fixture / state file before ProcessState)
      Label?: string
      Timestamps?: string[]
      Values?: (number | null)[]
    }>
  }
  discovered_clients?: Array<DiscoveredClientLike> | null
}

interface KafkaAdminLike {
  topics?: {
    details?: Array<{
      name: string
      partitions: number
      replication_factor: number
      configurations?: Record<string, string | null | undefined>
    }>
  }
  acls?: Array<{
    ResourceType: string
    ResourceName: string
    ResourcePatternType: string
    Principal: string
    Host: string
    Operation: string
    PermissionType: string
  }>
  self_managed_connectors?: {
    connectors?: Array<{
      name: string
      config?: Record<string, unknown>
      state?: string
      connect_host?: string
    }>
  } | null
  [key: string]: unknown
}

interface DiscoveredClientLike {
  client_id?: string
  role?: string
  topic?: string
  auth?: string
  principal?: string
  timestamp?: string
}

interface SchemaRegistryLike {
  type?: string
  url: string
  default_compatibility?: string
  contexts?: string[]
  subjects?: Array<{
    name: string
    schema_type?: string
    compatibility?: string
    versions?: Array<{
      schema?: string
      id?: number
      subject?: string
      version: number
      schemaType?: string
    }>
    latest_schema?: {
      schema?: string
      id?: number
      subject?: string
      version?: number
      schemaType?: string
    }
  }>
}

interface GlueSchemaRegistryLike {
  registry_name?: string
  registry_arn: string
  region?: string
  schemas?: Array<{
    schema_name: string
    schema_arn?: string
    data_format?: string
    versions?: Array<{
      schema_definition?: string
      data_format?: string
      version_number: number
      status?: string
      created_date?: string
    }>
    latest_version?: {
      version_number?: number
      schema_definition?: string
      data_format?: string
      status?: string
      created_date?: string
    } | null
  }>
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Return null if value is undefined or null; otherwise return it. */
function n<T>(v: T | undefined | null): T | null {
  return v == null ? null : v
}

/** Parse a string config value to a number; returns null on NaN or missing. */
function parseConfigNum(v: string | null | undefined): number | null {
  if (v == null) return null
  const num = Number(v)
  return Number.isNaN(num) ? null : num
}

/** Parse a string config value to a boolean "true"/"false"; returns null on missing. */
function parseConfigBool(v: string | null | undefined): boolean | null {
  if (v == null) return null
  return v.trim().toLowerCase() === 'true'
}

/**
 * Determine bootstrap endpoint visibility from a field name.
 * Names containing "VpcConnectivity" → 'VPC', "Public" → 'PUBLIC', else 'PRIVATE'.
 */
function endpointVisibility(fieldName: string): string {
  if (fieldName.includes('VpcConnectivity')) return 'VPC'
  if (fieldName.includes('Public')) return 'PUBLIC'
  return 'PRIVATE'
}

/**
 * Add window_end as ISO string by adding period_seconds to a start ISO string.
 */
function addSeconds(isoStart: string, periodSeconds: number): string {
  return new Date(new Date(isoStart).getTime() + periodSeconds * 1000).toISOString()
}

// ---------------------------------------------------------------------------
// MSK cluster normalizer
// ---------------------------------------------------------------------------

function normalizeMSKCluster(
  cluster: MSKClusterLike,
  tables: NormalizedTables,
): void {
  const clusterKey = cluster.arn ?? cluster.name
  const sourceType = 'msk'

  // --- clusters row ---
  const awsInfo = cluster.aws_client_information
  const mskConfig = awsInfo?.msk_cluster_config
  const provisioned = mskConfig?.Provisioned
  const metadata = cluster.metrics?.metadata

  const clusterRow: ClusterRow = {
    cluster_key: clusterKey,
    source_type: sourceType,
    name: cluster.name,
    region: n(cluster.region),
    kafka_version: n(
      provisioned?.CurrentBrokerSoftwareInfo?.KafkaVersion ??
      metadata?.kafka_version,
    ),
    cluster_type: n(mskConfig?.ClusterType),
    broker_type: n(metadata?.broker_type),
    instance_type: n(
      provisioned?.BrokerNodeGroupInfo?.InstanceType ??
      metadata?.instance_type,
    ),
    number_of_brokers: n(provisioned?.NumberOfBrokerNodes),
    enhanced_monitoring: n(
      provisioned?.EnhancedMonitoring ??
      metadata?.enhanced_monitoring,
    ),
    tiered_storage: metadata?.tiered_storage != null ? metadata.tiered_storage : null,
    follower_fetching: metadata?.follower_fetching != null ? metadata.follower_fetching : null,
    environment: null,
    location: null,
    last_scanned: null,
  }
  tables.clusters.push(clusterRow)

  // --- brokers rows (from subnets) ---
  const subnets = awsInfo?.cluster_networking?.subnets ?? []
  for (const subnet of subnets) {
    const brokerRow: BrokerRow = {
      cluster_key: clusterKey,
      broker_id: n(subnet.subnet_msk_broker_id),
      bootstrap_host: null,
      availability_zone: n(subnet.availability_zone),
      private_ip: n(subnet.private_ip_address),
      subnet_id: n(subnet.subnet_id),
      cidr_block: n(subnet.cidr_block),
    }
    tables.brokers.push(brokerRow)
  }

  // --- bootstrap_endpoints (MSK) ---
  const bootstrapBrokers = awsInfo?.bootstrap_brokers ?? {}
  for (const [fieldName, label] of Object.entries(BOOTSTRAP_BROKER_LABELS)) {
    const endpoints = (bootstrapBrokers as Record<string, string | null | undefined>)[fieldName]
    if (endpoints == null || endpoints === '') continue
    const endpointRow: BootstrapEndpointRow = {
      cluster_key: clusterKey,
      auth_type: label,
      visibility: endpointVisibility(fieldName),
      endpoints: endpoints,
    }
    tables.bootstrap_endpoints.push(endpointRow)
  }

  // --- topics + topic_configs ---
  normalizeMSKOrOSKTopics(clusterKey, sourceType, cluster.kafka_admin_client_information, tables)

  // --- ACLs ---
  normalizeMSKOrOSKAcls(clusterKey, sourceType, cluster.kafka_admin_client_information, tables)

  // --- MSK Connect connectors ---
  const connectors = awsInfo?.connectors ?? []
  for (const c of connectors) {
    const connectorName = c.connector_name ?? ''
    const connConfig = c.connector_configuration ?? {}
    const capacity = c.capacity
    const provisionedCap = capacity?.ProvisionedCapacity

    const connectorRow: ConnectorRow = {
      cluster_key: clusterKey,
      source_type: sourceType,
      connector_type: 'msk_connect',
      name: connectorName,
      arn: n(c.connector_arn),
      state: n(c.connector_state),
      connector_class: n(connConfig['connector.class']),
      tasks_max: connConfig['tasks.max'] != null ? parseConfigNum(connConfig['tasks.max']) : null,
      topics_pattern: n(connConfig['topics'] ?? connConfig['topics.regex']),
      worker_count: n(provisionedCap?.WorkerCount),
      mcu_count: n(provisionedCap?.McuCount),
      autoscaling_enabled: capacity?.AutoScaling != null ? true : null,
      creation_time: n(c.creation_time),
      connect_host: null,
    }
    tables.connectors.push(connectorRow)

    for (const [k, v] of Object.entries(connConfig)) {
      const configRow: ConnectorConfigRow = {
        cluster_key: clusterKey,
        connector_name: connectorName,
        config_key: k,
        config_value: n(v),
      }
      tables.connector_configs.push(configRow)
    }
  }

  // --- self-managed connectors ---
  normalizeSelfManagedConnectors(clusterKey, sourceType, cluster.kafka_admin_client_information, tables)

  // --- discovered clients ---
  normalizeMSKOrOSKClients(clusterKey, sourceType, cluster.discovered_clients, tables)

  // --- metrics timeseries ---
  // Handle two formats:
  //   1. Flattened ProcessedMetric (from Go ProcessState): {start, end, label, value}
  //   2. Raw CloudWatch format (fixture / raw state file): {Label, Timestamps[], Values[]}
  const metricsResults = cluster.metrics?.results ?? []
  const periodSeconds = metadata?.period ?? 300

  // Detect format by inspecting the first element
  const isRawCloudWatchFormat =
    metricsResults.length > 0 &&
    Array.isArray((metricsResults[0] as { Timestamps?: unknown }).Timestamps)

  const mskAggMap: Map<string, { sum: number; min: number; max: number; count: number }> = new Map()

  if (isRawCloudWatchFormat) {
    // Raw CloudWatch: each entry has Label, Timestamps[], Values[]
    for (const rawResult of metricsResults) {
      const metricName = rawResult.Label ?? ''
      const timestamps = (rawResult.Timestamps ?? []) as string[]
      const values = (rawResult.Values ?? []) as (number | null)[]

      for (let i = 0; i < timestamps.length; i++) {
        const ts = timestamps[i]
        const val: number | null = values[i] ?? null

        const tsRow: MetricsTimeseriesRow = {
          cluster_key: clusterKey,
          source_type: sourceType,
          metric_name: metricName,
          window_start: ts,
          window_end: addSeconds(ts, periodSeconds),
          value: val,
        }
        tables.metrics_timeseries.push(tsRow)
        accumulateAgg(mskAggMap, metricName, val)
      }
    }
  } else {
    // Flattened ProcessedMetric: each entry is one data point {start, end, label, value}
    for (const result of metricsResults) {
      const metricName = result.label ?? ''
      const val: number | null = result.value ?? null

      const tsRow: MetricsTimeseriesRow = {
        cluster_key: clusterKey,
        source_type: sourceType,
        metric_name: metricName,
        window_start: result.start ?? '',
        window_end: result.end ?? addSeconds(result.start ?? '', periodSeconds),
        value: val,
      }
      tables.metrics_timeseries.push(tsRow)
      accumulateAgg(mskAggMap, metricName, val)
    }
  }

  // Emit aggregate rows (computed from timeseries)
  for (const [metricName, agg] of mskAggMap.entries()) {
    const aggRow: MetricAggregateRow = {
      cluster_key: clusterKey,
      source_type: sourceType,
      metric_name: metricName,
      avg_value: agg.count > 0 ? agg.sum / agg.count : null,
      min_value: agg.count > 0 ? agg.min : null,
      max_value: agg.count > 0 ? agg.max : null,
    }
    tables.metric_aggregates.push(aggRow)
  }
}

/** Accumulate a metric value into an aggregation map entry. */
function accumulateAgg(
  map: Map<string, { sum: number; min: number; max: number; count: number }>,
  metricName: string,
  val: number | null,
): void {
  if (val !== null) {
    const existing = map.get(metricName)
    if (existing) {
      existing.sum += val
      existing.min = Math.min(existing.min, val)
      existing.max = Math.max(existing.max, val)
      existing.count += 1
    } else {
      map.set(metricName, { sum: val, min: val, max: val, count: 1 })
    }
  } else if (!map.has(metricName)) {
    // Register metric with count=0 so an aggregate row is still emitted (all-null case)
    map.set(metricName, { sum: 0, min: 0, max: 0, count: 0 })
  }
}

// ---------------------------------------------------------------------------
// OSK cluster normalizer
// ---------------------------------------------------------------------------

function normalizeOSKCluster(
  cluster: ProcessedOSKCluster,
  tables: NormalizedTables,
): void {
  const clusterKey = cluster.id
  const sourceType = 'osk'

  // --- clusters row ---
  const meta = cluster.metadata

  const clusterRow: ClusterRow = {
    cluster_key: clusterKey,
    source_type: sourceType,
    name: cluster.id,
    region: null,
    kafka_version: n(meta?.kafka_version),
    cluster_type: 'OSS',
    broker_type: null,
    instance_type: null,
    number_of_brokers: null,
    enhanced_monitoring: null,
    tiered_storage: null,
    follower_fetching: null,
    environment: n(meta?.environment),
    location: n(meta?.location),
    last_scanned: n(meta?.last_scanned),
  }
  tables.clusters.push(clusterRow)

  // --- brokers rows (from bootstrap servers) ---
  for (const server of cluster.bootstrap_servers ?? []) {
    const brokerRow: BrokerRow = {
      cluster_key: clusterKey,
      broker_id: null,
      bootstrap_host: server,
      availability_zone: null,
      private_ip: null,
      subnet_id: null,
      cidr_block: null,
    }
    tables.brokers.push(brokerRow)
  }

  // --- bootstrap_endpoints (OSK: one row for all servers) ---
  const endpointRow: BootstrapEndpointRow = {
    cluster_key: clusterKey,
    auth_type: 'osk_bootstrap',
    visibility: 'PRIVATE',
    endpoints: (cluster.bootstrap_servers ?? []).join(',') || null,
  }
  tables.bootstrap_endpoints.push(endpointRow)

  // --- topics + topic_configs ---
  normalizeMSKOrOSKTopics(
    clusterKey,
    sourceType,
    cluster.kafka_admin_client_information as KafkaAdminLike,
    tables,
  )

  // --- ACLs ---
  normalizeMSKOrOSKAcls(
    clusterKey,
    sourceType,
    cluster.kafka_admin_client_information as KafkaAdminLike,
    tables,
  )

  // --- self-managed connectors ---
  normalizeSelfManagedConnectors(
    clusterKey,
    sourceType,
    cluster.kafka_admin_client_information as KafkaAdminLike,
    tables,
  )

  // --- discovered clients ---
  normalizeMSKOrOSKClients(
    clusterKey,
    sourceType,
    cluster.discovered_clients as DiscoveredClientLike[],
    tables,
  )

  // --- OSK metrics (already flat: results[].{start, end, label, value}) ---
  const metricsResults = cluster.metrics?.results ?? []
  for (const result of metricsResults) {
    const tsRow: MetricsTimeseriesRow = {
      cluster_key: clusterKey,
      source_type: sourceType,
      metric_name: result.label,
      window_start: result.start,
      window_end: result.end,
      value: result.value,
    }
    tables.metrics_timeseries.push(tsRow)
  }

  // --- OSK metric aggregates (from aggregates map) ---
  const aggregates = cluster.metrics?.aggregates ?? {}
  for (const [metricName, agg] of Object.entries(aggregates)) {
    const aggRow: MetricAggregateRow = {
      cluster_key: clusterKey,
      source_type: sourceType,
      metric_name: metricName,
      avg_value: agg.avg != null ? agg.avg : null,
      min_value: agg.min != null ? agg.min : null,
      max_value: agg.max != null ? agg.max : null,
    }
    tables.metric_aggregates.push(aggRow)
  }
}

// ---------------------------------------------------------------------------
// Shared sub-normalizers
// ---------------------------------------------------------------------------

function normalizeMSKOrOSKTopics(
  clusterKey: string,
  sourceType: 'msk' | 'osk',
  kafkaAdmin: KafkaAdminLike | undefined | null,
  tables: NormalizedTables,
): void {
  const topicDetails = kafkaAdmin?.topics?.details ?? []
  for (const topic of topicDetails) {
    const configs = topic.configurations ?? {}

    const topicRow: TopicRow = {
      cluster_key: clusterKey,
      source_type: sourceType,
      name: topic.name,
      is_internal: topic.name.startsWith('__'),
      partitions: topic.partitions,
      replication_factor: topic.replication_factor,
      cleanup_policy: n(configs['cleanup.policy']),
      retention_ms: parseConfigNum(configs['retention.ms'] ?? null),
      retention_bytes: parseConfigNum(configs['retention.bytes'] ?? null),
      min_insync_replicas: parseConfigNum(configs['min.insync.replicas'] ?? null),
      remote_storage_enable: parseConfigBool(configs['remote.storage.enable'] ?? null),
      compression_type: n(configs['compression.type']),
    }
    tables.topics.push(topicRow)

    // Emit one row per config key
    for (const [configKey, configValue] of Object.entries(configs)) {
      const configRow: TopicConfigRow = {
        cluster_key: clusterKey,
        topic_name: topic.name,
        config_key: configKey,
        config_value: n(configValue),
      }
      tables.topic_configs.push(configRow)
    }
  }
}

function normalizeMSKOrOSKAcls(
  clusterKey: string,
  sourceType: 'msk' | 'osk',
  kafkaAdmin: KafkaAdminLike | undefined | null,
  tables: NormalizedTables,
): void {
  const acls = kafkaAdmin?.acls ?? []
  for (const acl of acls) {
    const aclRow: AclRow = {
      cluster_key: clusterKey,
      source_type: sourceType,
      resource_type: acl.ResourceType,
      resource_name: acl.ResourceName,
      pattern_type: acl.ResourcePatternType,
      principal: acl.Principal,
      host: acl.Host,
      operation: acl.Operation,
      permission_type: acl.PermissionType,
    }
    tables.acls.push(aclRow)
  }
}

function normalizeSelfManagedConnectors(
  clusterKey: string,
  sourceType: 'msk' | 'osk',
  kafkaAdmin: KafkaAdminLike | undefined | null,
  tables: NormalizedTables,
): void {
  const connectors = kafkaAdmin?.self_managed_connectors?.connectors ?? []
  for (const c of connectors) {
    const config = c.config ?? {}
    const connectorName = c.name

    const getStr = (key: string): string | null => {
      const v = config[key]
      if (v == null) return null
      return String(v)
    }

    const connectorRow: ConnectorRow = {
      cluster_key: clusterKey,
      source_type: sourceType,
      connector_type: 'self_managed',
      name: connectorName,
      arn: null,
      state: n(c.state),
      connector_class: getStr('connector.class'),
      tasks_max: parseConfigNum(getStr('tasks.max')),
      topics_pattern: getStr('topics') ?? getStr('topics.regex'),
      worker_count: null,
      mcu_count: null,
      autoscaling_enabled: null,
      creation_time: null,
      connect_host: n(c.connect_host),
    }
    tables.connectors.push(connectorRow)

    for (const [k, v] of Object.entries(config)) {
      const configRow: ConnectorConfigRow = {
        cluster_key: clusterKey,
        connector_name: connectorName,
        config_key: k,
        config_value: v == null ? null : String(v),
      }
      tables.connector_configs.push(configRow)
    }
  }
}

function normalizeMSKOrOSKClients(
  clusterKey: string,
  sourceType: 'msk' | 'osk',
  discoveredClients: Array<DiscoveredClientLike> | null | undefined,
  tables: NormalizedTables,
): void {
  for (const client of discoveredClients ?? []) {
    const clientRow: DiscoveredClientRow = {
      cluster_key: clusterKey,
      source_type: sourceType,
      kafka_client_id: n(client.client_id),
      role: n(client.role),
      topic: n(client.topic),
      auth: n(client.auth),
      principal: n(client.principal),
      observed_at: n(client.timestamp),
    }
    tables.discovered_clients.push(clientRow)
  }
}

// ---------------------------------------------------------------------------
// Schema registry normalizers
// ---------------------------------------------------------------------------

function normalizeConfluentSchemaRegistry(
  sr: SchemaRegistryLike,
  tables: NormalizedTables,
): void {
  const registryKey = sr.url
  const registryRow: SchemaRegistryRow = {
    registry_key: registryKey,
    registry_type: 'confluent',
    url: sr.url,
    registry_arn: null,
    registry_name: null,
    region: null,
    default_compatibility: n(sr.default_compatibility),
  }
  tables.schema_registries.push(registryRow)

  for (const subject of sr.subjects ?? []) {
    const versions = subject.versions ?? []
    const latestSchema = subject.latest_schema

    const subjectRow: SchemaSubjectRow = {
      registry_key: registryKey,
      subject_name: subject.name,
      schema_type: n(subject.schema_type),
      compatibility: n(subject.compatibility),
      version_count: versions.length,
      latest_version: latestSchema?.version != null ? latestSchema.version : null,
      latest_schema_id: latestSchema?.id != null ? latestSchema.id : null,
    }
    tables.schema_subjects.push(subjectRow)

    for (const version of versions) {
      const versionRow: SchemaVersionRow = {
        registry_key: registryKey,
        subject_name: subject.name,
        version: version.version,
        schema_id: n(version.id),
        schema_type: n(version.schemaType ?? subject.schema_type),
        schema_definition: n(version.schema),
        status: null,
        created_at: null,
      }
      tables.schema_versions.push(versionRow)
    }
  }
}

function normalizeGlueSchemaRegistry(
  gr: GlueSchemaRegistryLike,
  tables: NormalizedTables,
): void {
  const registryKey = gr.registry_arn
  const registryRow: SchemaRegistryRow = {
    registry_key: registryKey,
    registry_type: 'glue',
    url: null,
    registry_arn: gr.registry_arn,
    registry_name: n(gr.registry_name),
    region: n(gr.region),
    default_compatibility: null,
  }
  tables.schema_registries.push(registryRow)

  for (const schema of gr.schemas ?? []) {
    const versions = schema.versions ?? []
    const latestVersion = schema.latest_version

    const subjectRow: SchemaSubjectRow = {
      registry_key: registryKey,
      subject_name: schema.schema_name,
      schema_type: n(schema.data_format),
      compatibility: null,
      version_count: versions.length,
      latest_version: latestVersion?.version_number != null ? latestVersion.version_number : null,
      latest_schema_id: null,
    }
    tables.schema_subjects.push(subjectRow)

    for (const version of versions) {
      const versionRow: SchemaVersionRow = {
        registry_key: registryKey,
        subject_name: schema.schema_name,
        version: version.version_number,
        schema_id: null,
        schema_type: n(version.data_format),
        schema_definition: n(version.schema_definition),
        status: n(version.status),
        created_at: n(version.created_date),
      }
      tables.schema_versions.push(versionRow)
    }
  }
}

// ---------------------------------------------------------------------------
// Main normalize function
// ---------------------------------------------------------------------------

export function normalize(state: ProcessedState): NormalizedTables {
  const tables: NormalizedTables = {
    clusters: [],
    brokers: [],
    bootstrap_endpoints: [],
    topics: [],
    topic_configs: [],
    acls: [],
    connectors: [],
    connector_configs: [],
    discovered_clients: [],
    metrics_timeseries: [],
    metric_aggregates: [],
    region_costs: [],
    schema_registries: [],
    schema_subjects: [],
    schema_versions: [],
    regions: [],
  }

  for (const source of state.sources ?? []) {
    if (source.type === 'msk' && source.msk_data) {
      for (const region of source.msk_data.regions ?? []) {
        // --- regions table ---
        const regionRow: RegionRow = {
          name: region.name,
          cluster_count: (region.clusters ?? []).length,
        }
        tables.regions.push(regionRow)

        // --- clusters ---
        for (const cluster of region.clusters ?? []) {
          normalizeMSKCluster(cluster as unknown as MSKClusterLike, tables)
        }

        // --- region costs ---
        // The costs object shape varies: ProcessedRegionCosts has a .results[] array.
        const costsResults = (region as unknown as {
          costs?: {
            results?: Array<{
              start: string
              end: string
              service: string
              usage_type?: string
              values?: {
                unblended_cost?: number
                blended_cost?: number
                amortized_cost?: number
                net_amortized_cost?: number
                net_unblended_cost?: number
              }
            }>
          }
        }).costs?.results ?? []

        for (const cost of costsResults) {
          const costRow: RegionCostRow = {
            region: region.name,
            start_date: cost.start,
            end_date: cost.end,
            service: cost.service,
            usage_type: n(cost.usage_type),
            unblended_cost: n(cost.values?.unblended_cost),
            blended_cost: n(cost.values?.blended_cost),
            amortized_cost: n(cost.values?.amortized_cost),
            net_amortized_cost: n(cost.values?.net_amortized_cost),
            net_unblended_cost: n(cost.values?.net_unblended_cost),
          }
          tables.region_costs.push(costRow)
        }
      }
    } else if (source.type === 'osk' && source.osk_data) {
      for (const cluster of source.osk_data.clusters ?? []) {
        normalizeOSKCluster(cluster, tables)
      }
    }
  }

  // --- schema registries ---
  const schemaRegs = state.schema_registries
  for (const sr of (schemaRegs as { confluent_schema_registry?: SchemaRegistryLike[] } | undefined)?.confluent_schema_registry ?? []) {
    normalizeConfluentSchemaRegistry(sr, tables)
  }
  for (const gr of (schemaRegs as { aws_glue?: GlueSchemaRegistryLike[] } | undefined)?.aws_glue ?? []) {
    normalizeGlueSchemaRegistry(gr, tables)
  }

  return tables
}
