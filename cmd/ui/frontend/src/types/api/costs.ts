import type { ApiMetadata } from './common'

/**
 * Cost value structure - can contain multiple cost types
 */
export interface CostValues {
  unblended_cost?: number
  blended_cost?: number
  amortized_cost?: number
  net_amortized_cost?: number
  net_unblended_cost?: number
  usage_quantity?: number
  [key: string]: number | undefined
}

/**
 * Individual cost result from costs API
 */
export interface CostResult {
  start: string
  end: string
  service: string
  usage_type: string
  values: CostValues
}

/**
 * Usage type aggregate structure
 */
export interface UsageTypeAggregate {
  sum?: number
  avg?: number
  max?: number
  min?: number
}

/**
 * Cost type aggregates nested structure:
 * service -> cost_type -> usage_type -> aggregate
 */
export interface CostTypeAggregates {
  total?: number // Service-level total
  [usageType: string]: UsageTypeAggregate | number | undefined
}

/**
 * Service aggregates nested structure:
 * service -> cost_type -> cost type aggregates
 */
export interface ServiceAggregates {
  [costType: string]: CostTypeAggregates
}

/**
 * Cost aggregates structure:
 * service -> cost_type -> usage_type -> aggregate
 */
export interface CostAggregates {
  [service: string]: ServiceAggregates
}

/**
 * Costs API response structure
 */
export interface CostsApiResponse {
  metadata: ApiMetadata
  results: CostResult[]
  aggregates?: CostAggregates
}

/**
 * Query parameters for costs API
 */
export interface CostsQueryParams {
  startDate?: string | Date
  endDate?: string | Date
}

