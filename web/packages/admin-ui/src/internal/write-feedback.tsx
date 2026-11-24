/**
 * WriteFeedbackRegion: ui-kit's feedback region bound to the admin-ui error text,
 * with the optional sso/mfa sign-in exits; also the shared failure-text helpers.
 */

import { useEffect, useRef } from 'react'
import type { RefObject } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Typography from '@mui/material/Typography'
import type { SxProps, Theme } from '@mui/material/styles'
import { stepUpReasonOf } from '@speed/auth-core'
import { WriteFeedbackRegion as SharedWriteFeedbackRegion } from '@speed/ui-kit'
import type { WriteOutcome } from '@speed/ui-kit'
import { useAdminAccountHref } from './account-href.js'
import { useAdminUiErrorText } from './error-text.js'
import { useNoMfaFactor } from './mfa-refusal.js'
import { AdminLink } from './navigation.js'
import { useAdminUiTranslation } from './translation.js'

export interface WriteFeedbackRegionProps {
  /** The scope's slot; `null` renders no visible feedback. */
  readonly outcome: WriteOutcome | null
  /** Styles for the region's root container; spacing adjustments. */
  readonly sx?: SxProps<Theme>
  /**
   * The re-login exit for the tenant admission layer's SSO refusal; rendered
   * under the resolved sentence for `authn.sso_required` and nothing else.
   */
  readonly ssoRelogin?: () => void
  /**
   * The sign-in or enrollment exit for the multi-factor refusal: with an
   * enabled factor it renders this sign-in exit; with none it names the
   * missing two-step verification and points at the host's account settings.
   */
  readonly mfaRelogin?: () => void
}

/** The console's feedback region: one scope's slot rendered with its resolver. */
export function WriteFeedbackRegion({
  outcome,
  sx,
  ssoRelogin,
  mfaRelogin,
}: WriteFeedbackRegionProps) {
  const resolve = useAdminUiErrorText()
  const { t } = useAdminUiTranslation()
  const accountHref = useAdminAccountHref()
  const ssoRefused =
    outcome !== null &&
    outcome.kind === 'failure' &&
    stepUpReasonOf(outcome.code) === 'sso'
  const mfaRefused =
    outcome !== null &&
    outcome.kind === 'failure' &&
    mfaRelogin !== undefined &&
    stepUpReasonOf(outcome.code) === 'mfa'
  const noMfaFactor = useNoMfaFactor(mfaRefused)
  // With no factor the code's own sentence tells the operator to sign in
  // again, which would meet the same refusal; the alert carries the
  // enrollment guidance instead.
  const shownResolve = noMfaFactor
    ? (code: string) =>
        stepUpReasonOf(code) === 'mfa'
          ? t('stepUp.mfaEnrollRequired')
          : resolve(code)
    : resolve
  return (
    <>
      <SharedWriteFeedbackRegion
        outcome={outcome}
        resolve={shownResolve}
        sx={sx}
      />
      {ssoRefused && ssoRelogin !== undefined && (
        <Box sx={{ mt: 1 }}>
          <Button type="button" variant="contained" onClick={ssoRelogin}>
            {t('stepUp.relogin')}
          </Button>
        </Box>
      )}
      {mfaRefused && !noMfaFactor && (
        <Box sx={{ mt: 1 }}>
          <Button type="button" variant="contained" onClick={mfaRelogin}>
            {t('stepUp.relogin')}
          </Button>
        </Box>
      )}
      {mfaRefused && noMfaFactor && accountHref !== undefined && (
        <Box sx={{ mt: 1 }}>
          <Typography variant="body2">
            <AdminLink href={accountHref}>{t('stepUp.goToAccount')}</AdminLink>
          </Typography>
        </Box>
      )}
    </>
  )
}

/** The text a scope's failure resolves to, or null when the slot holds no failure. */
export function failureTextOf(
  outcome: WriteOutcome | null,
  resolve: (code: string) => string,
): string | null {
  return outcome !== null && outcome.kind === 'failure'
    ? resolve(outcome.code)
    : null
}

/**
 * A ref that brings a scope's region into view when it settles a failure,
 * since the region may sit below a tall table; success and a cleared slot
 * never move the page, and reduced motion jumps instantly.
 */
export function useFailureScroll(
  outcome: WriteOutcome | null,
): RefObject<HTMLDivElement | null> {
  const ref = useRef<HTMLDivElement | null>(null)
  useEffect(() => {
    if (outcome === null || outcome.kind !== 'failure') {
      return
    }
    const region = ref.current
    if (region === null) {
      return
    }
    const reduceMotion =
      window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
    region.scrollIntoView?.({
      behavior: reduceMotion ? 'auto' : 'smooth',
      block: 'center',
    })
  }, [outcome])
  return ref
}
