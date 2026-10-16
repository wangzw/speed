/**
 * Display formatting for the object page: instants, byte sizes and the
 * download filename.
 *
 * Instants arrive as RFC 3339 strings in UTC (the object view writes
 * every instant in UTC) and render through Intl in the current language
 * and explicitly in UTC, never the engine's implicit process-local
 * default; a malformed value renders nothing rather than throwing. The
 * formatter is cached per language so a table of rows formats against one
 * Intl.DateTimeFormat.
 *
 * Sizes are byte counts: the number renders through Intl.NumberFormat
 * (the frontend standard: numbers go through Intl, never a hand-rolled
 * format) and the unit is a bilingual bundle key the caller interpolates,
 * because Intl's built-in byte unit renders an uninflected "1,024 byte"
 * in en-US rather than "1,024 B".
 *
 * The download filename is derived from the object's own fields rather
 * than the declaration's `name`, which the view does not carry: the
 * object id names the file and the recorded MIME supplies a conventional
 * extension, falling back to `.bin` when the MIME maps to none.
 */

const dateFormatters = new Map<string, Intl.DateTimeFormat>()
const numberFormatters = new Map<string, Intl.NumberFormat>()

function dateFormatterFor(language: string): Intl.DateTimeFormat {
  const cached = dateFormatters.get(language)
  if (cached !== undefined) {
    return cached
  }
  const formatter = new Intl.DateTimeFormat(language, {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone: 'UTC',
  })
  dateFormatters.set(language, formatter)
  return formatter
}

function numberFormatterFor(language: string): Intl.NumberFormat {
  const cached = numberFormatters.get(language)
  if (cached !== undefined) {
    return cached
  }
  const formatter = new Intl.NumberFormat(language)
  numberFormatters.set(language, formatter)
  return formatter
}

/**
 * An instant as localized UTC text, or null for an absent or malformed
 * value (an empty string is treated as absent).
 */
export function formatUtc(
  iso: string | undefined,
  language: string,
): string | null {
  if (iso === undefined || iso === '') {
    return null
  }
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) {
    return null
  }
  return dateFormatterFor(language).format(date)
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

/** The filename for a downloaded object: its id plus a MIME-derived extension. */
export function downloadFilename(id: string, mime: string): string {
  return `${id}${extensionForMime(mime)}`
}
