/**
 * InlineError: the console's whole-attempt failure banner, bound to the
 * admin-ui namespace's error-text resolver.
 */

import Typography from '@mui/material/Typography'
import { waitCountdownText } from '@speed/i18n'
import { InlineError as SharedInlineError } from '@speed/ui-kit'
import { useAdminUiErrorText } from './error-text.js'
import { useAdminUiTranslation } from './translation.js'

export interface InlineErrorProps {
  /** The code to render, or null for no error (renders nothing). */
  readonly code: string | null
  /**
   * The wait a throttled answer stated, in whole seconds; rendered as a
   * non-live countdown line below the alert.
   */
  readonly retryAfterSeconds?: number
}

/**
 * The whole-attempt failure banner: the code's static sentence, with a throttled
 * answer's countdown below it in a non-live line.
 */
export function InlineError({ code, retryAfterSeconds }: InlineErrorProps) {
  const resolve = useAdminUiErrorText()
  const { t } = useAdminUiTranslation()
  const countdown =
    code === null || retryAfterSeconds === undefined
      ? null
      : waitCountdownText(
          t,
          { short: 'throttle.countdown', long: 'throttle.countdownLong' },
          retryAfterSeconds,
        )
  return (
    <>
      <SharedInlineError code={code} resolve={resolve} />
      {countdown === null ? null : (
        <Typography variant="body2" aria-live="off" sx={{ mt: 0.5 }}>
          {countdown}
        </Typography>
      )}
    </>
  )
}
