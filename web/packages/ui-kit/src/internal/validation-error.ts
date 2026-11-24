/**
 * The form family's validation-error text contract: a ui-kit namespace key
 * renders as its translation, anything else renders verbatim.
 */

export interface ValidationErrorLookup {
  /** Key-existence check scoped to the ui-kit namespace. */
  readonly exists: (key: string) => boolean
  /** Translation renderer for the ui-kit namespace in the active language. */
  readonly t: (key: string) => string
}

/**
 * Resolve a field error message to its display text: null for no
 * error, the ui-kit-namespace translation when the message is such a
 * key, the message itself otherwise.
 */
export function resolveValidationError(
  message: string | undefined | null,
  { exists, t }: ValidationErrorLookup,
): string | null {
  if (message === undefined || message === null || message === '') {
    return null
  }
  return exists(message) ? t(message) : message
}
