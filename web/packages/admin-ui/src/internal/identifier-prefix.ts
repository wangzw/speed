/** How much of an opaque identifier a table cell shows; the cell carries the full value. */
export const ID_PREFIX_LENGTH = 8

/** The prefix an identifier cell renders; the full value is `cellFullValue`. */
export function identifierPrefix(id: string): string {
  return id.slice(0, ID_PREFIX_LENGTH)
}
