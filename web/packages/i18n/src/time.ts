/**
 * Display-timezone resolution and instant formatting.
 *
 * The display-timezone chain, highest tier first: a stored preference the
 * host resolved (the profile's `time_zone` -- empty means "not chosen"),
 * the browser zone, then UTC as the terminal fallback. A stored
 * choice outranks the environment's guess, and the terminal tier closes the
 * chain so a renderer always has an explicit zone -- never the engine's
 * silent process-local default, which would make the same account render
 * differently depending on which machine ran the code.
 *
 * Every Intl construction is guarded. A stored value can be stale or
 * foreign (written against a newer tzdata, hand-edited), and
 * `Intl.DateTimeFormat` throws a RangeError on a zone it does not know: an
 * unguarded constructor would turn one bad value into a rendering crash.
 * A value the engine rejects is skipped exactly like an empty one.
 *
 * This module carries no host state: which stored value a surface hands in
 * (a profile field, a preferences store, a context provider's value) is the
 * host's decision, and the helpers only resolve and format the value they
 * are given. Formatting is the sole use of Intl here; timestamps are never
 * assembled by hand.
 */

/** The terminal tier: the platform default timezone, matching the backend
 * default, so an account with no stored choice and an environment with no
 * answer renders in a defined zone. */
const FALLBACK_TIME_ZONE = 'UTC'

/**
 * Reports whether the engine's Intl accepts `zone` as a timezone name.
 * The probe constructor is the acceptance test: `Intl.DateTimeFormat`
 * validates the zone eagerly (RangeError on an unknown name), so a
 * constructed formatter proves the zone is usable.
 */
export function isSupportedTimeZone(zone: string): boolean {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: zone })
    return true
  } catch {
    return false
  }
}

/**
 * The browser zone's IANA name, or null when the environment cannot
 * answer (an empty answer, an engine that refuses to construct). The read
 * is guarded for the same reason the chain guards its constructors: a
 * resolution failure must degrade to the next tier, never crash a render.
 */
function browserTimeZone(): string | null {
  try {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
    return zone === '' ? null : (zone ?? null)
  } catch {
    return null
  }
}

/**
 * Resolves the display timezone through the three-tier chain: the stored
 * value when present and accepted by the engine, else the browser zone
 * when the environment answers, else UTC. The returned value is always
 * usable as an `Intl.DateTimeFormat` `timeZone` option -- the chain's
 * guards have rejected everything the engine would refuse.
 */
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

/** The date/text shape each style maps to: the Intl style pair per tier. */
const STYLE_OPTIONS = {
  short: { dateStyle: 'short', timeStyle: 'short' },
  medium: { dateStyle: 'medium', timeStyle: 'short' },
  long: { dateStyle: 'medium', timeStyle: 'long' },
} as const

/** The default tier: medium date, short time. */
const DEFAULT_STYLE = 'medium'

/** Options accepted by formatInstant. */
export interface FormatInstantOptions {
  /** IANA zone the instant renders in; absent leaves the engine's default. */
  readonly timeZone?: string
  /** Length tier: short (date and time), medium (default), long (with zone name). */
  readonly style?: 'short' | 'medium' | 'long'
}

/** Formatters keyed by language, style and zone so repeated rows reuse one. */
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

/**
 * A localized rendering of an instant, or the empty string when the value
 * carries no instant (an empty string, a string the engine cannot parse,
 * an invalid Date, NaN) or the engine refuses the language. An unparseable
 * field renders as nothing rather than throwing, so one malformed value
 * never blanks a surface that shows a list of them.
 */
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
