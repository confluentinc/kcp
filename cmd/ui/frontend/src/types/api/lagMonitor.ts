/**
 * Lag monitor API types
 */

export interface MirrorLag {
  partition: number
  lag: number
  last_source_fetch_offset: number
}

export interface MirrorTopic {
  mirror_topic_name: string
  mirror_status: string
  mirror_lags: MirrorLag[]
}

export type LagMonitorApiResponse = MirrorTopic[]

export interface LagMonitorConfig {
  rest_endpoint: string
  cluster_id: string
  cluster_link_name: string
}
