/**
 * normalize.verify.ts — Workstream B sanity check
 *
 * Loads a fixture and runs assertions on the normalize() output.
 * Run with:
 *   node --experimental-strip-types src/lib/duckdb/normalize.verify.ts
 *
 * NOTE: The fixture at tests/e2e/fixtures/state-migration.json is in raw
 * State format (msk_sources / osk_sources keys) as produced by kcp-state.json.
 * The Go backend's ProcessState() converts it to ProcessedState before the
 * frontend receives it. We replicate that flattening here so the verify script
 * runs without the Go server.
 *
 * The MSK cluster metrics in the fixture use raw CloudWatch format
 * (Timestamps[]/Values[] per metric). normalize.ts detects this and handles it.
 */

import fs from 'node:fs'
import path from 'node:path'
import { normalize } from './normalize.ts'
import type { NormalizedTables } from './schema.ts'

// ---------------------------------------------------------------------------
// Load fixture
// ---------------------------------------------------------------------------

const fixtureFile = path.resolve(
  process.cwd(),
  'tests/e2e/fixtures/state-migration.json',
)

if (!fs.existsSync(fixtureFile)) {
  console.error(`Fixture not found: ${fixtureFile}`)
  process.exit(1)
}

const raw = JSON.parse(fs.readFileSync(fixtureFile, 'utf-8')) as {
  msk_sources?: {
    regions?: Array<{
      name: string
      configurations?: unknown[]
      costs?: {
        metadata?: Record<string, unknown>
        results?: unknown[]
        query_info?: unknown
      }
      clusters?: Array<Record<string, unknown>>
    }>
  }
  osk_sources?: {
    clusters?: Array<Record<string, unknown>>
  }
  schema_registries?: {
    confluent_schema_registry?: Array<Record<string, unknown>>
    aws_glue?: Array<Record<string, unknown>>
  }
}

// ---------------------------------------------------------------------------
// Convert raw State → ProcessedState shape
// (mirrors Go report_service.ProcessState)
// ---------------------------------------------------------------------------

const processedState = {
  sources: [] as Array<{
    type: 'msk' | 'osk'
    msk_data?: { regions: unknown[] }
    osk_data?: { clusters: unknown[] }
  }>,
  schema_registries: raw.schema_registries,
  kcp_build_info: undefined,
  timestamp: new Date().toISOString(),
}

if (raw.msk_sources?.regions && raw.msk_sources.regions.length > 0) {
  const processedRegions = raw.msk_sources.regions.map((region) => ({
    name: region.name,
    configurations: region.configurations ?? [],
    costs: {
      region: region.name,
      metadata: {},
      results: region.costs?.results ?? [],
      aggregates: {},
      query_info: region.costs?.query_info ?? {},
    },
    clusters: (region.clusters ?? []).map((cluster) => ({ ...cluster })),
  }))

  processedState.sources.push({
    type: 'msk',
    msk_data: { regions: processedRegions },
  })
}

if (raw.osk_sources?.clusters && raw.osk_sources.clusters.length > 0) {
  processedState.sources.push({
    type: 'osk',
    osk_data: { clusters: raw.osk_sources.clusters.map((c) => ({ ...c })) },
  })
}

// ---------------------------------------------------------------------------
// Normalize
// ---------------------------------------------------------------------------

let tables: NormalizedTables
try {
  // We cast here because ProcessedState uses @/ types; the runtime shape is identical.
  tables = normalize(processedState as Parameters<typeof normalize>[0])
} catch (err) {
  console.error('normalize() threw an error:', err)
  process.exit(1)
}

// ---------------------------------------------------------------------------
// Assertions
// ---------------------------------------------------------------------------

let failures = 0

function assert(condition: boolean, message: string): void {
  if (!condition) {
    console.error(`  FAIL: ${message}`)
    failures++
  } else {
    console.log(`  OK:   ${message}`)
  }
}

const expectedKeys: (keyof NormalizedTables)[] = [
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
]

// 1. Every key is present and is an array
console.log('\n--- Checking table presence ---')
for (const key of expectedKeys) {
  assert(
    key in tables && Array.isArray(tables[key]),
    `tables.${key} is present and is an array`,
  )
}

// 2. clusters.length > 0
console.log('\n--- Checking cluster count ---')
if (tables.clusters.length > 0) {
  assert(true, `clusters has ${tables.clusters.length} row(s)`)
} else {
  console.warn('  WARN: clusters is empty — fixture may have no clusters')
}

// 3. Every topic row references a known cluster_key
console.log('\n--- Checking topic cluster_key references ---')
const clusterKeys = new Set(tables.clusters.map((c) => c.cluster_key))
const orphanTopics = tables.topics.filter((t) => !clusterKeys.has(t.cluster_key))
assert(
  orphanTopics.length === 0,
  `all ${tables.topics.length} topic rows reference a known cluster_key`,
)

// 4. Every ACL row references a known cluster_key
console.log('\n--- Checking ACL cluster_key references ---')
const orphanAcls = tables.acls.filter((a) => !clusterKeys.has(a.cluster_key))
assert(
  orphanAcls.length === 0,
  `all ${tables.acls.length} ACL rows reference a known cluster_key`,
)

// 5. MSK clusters with metrics → metrics_timeseries is non-empty
console.log('\n--- Checking metrics_timeseries ---')
const anyMSKClusterWithMetrics = processedState.sources.some(
  (s) =>
    s.type === 'msk' &&
    (s.msk_data?.regions as Array<{ clusters?: Array<{ metrics?: { results?: unknown[] } }> }> | undefined)?.some((r) =>
      r.clusters?.some((c) => (c.metrics?.results?.length ?? 0) > 0),
    ),
)
if (anyMSKClusterWithMetrics) {
  assert(
    tables.metrics_timeseries.length > 0,
    `metrics_timeseries populated (${tables.metrics_timeseries.length} rows) for clusters with metrics`,
  )
}

// 6. No undefined values in any topic row
console.log('\n--- Checking for undefined values in topics ---')
const topicsWithUndefined = tables.topics.filter((t) =>
  Object.values(t).some((v) => v === undefined),
)
assert(
  topicsWithUndefined.length === 0,
  'no topic row has any undefined field (all nulls are explicit)',
)

// 7. bootstrap_endpoints: at least one row per cluster
console.log('\n--- Checking bootstrap_endpoints ---')
const endpointClusterKeys = new Set(tables.bootstrap_endpoints.map((e) => e.cluster_key))
const clustersWithoutEndpoints = [...clusterKeys].filter((k) => !endpointClusterKeys.has(k))
assert(
  clustersWithoutEndpoints.length === 0,
  `all ${tables.clusters.length} clusters have at least one bootstrap_endpoint row`,
)

// ---------------------------------------------------------------------------
// Summary table
// ---------------------------------------------------------------------------

console.log('\n--- Row count summary ---')
const width = 28
const lines: string[] = []
lines.push(`${'Table'.padEnd(width)} Rows`)
lines.push(`${'-'.repeat(width)} -----`)
for (const key of expectedKeys) {
  lines.push(`${key.padEnd(width)} ${tables[key].length}`)
}
console.log(lines.join('\n'))

// ---------------------------------------------------------------------------
// Exit
// ---------------------------------------------------------------------------

if (failures > 0) {
  console.error(`\n${failures} assertion(s) FAILED.`)
  process.exit(1)
} else {
  console.log('\nAll checks passed.')
}
