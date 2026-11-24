/**
 * InlineError: the account surface's binding of ui-kit's failure banner to
 * the account-ui error-text resolver.
 */

import Typography from '@mui/material/Typography'
import { waitCountdownText } from '@speed/i18n'
import { InlineError as SharedInlineError } from '@speed/ui-kit'
import { useAccountUiErrorText } from './error-text.js'
import { useAccountUiTranslation } from './translation.js'

export interface InlineErrorProps {
  /** The code to render, or null for no error (renders nothing). */
  readonly code: string | null
  /**
   * The wait a throttled answer stated, in whole seconds. Given, a countdown
   * line renders outside the live region.
   */
  readonly retryAfterSeconds?: number
  /**
   * The id the countdown line carries, so a control held by the same wait
   * can reach it through `aria-describedby` as its reason.
   */
  readonly countdownId?: string
  /**
   * Text rendered instead of the code's resolved sentence, for a surface
   * that words this particular code for itself. The banner shell, its role
   * and its announcement stay the shared ones.
   */
  readonly message?: string
}

/**
 * The surface's whole-attempt failure banner, one role="alert" per error.
 */
export function InlineError({
  code,
  retryAfterSeconds,
  countdownId,
  message,
}: InlineErrorProps) {
  const resolve = useAccountUiErrorText()
  const { t } = useAccountUiTranslation()
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
      <SharedInlineError
        code={code}
        resolve={message === undefined ? resolve : () => message}
      />
      {countdown === null ? null : (
        <Typography
          id={countdownId}
          variant="body2"
          aria-live="off"
          sx={{ mt: 0.5 }}
        >
          {countdown}
        </Typography>
      )}
    </>
  )
}
