/**
 * Common API types shared across all endpoints
 */

/**
 * Metadata structure common to metrics and costs responses
 */
export interface ApiMetadata {
  start_date: string
  end_date: string
  cluster_type?: string
  follower_fetching?: boolean
  tiered_storage?: boolean
  instance_type?: string
  broker_az_distribution?: string
  kafka_version?: string
  enhanced_monitoring?: string
  period?: number // Period in seconds
  broker_type?: 'express' | 'standard'
}

/**
 * Standard API error response
 */
export interface ApiErrorResponse {
  message: string
  error?: string
  status?: number
  details?: Record<string, unknown>
}

