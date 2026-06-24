import { REDACTED_PLACEHOLDER } from '@/constants'

/**
 * Reports whether a connector config still carries the redaction placeholder
 * (`<kcp-redacted>`), meaning the operator must replace it with a real value
 * before applying generated migration assets.
 *
 * Matching is a substring test on the serialized config, which also covers any
 * incidental nesting without a recursive walk. This is intentionally looser than
 * the Go-side exact-equality check (`redact.MapContainsRedacted`): a benign value
 * that merely embeds the placeholder text would trip this banner. That errs in
 * the safe direction — the banner only over-warns, never under-warns — and is
 * acceptable for an advisory notice with no secret/name/key disclosure.
 */
export function hasRedactedConfig(config: unknown): boolean {
  if (config == null) {
    return false
  }
  return JSON.stringify(config).includes(REDACTED_PLACEHOLDER)
}
