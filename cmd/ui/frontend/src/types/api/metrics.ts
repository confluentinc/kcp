import type { ApiMetadata } from './common'

/**
 * Individual metric result from metrics API
 */
export interface MetricResult {
  start: string
  end: string
  label: string
  value: number | null
}

/**
 * Metric aggregates (min, avg, max) per metric name
 */
export interface MetricAggregates {
  [metricName: string]: {
    min?: number
    avg?: number
    max?: number
  }
}

/**
 * Query info for an individual metric (CloudWatch, Jolokia, or Prometheus)
 */
export interface MetricQueryInfo {
  metric_name: string
  source_type?: 'cloudwatch' | 'jolokia' | 'prometheus'

  // CloudWatch fields
  namespace?: string
  dimensions?: string
  statistic?: string
  period?: number
  search_expression?: string
  math_expression?: string
  aws_cli_command?: string
  console_source_json?: string

  // Jolokia fields
  mbean_path?: string
  jolokia_url?: string

  // Prometheus fields
  promql_query?: string
  prometheus_url?: string
  prometheus_metric_name?: string

  // Shared fields
  curl_command?: string
  query_duration?: string
  aggregation_note: string
}

/**
 * Metrics API response structure
 */
export interface MetricsApiResponse {
  metadata: ApiMetadata
  results: MetricResult[]
  aggregates?: MetricAggregates
  query_info?: MetricQueryInfo[]
}

/**
 * Query parameters for metrics API
 */
export interface MetricsQueryParams {
  startDate?: string | Date
  endDate?: string | Date
}

