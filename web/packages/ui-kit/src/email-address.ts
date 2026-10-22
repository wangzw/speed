/**
 * The email-address rule shared by the surfaces that accept one as a
 * login identifier: the authn account module's `NormalizeIdentifier`
 * (email kind) stated on the client, so a malformed address is refused
 * in the field it was typed in instead of making a round trip that can
 * only answer the generic bad-request.
 *
 * The rule is the server's: the value is trimmed; the result is printable
 * ASCII (bytes 0x21 to 0x7e, so no space remains inside it); it holds
 * exactly one `@` with a non-empty part on each side; and it is at most
 * 254 bytes. Case is preserved -- the server folds it, and folding a
 * value the viewer is still editing would change what they typed.
 *
 * `parseEmailAddress` returns the trimmed address, so callers submit the
 * same value they validated; `null` means the address is not well formed.
 */
export const EMAIL_ADDRESS_MAX_BYTES = 254

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
