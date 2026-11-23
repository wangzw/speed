/**
 * InlineError: the code-level failure banner of the account surface.
 *
 * Every failure path of the surface resolves its failure to one error
 * code and renders it through this banner instead of laying out per-field
 * error text (frontend standards: field errors belong to the fields,
 * whole-attempt failures belong to one alert). The banner itself -- the
 * alert shell and the failure classifier -- is ui-kit's shared
 * InlineError / errorCodeOf; this module is the surface's binding of it
 * to the account-ui namespace's error-text resolver, so call sites keep
 * passing a bare code and the resolved text can only ever come from this
 * namespace (whitelisted codes have their own errors-section key,
 * anything else renders the errors.unknown fallback).
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
   * The wait a throttled answer stated, in whole seconds. Given, the banner
   * renders the code's static sentence and, outside the live region, a
   * countdown line that changes with the wait.
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

/** The surface's whole-attempt failure banner, one role="alert" per error.
 *
 * The alert carries only the code's static sentence. A throttled answer's
 * remaining wait changes every tick, so it renders below the alert in a
 * non-live line: announcing it would make the screen reader repeat the whole
 * banner once per second.
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
