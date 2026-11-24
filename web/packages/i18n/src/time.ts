/**
 * Display-timezone resolution (stored value, browser zone, UTC) and instant
 * formatting through Intl. Every Intl construction is guarded, and the module
 * carries no host state.
 */

/** The terminal tier: the platform default timezone, matching the backend default. */
const FALLBACK_TIME_ZONE = 'UTC'

/** Whether Intl accepts `zone` as a timezone name; the probe constructor is the acceptance test. */
export function isSupportedTimeZone(zone: string): boolean {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: zone })
    return true
  } catch {
    return false
  }
}

function browserTimeZone(): string | null {
  try {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
    return zone === '' ? null : (zone ?? null)
  } catch {
    return null
  }
}

/** Resolves the display timezone through stored value, browser zone, then UTC; the result is always usable. */
export function resolveTimeZone(stored?: string | null): string {
  const preferred = stored?.trim() ?? ''
  if (preferred !== '' && isSupportedTimeZone(preferred)) {
    return preferred
  }
  const browserZone = browserTimeZone()
  if (browserZone !== null && isSupportedTimeZone(browserZone)) {
    return browserZone
  }
  return FALLBACK_TIME_ZONE
}

const STYLE_OPTIONS = {
  short: { dateStyle: 'short', timeStyle: 'short' },
  medium: { dateStyle: 'medium', timeStyle: 'short' },
  long: { dateStyle: 'medium', timeStyle: 'long' },
} as const

const DEFAULT_STYLE = 'medium'

/** Options accepted by formatInstant. */
export interface FormatInstantOptions {
  /** IANA zone the instant renders in; absent leaves the engine's default. */
  readonly timeZone?: string
  /** Length tier: short, medium (default), or long (with zone name). */
  readonly style?: 'short' | 'medium' | 'long'
}

const formatters = new Map<string, Intl.DateTimeFormat>()

function formatterFor(
  language: string,
  style: 'short' | 'medium' | 'long',
  timeZone: string | undefined,
): Intl.DateTimeFormat {
  const cacheKey = `${language}\u0000${style}\u0000${timeZone ?? ''}`
  const cached = formatters.get(cacheKey)
  if (cached !== undefined) {
    return cached
  }
  const formatter = new Intl.DateTimeFormat(language, {
    ...STYLE_OPTIONS[style],
    ...(timeZone === undefined ? {} : { timeZone }),
  })
  formatters.set(cacheKey, formatter)
  return formatter
}

/** A localized rendering of an instant, or the empty string when the value carries no instant or the engine refuses the language. */
export function formatInstant(
  value: string | number | Date,
  language: string,
  options: FormatInstantOptions = {},
): string {
  if (typeof value === 'string' && value.trim() === '') {
    return ''
  }
  const date = value instanceof Date ? value : new Date(value)
  const instant = date.getTime()
  if (Number.isNaN(instant)) {
    return ''
  }
  try {
    return formatterFor(language, options.style ?? DEFAULT_STYLE, options.timeZone).format(
      date,
    )
  } catch {
    return ''
  }
}
