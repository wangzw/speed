/**
 * Display formatting for the object page: byte sizes and the download
 * filename.
 *
 * Instants render through @speed/i18n's `formatInstant` in the active
 * language and explicitly in UTC, never the engine's implicit
 * process-local default; the call sites pass the `timeZone: 'UTC'` option
 * because the object view writes every instant in UTC.
 *
 * Sizes are byte counts: the number renders through Intl.NumberFormat
 * (the frontend standard: numbers go through Intl, never a hand-rolled
 * format) and the unit is a bilingual bundle key the caller interpolates,
 * because Intl's built-in byte unit renders an uninflected "1,024 byte"
 * in en-US rather than "1,024 B".
 *
 * The download filename is the object view's stored name when there is
 * one -- the module normalized it once at declaration, so this package
 * renders it as it is -- and otherwise the object id with a conventional
 * extension derived from the recorded MIME, falling back to `.bin` when
 * the MIME maps to none.
 */

const numberFormatters = new Map<string, Intl.NumberFormat>()

function numberFormatterFor(language: string): Intl.NumberFormat {
  const cached = numberFormatters.get(language)
  if (cached !== undefined) {
    return cached
  }
  const formatter = new Intl.NumberFormat(language)
  numberFormatters.set(language, formatter)
  return formatter
}

/** A byte count's number as localized text (grouping per language); the
 * caller interpolates the unit from the bundle. */
export function formatByteNumber(size: number, language: string): string {
  return numberFormatterFor(language).format(size)
}

/**
 * A conventional filename extension for a MIME type: the subtype's last
 * segment, with the `+suffix` stripped and a few common aliases mapped,
 * or '.bin' for a MIME that names none. Not an exhaustive MIME database
 * -- a download names the bytes, and the recorded type is what the server
 * wrote.
 */
export function extensionForMime(mime: string): string {
  const slash = mime.indexOf('/')
  if (slash === -1) {
    return '.bin'
  }
  const subtype = mime.slice(slash + 1).split(';')[0]?.trim().toLowerCase() ?? ''
  const aliases: Record<string, string> = {
    'plain': 'txt',
    'jpeg': 'jpg',
    'svg+xml': 'svg',
    'x-markdown': 'md',
    'x-ndjson': 'ndjson',
  }
  const base = subtype.split('+')[0] ?? subtype
  const name = aliases[subtype] ?? aliases[base] ?? base
  if (name === '' || !/^[a-z0-9]+$/.test(name)) {
    return '.bin'
  }
  return `.${name}`
}

/**
 * The filename for a downloaded object: the stored name when it has one,
 * and otherwise the object id plus a MIME-derived extension. The name is
 * the module's normalized value, not the raw declaration, so it carries no
 * path separator or control character; it is used verbatim.
 */
export function downloadFilename(name: string, id: string, mime: string): string {
  if (name !== '') {
    return name
  }
  return `${id}${extensionForMime(mime)}`
}
