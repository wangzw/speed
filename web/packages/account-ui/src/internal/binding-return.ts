/**
 * The binding round trip's return seam.
 *
 * A social-account binding starts from an account page, but the provider's
 * round trip lands the browser on the host's callback route, which holds no
 * knowledge of the page it started from. The link flow therefore carries
 * that page inside its top-level `return_to`: the callback route plus a
 * `return` query parameter naming the source page. The server stores the
 * whole value in the flow record and lands the browser on it, so the
 * callback stays the one page that completes the flow and the source page
 * is recoverable from the URL there.
 *
 * Only a site-internal relative path is ever carried. A value that names
 * another origin (an absolute URL, a scheme-relative "//host") is dropped
 * rather than sent, and the callback's reader applies the same rule: a
 * missing or unusable target is the caller's safe landing, never a
 * navigation somewhere the flow did not start. The rule mirrors the
 * server's own relative-return-target check (pkg/authn/login): a single
 * leading slash, no scheme-relative "//", no backslash, no control
 * character, no "@" and at most 2048 bytes.
 */

/** The query parameter that carries the source page inside `return_to`. */
export const RETURN_PARAM = 'return'

/** The most a return target may be, in bytes, mirroring the server's bound. */
const MAX_TARGET_BYTES = 2048

const ABSOLUTE_URL_PATTERN = /^[a-zA-Z][a-zA-Z0-9+.-]*:/

/** The UTF-8 byte length of a string, without allocating an encoder. */
function byteLength(value: string): number {
  let bytes = 0
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index)
    if (code < 0x80) {
      bytes += 1
    } else if (code < 0x800) {
      bytes += 2
    } else if (code >= 0xd800 && code <= 0xdbff) {
      // A surrogate pair encodes as four bytes; count it once for the pair.
      bytes += 4
      index += 1
    } else {
      bytes += 3
    }
  }
  return bytes
}

/**
 * Whether the value is a well-formed site-internal relative path. A value
 * naming an origin -- an absolute URL, or a scheme-relative "//host" -- is
 * never one, so it cannot send the callback to another site.
 */
export function isSitePath(value: string): boolean {
  if (!value.startsWith('/') || value.startsWith('//')) {
    return false
  }
  if (byteLength(value) > MAX_TARGET_BYTES) {
    return false
  }
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index)
    if (code < 0x20 || code === 0x7f) {
      return false
    }
    const char = value[index]
    if (char === '\\' || char === '@') {
      return false
    }
  }
  return true
}

/**
 * The link flow's top-level `return_to`: the host's callback route with the
 * source page appended as `return`, when the source is a site-internal
 * path. Without one the callback route is sent unchanged, so the callback
 * falls back to its own safe landing.
 */
export function withBindingReturn(
  callback: string,
  source: string | undefined,
): string {
  if (source === undefined || !isSitePath(source)) {
    return callback
  }
  if (ABSOLUTE_URL_PATTERN.test(callback)) {
    const url = new URL(callback)
    url.searchParams.set(RETURN_PARAM, source)
    return url.toString()
  }
  const separator = callback.includes('?') ? '&' : '?'
  return `${callback}${separator}${RETURN_PARAM}=${encodeURIComponent(source)}`
}

/**
 * The source page a callback route carries, when the query names a
 * site-internal path. A missing or unusable value reads as undefined, and
 * the caller uses its own safe landing instead.
 */
export function bindingReturnTarget(search: string): string | undefined {
  const value = new URLSearchParams(search).get(RETURN_PARAM)
  return value !== null && isSitePath(value) ? value : undefined
}
