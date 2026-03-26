import type { Region } from '@/types'

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
  regions: Region[]
  schema_registries?: SchemaRegistriesState
  kcp_build_info?: unknown
  timestamp?: string
}

/**
 * State upload request payload
 */
export interface StateUploadRequest {
  regions: Region[]
  schema_registries?: SchemaRegistriesState
  [key: string]: unknown
}

/**
 * State upload API response
 */
export interface StateUploadResponse {
  regions: Region[]
  schema_registries?: SchemaRegistriesState
  message?: string
}

