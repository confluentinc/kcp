import type { SavedQuery } from '@/lib/duckdb/schema'

export const SEED_QUERIES: SavedQuery[] = [
  // --- overview ---
  {
    id: 'seed-overview-clusters',
    name: 'All clusters',
    description: 'Every cluster across MSK and OSK with key specs.',
    category: 'overview',
    sql: `SELECT source_type, name, region, kafka_version, cluster_type,
       number_of_brokers, tiered_storage
FROM clusters
ORDER BY source_type, name;`,
    builtIn: true,
  },
  // --- topics ---
  {
    id: 'seed-topics-biggest',
    name: 'Top 25 topics by partitions',
    description: 'Identifies the most-partitioned topics across all clusters.',
    category: 'topics',
    sql: `SELECT c.name AS cluster, t.name AS topic, t.partitions, t.replication_factor,
       t.cleanup_policy, t.retention_ms / 1000 / 3600 AS retention_hours
FROM topics t
JOIN clusters c USING (cluster_key)
WHERE NOT t.is_internal
ORDER BY t.partitions DESC
LIMIT 25;`,
    builtIn: true,
  },
  {
    id: 'seed-topics-tiered',
    name: 'Topics with tiered storage',
    description: 'Topics that have remote/tiered storage enabled.',
    category: 'topics',
    sql: `SELECT c.name AS cluster, t.name AS topic, t.partitions, t.retention_ms
FROM topics t
JOIN clusters c USING (cluster_key)
WHERE t.remote_storage_enable
ORDER BY c.name, t.name;`,
    builtIn: true,
  },
  {
    id: 'seed-topics-compact',
    name: 'Non-standard cleanup policies',
    description: 'Topics with cleanup.policy other than the default "delete".',
    category: 'topics',
    sql: `SELECT c.name AS cluster, t.name AS topic, t.cleanup_policy, t.partitions
FROM topics t
JOIN clusters c USING (cluster_key)
WHERE t.cleanup_policy IS NOT NULL AND t.cleanup_policy <> 'delete'
ORDER BY c.name, t.name;`,
    builtIn: true,
  },
  // --- acls ---
  {
    id: 'seed-acls-by-principal',
    name: 'ACL count by principal',
    description: 'How many ACLs each principal has, by cluster.',
    category: 'acls',
    sql: `SELECT c.name AS cluster, a.principal, COUNT(*) AS acl_count
FROM acls a
JOIN clusters c USING (cluster_key)
GROUP BY c.name, a.principal
ORDER BY acl_count DESC;`,
    builtIn: true,
  },
  {
    id: 'seed-acls-wildcard',
    name: 'Wildcard / broad ACLs',
    description: 'Principals with * resource_name or * host — audit targets.',
    category: 'acls',
    sql: `SELECT c.name AS cluster, a.principal, a.resource_type, a.resource_name, a.host, a.operation, a.permission_type
FROM acls a
JOIN clusters c USING (cluster_key)
WHERE a.resource_name = '*' OR a.host = '*'
ORDER BY c.name, a.principal;`,
    builtIn: true,
  },
  {
    id: 'seed-acls-topic-coverage',
    name: 'Topics with / without ACLs',
    description: 'Non-internal topics joined against ACL coverage. Rows with null principal = no ACL.',
    category: 'acls',
    sql: `SELECT c.name AS cluster, t.name AS topic,
       COUNT(DISTINCT a.principal) AS principals_with_acl
FROM topics t
JOIN clusters c USING (cluster_key)
LEFT JOIN acls a
  ON a.cluster_key = t.cluster_key
 AND a.resource_type = 'Topic'
 AND (a.resource_name = t.name OR (a.pattern_type = 'PREFIXED' AND t.name LIKE a.resource_name || '%'))
WHERE NOT t.is_internal
GROUP BY c.name, t.name
ORDER BY principals_with_acl ASC, c.name, t.name;`,
    builtIn: true,
  },
  // --- connectors ---
  {
    id: 'seed-connectors-by-class',
    name: 'Connectors by connector.class',
    description: 'Distribution of connector plugins across clusters.',
    category: 'connectors',
    sql: `SELECT connector_class, connector_type, COUNT(*) AS n
FROM connectors
WHERE connector_class IS NOT NULL
GROUP BY connector_class, connector_type
ORDER BY n DESC;`,
    builtIn: true,
  },
  // --- clients ---
  {
    id: 'seed-clients-by-role',
    name: 'Discovered clients by role',
    description: 'Producer/Consumer counts per cluster.',
    category: 'clients',
    sql: `SELECT c.name AS cluster, dc.role, COUNT(DISTINCT dc.kafka_client_id) AS clients
FROM discovered_clients dc
JOIN clusters c USING (cluster_key)
GROUP BY c.name, dc.role
ORDER BY c.name, dc.role;`,
    builtIn: true,
  },
  {
    id: 'seed-clients-topic-hotness',
    name: 'Topic hotness (client count)',
    description: 'Topics ranked by number of distinct clients observed.',
    category: 'clients',
    sql: `SELECT c.name AS cluster, dc.topic, COUNT(DISTINCT dc.kafka_client_id) AS clients
FROM discovered_clients dc
JOIN clusters c USING (cluster_key)
WHERE dc.topic IS NOT NULL
GROUP BY c.name, dc.topic
ORDER BY clients DESC
LIMIT 50;`,
    builtIn: true,
  },
  // --- metrics ---
  {
    id: 'seed-metrics-throughput-peak',
    name: 'Peak BytesIn/Out per cluster',
    description: 'Peak ingress + egress throughput across the metric window.',
    category: 'metrics',
    sql: `SELECT c.name AS cluster,
       MAX(CASE WHEN metric_name = 'BytesInPerSec'  THEN value END) AS peak_bytes_in,
       MAX(CASE WHEN metric_name = 'BytesOutPerSec' THEN value END) AS peak_bytes_out
FROM metrics_timeseries m
JOIN clusters c USING (cluster_key)
WHERE metric_name IN ('BytesInPerSec','BytesOutPerSec')
GROUP BY c.name
ORDER BY peak_bytes_in DESC NULLS LAST;`,
    builtIn: true,
  },
  {
    id: 'seed-metrics-trend',
    name: 'BytesInPerSec trend (time-series)',
    description: 'Time-series ingress per cluster — chart as line over window_start.',
    category: 'metrics',
    sql: `SELECT window_start, c.name AS cluster, value
FROM metrics_timeseries m
JOIN clusters c USING (cluster_key)
WHERE metric_name = 'BytesInPerSec'
ORDER BY window_start;`,
    builtIn: true,
  },
  // --- costs ---
  {
    id: 'seed-costs-by-service',
    name: 'Cost by service (per region)',
    description: 'Unblended cost summed by service across the query window.',
    category: 'costs',
    sql: `SELECT region, service, ROUND(SUM(unblended_cost), 2) AS unblended
FROM region_costs
GROUP BY region, service
ORDER BY unblended DESC;`,
    builtIn: true,
  },
  // --- schemas ---
  {
    id: 'seed-schemas-evolving',
    name: 'Evolving schemas (>1 version)',
    description: 'Subjects with more than one version — likely evolving contracts.',
    category: 'schemas',
    sql: `SELECT sr.registry_key, ss.subject_name, ss.schema_type, ss.version_count
FROM schema_subjects ss
JOIN schema_registries sr USING (registry_key)
WHERE ss.version_count > 1
ORDER BY ss.version_count DESC;`,
    builtIn: true,
  },
]
