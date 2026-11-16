/**
 * Wall-clock conversion between a named time zone and an instant.
 *
 * A wall clock is what a person reads on a calendar and clock -- the value
 * a date-time input carries. An instant is the point on the global
 * timeline the API carries. They differ by the zone's UTC offset, which
 * itself moves at daylight-saving transitions, so the conversion is never a
 * fixed subtraction.
 *
 * Both directions are guarded. An unknown zone, an unparseable instant, a
 * non-existent calendar reading or an out-of-range field answers null
 * rather than throwing or silently normalizing. Two transition cases are
 * deliberate:
 *
 * - A reading inside a spring-forward gap (for example 02:30 on a day whose
 *   clocks jump 02:00 to 03:00) denotes no instant, and answers null.
 * - A reading inside a fall-back overlap (an hour the clocks repeat)
 *   denotes two instants. The earlier one is returned by default, so the
 *   answer is deterministic; a caller reading an upper bound can ask for
 *   the later one instead, so a range spans both repeated hours.
 *
 * Resolution is whole-second: `instantToWallClock` drops a sub-second part,
 * and `wallClockToInstant` builds from whole seconds.
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

/** A reading with every field present and in range. */
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

/** How far either side of a reading to probe the zone's offsets: wider
 * than any real UTC offset, narrower than the spacing between two
 * transitions in a zone's history. */
const OFFSET_PROBE_MS = 24 * 60 * 60 * 1000

/** Formatters keyed by zone: the parts read back for one zone never vary. */
const partFormatters = new Map<string, Intl.DateTimeFormat>()

/**
 * The formatter that reads a zone's wall clock. The constructor validates
 * the zone eagerly, so an unknown name throws here and every caller
 * catches it as "no answer". `hourCycle: 'h23'` keeps midnight at 00, not
 * 24.
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

/** Reads the wall-clock fields out of formatted parts, or null when a
 * field is missing or not an integer. */
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

/** The wall-clock reading of an instant in a zone, or null when the zone
 * is unknown or the instant cannot be formatted. */
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

/** The UTC milliseconds a reading denotes when read as UTC. `setUTCFullYear`
 * is used rather than `Date.UTC` because the latter maps years 0-99 onto
 * 1900-1999. Out-of-range fields normalize here and are rejected by the
 * caller's round-trip check. */
function utcMillisFromFields(fields: WholeSecondWallClock): number {
  const date = new Date(0)
  date.setUTCFullYear(fields.year, fields.month - 1, fields.day)
  date.setUTCHours(fields.hour, fields.minute, fields.second, 0)
  return date.getTime()
}

/** The zone's offset at an instant in milliseconds, or null when the zone
 * is unknown. */
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

/** Validates a caller's reading, rejecting fractions, out-of-range fields
 * and calendar overflows such as 31 February. */
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

/**
 * The wall-clock reading of `value` in `timeZone`.
 *
 * Answers null for an empty or unparseable value, an invalid `Date`, `NaN`,
 * or a zone the engine refuses. A sub-second part is dropped, because a
 * wall clock is whole-second.
 */
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
  /**
   * Which occurrence a fall-back overlap denotes. `'earlier'` (the default)
   * is the first occurrence; `'later'` is the second. A range reads its
   * lower bound as the earlier occurrence and its upper bound as the later
   * one, so neither repeated hour falls outside it.
   */
  readonly overlap?: 'earlier' | 'later'
}

/**
 * The instant a wall-clock reading denotes in `timeZone`.
 *
 * Answers null for an out-of-range or impossible reading, a zone the engine
 * refuses, and a reading that falls in a spring-forward gap. A reading in a
 * fall-back overlap denotes two instants and resolves to the earlier one
 * unless `options.overlap` asks for the later one.
 */
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
  // The offsets in force on either side of any transition near the reading.
  // A day either side is wider than any real UTC offset and narrower than
  // the spacing between two transitions in a zone's history.
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
  // A reading denotes one instant per offset that maps back to it. Around a
  // fall-back overlap two offsets do: the first occurrence is built from the
  // larger offset and the second from the smaller one. A spring-forward gap
  // yields none.
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
