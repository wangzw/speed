/**
 * InlineError: the code-level failure banner of the sign-in family.
 *
 * Every submit path of the family resolves its failure to one error
 * code and renders it through this banner instead of laying out
 * per-field error text (frontend standards: field errors belong to the
 * fields, whole-attempt failures belong to one alert). The banner
 * itself -- the alert shell and the failure classifier -- is ui-kit's
 * shared InlineError / errorCodeOf; this module is the family's binding
 * of it to the auth-ui namespace's error-text resolver, so call sites
 * keep passing a bare code and the resolved text can only ever come
 * from this namespace (whitelisted codes have their own errors-section
 * key, anything else renders the errors.unknown fallback).
 */

import type { ReactNode } from 'react'
import { InlineError as SharedInlineError } from '@speed/ui-kit'
import { useAuthUiErrorText } from './error-text.js'

export interface InlineErrorProps {
  /** The code to render, or null for no error (renders nothing). */
  readonly code: string | null
  /** The wait a throttled answer stated, in whole seconds. */
  readonly retryAfterSeconds?: number
  /** The wait still remaining, in whole seconds, when the caller counts the
   * stated wait down. Omitted, the visible sentence carries the initial
   * wait unchanged. */
  readonly liveRetryAfterSeconds?: number
  /** Content rendered inside the banner, below the message. */
  readonly detail?: ReactNode
}

/** The family's whole-attempt failure banner, one role="alert" per error. */
export function InlineError({
  code,
  retryAfterSeconds,
  liveRetryAfterSeconds,
  detail,
}: InlineErrorProps) {
  const resolve = useAuthUiErrorText()
  return (
    <SharedInlineError
      code={code}
      resolve={resolve}
      retryAfterSeconds={retryAfterSeconds}
      liveRetryAfterSeconds={liveRetryAfterSeconds}
      detail={detail}
    />
  )
}
