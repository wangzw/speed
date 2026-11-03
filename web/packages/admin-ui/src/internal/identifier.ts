/**
 * The tenant-identifier rule the roster's inputs mirror.
 *
 * The backend admits a tenant identifier or alias only when it is
 * non-empty and free of blanks and control characters anywhere in it
 * (pkg/tenant's IsIdentifier); anything else is refused with a 400 before
 * the roster is called. The console mirrors the rule so an input can hint
 * at an inadmissible value as it is typed; the server still judges every
 * write, so this is an affordance, not the boundary.
 */

/** Blanks and control characters -- the code points the rule refuses. */
const REJECTED = /[\s\u0000-\u001f\u007f]/

/**
 * Reports whether a value the roster's inputs carry would be refused by the
 * identifier domain: `true` for an empty value or one containing a blank or
 * a control character. An empty value is refused too, but the roster's
 * inputs treat "nothing typed yet" as not-yet-an-error, so the caller gates
 * on emptiness itself before treating this as a violation.
 */
export function isRejectedIdentifier(value: string): boolean {
  return value === '' || REJECTED.test(value)
}
