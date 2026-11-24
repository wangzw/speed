/** The auth-ui binding of ui-kit's InlineError to the namespace's error-text
 * resolver. */

import type { ReactNode } from 'react'
import { InlineError as SharedInlineError } from '@speed/ui-kit'
import { useAuthUiErrorText } from './error-text.js'

export interface InlineErrorProps {
  readonly code: string | null
  readonly retryAfterSeconds?: number
  /** The wait still remaining when the caller counts the stated wait down.
   * Omitted, the sentence carries the initial wait unchanged. */
  readonly liveRetryAfterSeconds?: number
  readonly detail?: ReactNode
}

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
