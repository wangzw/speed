/**
 * Timestamp rendering for the console surfaces.
 *
 * The administration views carry instants as RFC 3339 strings in UTC. They
 * render through Intl in the current language and explicitly in UTC (the
 * zone the views carry), never the engine's implicit process-local
 * default; a malformed value renders nothing rather than throwing. The
 * formatter is cached per language so a table of rows formats against one
 * Intl.DateTimeFormat.
 *
 * The empty-string sentinel some views use for "no instant" (the zero
 * instant, or an absent field) renders as an empty cell, not the epoch:
 * the callers pass `undefined` for it, and a real empty string is treated
 * the same way.
 */

const formatters = new Map<string, Intl.DateTimeFormat>()
const longFormatters = new Map<string, Intl.DateTimeFormat>()

function formatterFor(language: string): Intl.DateTimeFormat {
  const cached = formatters.get(language)
  if (cached !== undefined) {
    return cached
  }
  const formatter = new Intl.DateTimeFormat(language, {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone: 'UTC',
  })
  formatters.set(language, formatter)
  return formatter
}

/**
 * A millisecond instant as localized UTC text with seconds and the zone
 * name. The refresh label uses this form: seconds make two consecutive
 * refreshes distinguishable, and the date keeps a page that sat open
 * overnight readable. It is deliberately not the short `formatUtc` form,
 * which carries neither seconds nor the zone name.
 */
export function formatUtcLong(
  instant: number,
  language: string,
): string {
  let formatter = longFormatters.get(language)
  if (formatter === undefined) {
    formatter = new Intl.DateTimeFormat(language, {
      dateStyle: 'medium',
      timeStyle: 'long',
      timeZone: 'UTC',
    })
    longFormatters.set(language, formatter)
  }
  return formatter.format(new Date(instant))
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
  return formatterFor(language).format(date)
}

/**
 * Whether an instant is the zero instant (0001-01-01T00:00:00Z), the
 * sentinel the administration views use for "never" (an impersonation
 * still in force, an invitation not yet accepted). The literal the Go
 * time package writes for the zero time is matched directly, so no
 * timezone conversion can mistake it for a real instant.
 */
export function isZeroInstant(iso: string | undefined): boolean {
  return iso === undefined || iso === '' || iso.startsWith('0001-01-01')
}
