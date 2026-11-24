/**
 * Wall-clock conversion between a named zone and an instant. Both directions
 * answer null for an unknown zone or an unparseable reading; a spring-forward gap
 * denotes no instant, and an overlap resolves earlier unless asked for later.
 */

/** A calendar and clock reading with no zone of its own. */
export interface WallClock {
  /** Proleptic Gregorian year, 1-9999. */
  readonly year: number
  /** Month, 1-12. */
  readonly month: number
  /** Day of month, 1-31. */
  readonly day: number
  /** Hour, 0-23. */
  readonly hour: number
  /** Minute, 0-59. */
  readonly minute: number
  /** Second, 0-59; absent reads as 0. */
  readonly second?: number
}

interface WholeSecondWallClock {
  readonly year: number
  readonly month: number
  readonly day: number
  readonly hour: number
  readonly minute: number
  readonly second: number
}

const MIN_YEAR = 1
const MAX_YEAR = 9999

/** How far either side of a reading to probe the zone's offsets: wider than any UTC offset, narrower than the spacing between two transitions. */
const OFFSET_PROBE_MS = 24 * 60 * 60 * 1000

const partFormatters = new Map<string, Intl.DateTimeFormat>()

/**
 * The formatter that reads a zone's wall clock; the constructor validates the
 * zone eagerly. `hourCycle: 'h23'` keeps midnight at 00, not 24.
 */
function partFormatter(timeZone: string): Intl.DateTimeFormat {
  const cached = partFormatters.get(timeZone)
  if (cached !== undefined) {
    return cached
  }
  const formatter = new Intl.DateTimeFormat('en-US', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  })
  partFormatters.set(timeZone, formatter)
  return formatter
}

function readParts(
  parts: readonly Intl.DateTimeFormatPart[],
): WholeSecondWallClock | null {
  let year: number | undefined
  let month: number | undefined
  let day: number | undefined
  let hour: number | undefined
  let minute: number | undefined
  let second: number | undefined
  for (const part of parts) {
    switch (part.type) {
      case 'year':
        year = Number(part.value)
        break
      case 'month':
        month = Number(part.value)
        break
      case 'day':
        day = Number(part.value)
        break
      case 'hour':
        hour = Number(part.value)
        break
      case 'minute':
        minute = Number(part.value)
        break
      case 'second':
        second = Number(part.value)
        break
      default:
        break
    }
  }
  if (
    year === undefined ||
    month === undefined ||
    day === undefined ||
    hour === undefined ||
    minute === undefined ||
    second === undefined
  ) {
    return null
  }
  if (
    !Number.isInteger(year) ||
    !Number.isInteger(month) ||
    !Number.isInteger(day) ||
    !Number.isInteger(hour) ||
    !Number.isInteger(minute) ||
    !Number.isInteger(second)
  ) {
    return null
  }
  return { year, month, day, hour, minute, second }
}

function wallClockAt(
  instantMs: number,
  timeZone: string,
): WholeSecondWallClock | null {
  try {
    return readParts(partFormatter(timeZone).formatToParts(new Date(instantMs)))
  } catch {
    return null
  }
}

/**
 * The UTC milliseconds a reading denotes when read as UTC. `setUTCFullYear`
 * is used rather than `Date.UTC`, which maps years 0-99 onto 1900-1999.
 */
function utcMillisFromFields(fields: WholeSecondWallClock): number {
  const date = new Date(0)
  date.setUTCFullYear(fields.year, fields.month - 1, fields.day)
  date.setUTCHours(fields.hour, fields.minute, fields.second, 0)
  return date.getTime()
}

function offsetMillis(
  instantMs: number,
  timeZone: string,
): number | null {
  const wall = wallClockAt(instantMs, timeZone)
  if (wall === null) {
    return null
  }
  const asUtc = utcMillisFromFields(wall)
  if (!Number.isFinite(asUtc)) {
    return null
  }
  return asUtc - instantMs
}

function wholeSecondClock(wallClock: WallClock): WholeSecondWallClock | null {
  const { year, month, day, hour, minute } = wallClock
  const second = wallClock.second ?? 0
  if (
    !Number.isInteger(year) ||
    !Number.isInteger(month) ||
    !Number.isInteger(day) ||
    !Number.isInteger(hour) ||
    !Number.isInteger(minute) ||
    !Number.isInteger(second)
  ) {
    return null
  }
  if (
    year < MIN_YEAR ||
    year > MAX_YEAR ||
    month < 1 ||
    month > 12 ||
    day < 1 ||
    day > 31 ||
    hour < 0 ||
    hour > 23 ||
    minute < 0 ||
    minute > 59 ||
    second < 0 ||
    second > 59
  ) {
    return null
  }
  const clock = { year, month, day, hour, minute, second }
  const instantMs = utcMillisFromFields(clock)
  if (!Number.isFinite(instantMs)) {
    return null
  }
  const check = new Date(instantMs)
  if (
    check.getUTCFullYear() !== year ||
    check.getUTCMonth() !== month - 1 ||
    check.getUTCDate() !== day ||
    check.getUTCHours() !== hour ||
    check.getUTCMinutes() !== minute ||
    check.getUTCSeconds() !== second
  ) {
    return null
  }
  return clock
}

function sameClock(a: WholeSecondWallClock, b: WholeSecondWallClock): boolean {
  return (
    a.year === b.year &&
    a.month === b.month &&
    a.day === b.day &&
    a.hour === b.hour &&
    a.minute === b.minute &&
    a.second === b.second
  )
}

/** The wall-clock reading of `value` in `timeZone`, or null for an unparseable value, an invalid Date or an unknown zone. */
export function instantToWallClock(
  value: string | number | Date,
  timeZone: string,
): WallClock | null {
  if (typeof value === 'string' && value.trim() === '') {
    return null
  }
  const date = value instanceof Date ? value : new Date(value)
  const instantMs = date.getTime()
  if (!Number.isFinite(instantMs)) {
    return null
  }
  return wallClockAt(instantMs, timeZone)
}

/** How to resolve a wall-clock reading that denotes more than one instant. */
export interface WallClockToInstantOptions {
  /** Which occurrence a fall-back overlap denotes: `'earlier'` (default) or `'later'`. */
  readonly overlap?: 'earlier' | 'later'
}

/** The instant a wall-clock reading denotes in `timeZone`, or null for an impossible reading or a spring-forward gap. */
export function wallClockToInstant(
  wallClock: WallClock,
  timeZone: string,
  options: WallClockToInstantOptions = {},
): Date | null {
  const clock = wholeSecondClock(wallClock)
  if (clock === null) {
    return null
  }
  const naiveMs = utcMillisFromFields(clock)
  if (!Number.isFinite(naiveMs)) {
    return null
  }
  const offsets = new Set<number>()
  for (const probe of [
    naiveMs - OFFSET_PROBE_MS,
    naiveMs,
    naiveMs + OFFSET_PROBE_MS,
  ]) {
    const offset = offsetMillis(probe, timeZone)
    if (offset === null) {
      return null
    }
    offsets.add(offset)
  }
  // A reading denotes one instant per offset that maps back to it; an overlap yields two and a gap none.
  const preferLater = options.overlap === 'later'
  let chosenMs: number | null = null
  for (const offset of offsets) {
    const candidateMs = naiveMs - offset
    const roundTrip = wallClockAt(candidateMs, timeZone)
    if (roundTrip === null || !sameClock(roundTrip, clock)) {
      continue
    }
    if (
      chosenMs === null ||
      (preferLater ? candidateMs > chosenMs : candidateMs < chosenMs)
    ) {
      chosenMs = candidateMs
    }
  }
  return chosenMs === null ? null : new Date(chosenMs)
}
