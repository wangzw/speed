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
  readonly code: string | null
  readonly retryAfterSeconds?: number
  readonly countdownId?: string
  readonly message?: string
}

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
