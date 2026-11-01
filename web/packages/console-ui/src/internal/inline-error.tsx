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

import Typography from '@mui/material/Typography'
import { waitCountdownText } from '@speed/i18n'
import { InlineError as SharedInlineError } from '@speed/ui-kit'
import { useConsoleUiErrorText } from './error-text.js'
import { useConsoleUiTranslation } from './translation.js'

export interface InlineErrorProps {
  /** The code to render, or null for no error (renders nothing). */
  readonly code: string | null
  /**
   * The wait a throttled answer stated, in whole seconds. Given, the banner
   * renders the code's static sentence and, outside the live region, a
   * countdown line that changes with the wait.
   */
  readonly retryAfterSeconds?: number
}

/** The surface's whole-attempt failure banner, one role="alert" per error.
 *
 * The alert carries only the code's static sentence. A throttled answer's
 * remaining wait changes every tick, so it renders below the alert in a
 * non-live line: announcing it would make the screen reader repeat the whole
 * banner once per second.
 */
export function InlineError({ code, retryAfterSeconds }: InlineErrorProps) {
  const resolve = useConsoleUiErrorText()
  const { t } = useConsoleUiTranslation()
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
