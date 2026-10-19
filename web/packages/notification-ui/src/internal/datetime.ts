/**
 * Date/time formatting for the notification surface.
 *
 * Timestamps arrive as ISO strings and render through the shared
 * @speed/i18n `formatInstant` in the current UI language and the host's
 * display time zone, never by hand. `resolveTimeZone` applies the
 * platform's tier chain: the host's zone when it supplied one and the
 * engine accepts it, the device's own zone otherwise, UTC as the terminal
 * fallback. A value the engine cannot parse renders as-is rather than
 * throwing, so one malformed field never blanks a surface of rows.
 */

import { formatInstant, resolveTimeZone } from '@speed/i18n'

/** Formats an ISO timestamp for display in `locale` and `timeZone`
 * (medium date, short time). An absent `timeZone` falls back to the
 * device's zone; a value the engine cannot parse is returned unchanged. */
export function formatDateTime(
  value: string,
  locale: string,
  timeZone?: string,
): string {
  const formatted = formatInstant(value, locale, {
    timeZone: resolveTimeZone(timeZone),
  })
  return formatted === '' && value.trim() !== '' ? value : formatted
}
