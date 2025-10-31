import type { Region } from '@/types'

/**
 * Schema Registry structure
 */
export interface SchemaRegistry {
  type: string
  url: string
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
 * State upload request payload
 */
export interface StateUploadRequest {
  regions: Region[]
  schema_registries?: SchemaRegistry[]
  [key: string]: unknown
}

/**
 * State upload API response
 */
export interface StateUploadResponse {
  regions: Region[]
  schema_registries?: SchemaRegistry[]
  message?: string
}

