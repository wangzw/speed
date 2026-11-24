/** The maximum length, in bytes, of an email address the rule accepts. */
export const EMAIL_ADDRESS_MAX_BYTES = 254

/**
 * Parses an email address by the authn account rule: trims it, accepts
 * printable ASCII with one `@` and a non-empty part each side, rejects over
 * `EMAIL_ADDRESS_MAX_BYTES` bytes, preserves case; `null` when malformed.
 */
export function parseEmailAddress(value: string): string | null {
  const trimmed = value.trim()
  const bytes = new TextEncoder().encode(trimmed)
  if (bytes.length > EMAIL_ADDRESS_MAX_BYTES) {
    return null
  }
  for (const byte of bytes) {
    if (byte < 0x21 || byte > 0x7e) {
      return null
    }
  }
  const at = trimmed.indexOf('@')
  if (at <= 0 || at === trimmed.length - 1) {
    return null
  }
  if (trimmed.indexOf('@', at + 1) !== -1) {
    return null
  }
  return trimmed
}
