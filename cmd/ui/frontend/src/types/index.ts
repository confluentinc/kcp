// Shared types for the application
export interface Cluster {
  name: string
  metrics?: {
    metadata: {
      cluster_type: string
      follower_fetching: boolean
      tiered_storage: boolean
      instance_type: string
      broker_az_distribution: string
      kafka_version: string
      enhanced_monitoring: string
      start_window_date: string
      end_window_date: string
      period: number
    }
    results: Array<{
      start: string
      end: string
      label: string
      value: number | null
    }>
  }
  aws_client_information: any
  kafka_admin_client_information: any
  timestamp?: string
}

export interface Region {
  name: string
  configurations?: Array<any>
  costs?: {
    results: Array<any>
    metadata: any
  }
  clusters?: Array<Cluster>
}
