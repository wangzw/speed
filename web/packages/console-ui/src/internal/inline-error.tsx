/**
 * InlineError: the code-level failure banner of the console surface.
 *
 * Every failure path of the console resolves its failure to one error
 * code and renders it through this banner instead of laying out per-field
 * error text (frontend standards: field errors belong to the fields,
 * whole-attempt failures belong to one alert). The banner itself -- the
 * alert shell and the failure classifier -- is ui-kit's shared
 * InlineError / errorCodeOf; this module is the surface's binding of it
 * to the console-ui namespace's error-text resolver, so call sites keep
 * passing a bare code and the resolved text can only ever come from this
 * namespace (whitelisted codes have their own errors-section key,
 * anything else renders the errors.unknown fallback).
 */

import { InlineError as SharedInlineError } from '@speed/ui-kit'
import { useConsoleUiErrorText } from './error-text.js'

export interface InlineErrorProps {
  /** The code to render, or null for no error (renders nothing). */
  readonly code: string | null
  /**
   * The wait a throttled answer stated, in whole seconds. Given, the
   * resolver renders the sentence that names it instead of the code's
   * plain text.
   */
  readonly retryAfterSeconds?: number
}

/** The surface's whole-attempt failure banner, one role="alert" per error. */
export function InlineError({ code, retryAfterSeconds }: InlineErrorProps) {
  const resolve = useConsoleUiErrorText()
  return (
    <SharedInlineError
      code={code}
      resolve={resolve}
      retryAfterSeconds={retryAfterSeconds}
    />
  )
}
