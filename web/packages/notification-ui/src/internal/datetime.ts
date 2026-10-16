/**
 * Date/time formatting for the notification surface.
 *
 * Timestamps arrive as ISO strings and always render through
 * `Intl.DateTimeFormat` in the current UI language (frontend standards:
 * never format by hand). An unparseable value renders as-is rather than
 * throwing, so one malformed field never blanks the surface.
 */

/** Formats an ISO timestamp for display in `locale` (medium date, short
 * time); a value the engine cannot parse is returned unchanged. */
export function formatDateTime(value: string, locale: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  try {
    return new Intl.DateTimeFormat(locale, {
      dateStyle: 'medium',
      timeStyle: 'short',
    }).format(date)
  } catch {
    return value
  }
}
