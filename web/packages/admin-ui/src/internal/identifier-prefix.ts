/**
 * How much of an opaque identifier a table cell shows. An identifier
 * column is one of the least important (`low`) columns, so its cell stays
 * narrow; the complete value rides `cellFullValue` -- the cell title and
 * the copy control -- so nothing is lost when the prefix is what fits.
 */
export const ID_PREFIX_LENGTH = 8

/** The prefix an identifier cell renders; the full value is `cellFullValue`. */
export function identifierPrefix(id: string): string {
  return id.slice(0, ID_PREFIX_LENGTH)
}
