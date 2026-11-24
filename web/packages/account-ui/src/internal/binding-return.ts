/**
 * The binding round trip's return seam: the source page travels inside the
 * link flow's top-level `return_to` as a `return` query parameter.
 */

/** The query parameter that carries the source page inside `return_to`. */
export const RETURN_PARAM = 'return'

// Mirrors the server's relative-return-target bound (pkg/authn/login).
const MAX_TARGET_BYTES = 2048

const ABSOLUTE_URL_PATTERN = /^[a-zA-Z][a-zA-Z0-9+.-]*:/

// Only the parsed path, query and fragment are read back, so this origin
// never reaches the returned value.
const RELATIVE_ORIGIN = 'http://site.invalid'

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
 * Whether the value is a well-formed site-internal relative path. An
 * absolute URL or a scheme-relative "//host" is never one.
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
 * The link flow's top-level `return_to`: the callback route with the source
 * page appended as `return` when the source is a site-internal path, else
 * the route unchanged.
 */
export function withBindingReturn(
  callback: string,
  source: string | undefined,
): string {
  if (source === undefined || !isSitePath(source)) {
    return callback
  }
  const absolute = ABSOLUTE_URL_PATTERN.test(callback)
  const relativeRoute =
    callback.startsWith('/') && !callback.startsWith('//')
  if (absolute || relativeRoute) {
    // Parse both forms so a fragment stays after the query: appending
    // inline would place the parameter after the `#`.
    const url = new URL(callback, RELATIVE_ORIGIN)
    url.searchParams.set(RETURN_PARAM, source)
    return absolute
      ? url.toString()
      : `${url.pathname}${url.search}${url.hash}`
  }
  const separator = callback.includes('?') ? '&' : '?'
  return `${callback}${separator}${RETURN_PARAM}=${encodeURIComponent(source)}`
}

/**
 * The source page a callback route carries, when the query names a
 * site-internal path. A missing or unusable value reads as undefined.
 */
export function bindingReturnTarget(search: string): string | undefined {
  const value = new URLSearchParams(search).get(RETURN_PARAM)
  return value !== null && isSitePath(value) ? value : undefined
}
