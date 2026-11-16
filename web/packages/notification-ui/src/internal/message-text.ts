/**
 * The one place a message's server-written text is resolved for display.
 *
 * The delivery pipeline does not format an in-app time segment. It writes a
 * marker naming the parameter — U+E000, `time:`, the name, U+E001 — and the
 * client resolves it from the message's `params` in the viewer's display zone
 * and language. The marker carries no instant, layout or zone, so a viewer who
 * changes either sees an old message re-rendered rather than a moment frozen at
 * delivery time.
 *
 * `resolveMessageText` is pure: it takes the formatter as a parameter instead
 * of reading the `useFormatInstant` hook, so it can be tested on its own and
 * called from more than one place (the row, and the mark-read confirmation's
 * message title). A missing parameter, `null`, a boolean, an object, an array
 * and an empty string all resolve to the empty string; a string the engine
 * cannot parse is shown as it was written, the same rule the message's own
 * received-time field follows. It never throws and never leaves a marker
 * character in its result.
 *
 * A private-use character that is not part of a `time:` marker is untouched:
 * only the full marker pattern is replaced.
 */

/** Format an instant for the active display zone and language. */
export type FormatInstant = (value: string | number | Date) => string

/**
 * The marker a marked render writes: U+E000 `time:` <parameter name> U+E001.
 * The name runs to the closing delimiter and is non-empty.
 */
export const TIME_MARKER = /\uE000time:([^\uE001]+)\uE001/g

/**
 * Resolve every time marker in `text` against `params`, using
 * `formatInstant`.
 */
export function resolveMessageText(
  text: string,
  params: Record<string, unknown> | null | undefined,
  formatInstant: FormatInstant,
): string {
  if (text === '' || !text.includes('\uE000')) {
    return text
  }
  return text.replace(TIME_MARKER, (_match, name: string) =>
    resolveOne(name, params, formatInstant),
  )
}

/** Resolve one marker's parameter value. */
function resolveOne(
  name: string,
  params: Record<string, unknown> | null | undefined,
  formatInstant: FormatInstant,
): string {
  const value = params?.[name]
  if (typeof value === 'number') {
    // A number is an instant in milliseconds; the formatter answers the empty
    // string for a value that carries none, which is the degradation the
    // contract asks for.
    return formatInstant(value)
  }
  if (typeof value === 'string') {
    if (value.trim() === '') {
      return ''
    }
    const formatted = formatInstant(value)
    // A string the engine cannot parse is shown as written, matching the
    // received-time field's rule.
    return formatted === '' ? value : formatted
  }
  return ''
}
