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

export type TabId = typeof TAB_IDS[keyof typeof TAB_IDS]

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

export type CostType = typeof COST_TYPES[keyof typeof COST_TYPES]

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
 * Request timeout (in milliseconds)
 */
export const REQUEST_TIMEOUT = 30000 // 30 seconds

