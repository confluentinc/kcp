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
 * Metrics API response structure
 */
export interface MetricsApiResponse {
  metadata: ApiMetadata
  results: MetricResult[]
  aggregates?: MetricAggregates
}

/**
 * Query parameters for metrics API
 */
export interface MetricsQueryParams {
  startDate?: string | Date
  endDate?: string | Date
}

