import { REDACTED_PLACEHOLDER } from '@/constants'

/**
 * Reports whether a connector config still carries the redaction placeholder
 * (`<kcp-redacted>`), meaning the operator must replace it with a real value
 * before applying generated migration assets.
 *
 * Connector configs are rendered flat in the UI, but serializing the whole
 * object also catches any incidental nesting, so a recursive walk isn't needed.
 */
export function hasRedactedConfig(config: unknown): boolean {
  if (config == null) {
    return false
  }
  return JSON.stringify(config).includes(REDACTED_PLACEHOLDER)
}
