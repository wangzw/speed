/** Formats an instant for the active display zone and language. */
export type FormatInstant = (value: string | number | Date) => string

export const TIME_MARKER = /\uE000time:([^\uE001]+)\uE001/g

/** Replaces every U+E000 `time:<name>` U+E001 marker in `text` with
 * `formatInstant(params[name])`. A missing or non-string value renders
 * empty, an unparseable instant renders as written, and it never throws. */
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

function resolveOne(
  name: string,
  params: Record<string, unknown> | null | undefined,
  formatInstant: FormatInstant,
): string {
  const value = params?.[name]
  if (typeof value === 'number') {
    return formatInstant(value)
  }
  if (typeof value === 'string') {
    if (value.trim() === '') {
      return ''
    }
    const formatted = formatInstant(value)
    return formatted === '' ? value : formatted
  }
  return ''
}
