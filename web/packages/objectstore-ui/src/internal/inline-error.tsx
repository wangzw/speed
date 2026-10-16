/**
 * InlineError: the code-level failure banner of the object page.
 *
 * Every failure path of the page resolves its failure to one error code
 * and renders it through this banner instead of laying out per-action
 * error text. The banner itself -- the alert shell and the failure
 * classifier -- is ui-kit's shared InlineError / errorCodeOf; this module
 * is the page's binding of it to the objectstore-ui namespace's
 * error-text resolver, so call sites keep passing a bare code and the
 * resolved text can only ever come from this namespace (whitelisted
 * codes have their own errors-section leaf, anything else renders the
 * errors.unknown fallback).
 */

import { InlineError as SharedInlineError } from '@speed/ui-kit'
import { useObjectstoreUiErrorText } from './error-text.js'

export interface InlineErrorProps {
  /** The code to render, or null for no error (renders nothing). */
  readonly code: string | null
}

/** The page's whole-attempt failure banner, one role="alert" per error. */
export function InlineError({ code }: InlineErrorProps) {
  const resolve = useObjectstoreUiErrorText()
  return <SharedInlineError code={code} resolve={resolve} />
}
