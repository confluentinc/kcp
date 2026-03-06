import type { Region } from '@/types'
import type { ProcessedOSKCluster } from '@/types/osk'

/**
 * Source type discriminator
 */
export type SourceType = 'msk' | 'osk'

/**
 * Processed MSK Source (contains regions)
 */
export interface ProcessedMSKSource {
  regions: Region[]
}

/**
 * Processed OSK Source (contains OSK clusters)
 */
export interface ProcessedOSKSource {
  clusters: ProcessedOSKCluster[]
}

/**
 * Processed Source (discriminated union)
 */
export interface ProcessedSource {
  type: SourceType
  msk_data?: ProcessedMSKSource
  osk_data?: ProcessedOSKSource
}

/**
 * Schema Registry structure
 */
export interface SchemaRegistry {
  type: string
  url: string
  default_compatibility: string
  contexts: Array<string>
  subjects: Array<{
    name: string
    schema_type: string
    versions: Array<{
      schema: string
      id: number
      subject: string
      version: number
      schemaType?: string
    }>
    latest_schema: {
      schema: string
      id: number
      subject: string
      version: number
      schemaType?: string
    }
  }>
}

/**
 * Processed state structure from backend
 */
export interface ProcessedState {
  sources: ProcessedSource[]
  schema_registries?: SchemaRegistry[]
  kcp_build_info?: unknown
  timestamp?: string
}

/**
 * State upload request payload
 */
export interface StateUploadRequest {
  msk_sources?: {
    regions: Region[]
  }
  osk_sources?: {
    clusters: any[] // Will be processed by backend
  }
  schema_registries?: SchemaRegistry[]
  kcp_build_info?: unknown
  timestamp?: string
}

/**
 * State upload API response (same as ProcessedState)
 */
export interface StateUploadResponse {
  sources: ProcessedSource[]
  schema_registries?: SchemaRegistry[]
  kcp_build_info?: unknown
  timestamp?: string
  message?: string
}

