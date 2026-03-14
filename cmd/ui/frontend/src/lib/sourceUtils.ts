import type { ProcessedSource, ProcessedMSKSource, ProcessedOSKSource } from '@/types'

/**
 * Type guard to check if a source is MSK
 */
export function isMSKSource(
  source: ProcessedSource
): source is ProcessedSource & { msk_data: ProcessedMSKSource } {
  return source.type === 'msk' && source.msk_data !== undefined
}

/**
 * Type guard to check if a source is OSK
 */
export function isOSKSource(
  source: ProcessedSource
): source is ProcessedSource & { osk_data: ProcessedOSKSource } {
  return source.type === 'osk' && source.osk_data !== undefined
}

/**
 * Get MSK source from sources array (if present)
 */
export function getMSKSource(sources: ProcessedSource[]): ProcessedMSKSource | null {
  const mskSource = sources.find(isMSKSource)
  return mskSource?.msk_data ?? null
}

/**
 * Get OSK source from sources array (if present)
 */
export function getOSKSource(sources: ProcessedSource[]): ProcessedOSKSource | null {
  const oskSource = sources.find(isOSKSource)
  return oskSource?.osk_data ?? null
}
