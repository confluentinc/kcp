import type { Region } from '@/types'
import type { ProcessedOSKSource } from '@/types/osk'

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
 * Processed Source (discriminated union)
 */
export interface ProcessedSource {
  type: SourceType
  msk_data?: ProcessedMSKSource
  osk_data?: ProcessedOSKSource
}

/**
 * Confluent Schema Registry structure
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
 * AWS Glue Schema Registry structure
 */
export interface GlueSchemaVersion {
  schema_definition: string
  data_format: string
  version_number: number
  status: string
  created_date: string
}

export interface GlueSchema {
  schema_name: string
  schema_arn: string
  data_format: string
  versions: GlueSchemaVersion[]
  latest_version: GlueSchemaVersion | null
}

export interface GlueSchemaRegistry {
  registry_name: string
  registry_arn: string
  region: string
  schemas: GlueSchema[]
}

/**
 * Schema registries state organized by type
 */
export interface SchemaRegistriesState {
  confluent_schema_registry?: SchemaRegistry[]
  aws_glue?: GlueSchemaRegistry[]
}

/**
 * Processed state structure from backend
 */
export interface ProcessedState {
  sources: ProcessedSource[]
  schema_registries?: SchemaRegistriesState
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
