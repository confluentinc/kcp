import type { ProcessedSource, ProcessedMSKSource, ProcessedApacheKafkaSource } from '@/types'

/**
 * Type guard to check if a source is MSK
 */
export function isMSKSource(
  source: ProcessedSource
): source is ProcessedSource & { msk_data: ProcessedMSKSource } {
  return source.type === 'msk' && source.msk_data !== undefined
}

/**
 * Type guard to check if a source is Apache Kafka
 */
export function isApacheKafkaSource(
  source: ProcessedSource
): source is ProcessedSource & { apache_kafka_data: ProcessedApacheKafkaSource } {
  return source.type === 'apache-kafka' && source.apache_kafka_data !== undefined
}

/**
 * Get MSK source from sources array (if present)
 */
export function getMSKSource(sources: ProcessedSource[]): ProcessedMSKSource | null {
  const mskSource = sources.find(isMSKSource)
  return mskSource?.msk_data ?? null
}

/**
 * Get Apache Kafka source from sources array (if present)
 */
export function getApacheKafkaSource(sources: ProcessedSource[]): ProcessedApacheKafkaSource | null {
  const apacheKafkaSource = sources.find(isApacheKafkaSource)
  return apacheKafkaSource?.apache_kafka_data ?? null
}
