import type { MetricsApiResponse } from '@/types/api/metrics'

/**
 * scopeConnectMetricsToConnector narrows an MSK-managed Connect metrics response
 * to a single connector.
 *
 * MSK-managed metrics arrive as one response whose series are labeled
 * `"<metric> (<connectorName>)"` (e.g. `"incoming-byte-rate (my-connector)"`).
 * This filters `results`, `aggregates`, and `query_info` to the named connector
 * and strips the ` (connectorName)` suffix so downstream views (chart/table/query,
 * JSON/CSV) show bare metric names. `metadata` is left unchanged. An unknown
 * connector yields empty `results`/`aggregates`.
 */
export function scopeConnectMetricsToConnector(
  response: MetricsApiResponse,
  connectorName: string
): MetricsApiResponse {
  const suffix = ` (${connectorName})`
  const strip = (s: string): string => (s.endsWith(suffix) ? s.slice(0, -suffix.length) : s)

  const results = response.results
    .filter((r) => r.label.endsWith(suffix))
    .map((r) => ({ ...r, label: strip(r.label) }))

  let aggregates: MetricsApiResponse['aggregates']
  if (response.aggregates) {
    aggregates = {}
    for (const [key, value] of Object.entries(response.aggregates)) {
      if (key.endsWith(suffix)) {
        aggregates[strip(key)] = value
      }
    }
  }

  const query_info = response.query_info
    ?.filter((q) => q.metric_name.endsWith(suffix))
    .map((q) => ({ ...q, metric_name: strip(q.metric_name) }))

  return { ...response, results, aggregates, query_info }
}
