/**
 * WriteFeedbackRegion: the console's binding of ui-kit's feedback region.
 *
 * A console page reports one control group's writes through one
 * `useWriteFeedback` slot and renders this region directly under the
 * controls. The region itself -- the success notice always mounted as the
 * polite live region, and the failure alert mounted only for a failure --
 * is ui-kit's shared `WriteFeedbackRegion`. This module is the surface's
 * binding of it to the admin-ui namespace's error-text resolver, so call
 * sites pass the slot and nothing else: a failure code resolves only
 * through the console whitelist, and a code outside it renders the shared
 * `errors.unknown` fallback instead of a raw key.
 *
 * The module also carries the two pieces every page's scopes share: the
 * resolved text of a slot's failure, so a row's inline marker and the
 * region render the one message through the one resolver, and a ref that
 * brings a region into view when its scope settles a failure, so a failure
 * reported below a long table is still seen.
 *
 * A surface that offers a sign-in exit passes `ssoRelogin` and
 * `mfaRelogin`. When the slot settles the tenant admission layer's
 * `authn.sso_required` refusal, the first renders the region's exit under
 * the resolved sentence. When it settles the layer's
 * `authn.insufficient_user_authentication` refusal, the second splits on
 * the account's own factors (internal/mfa-refusal reads
 * GET /api/v1/auth/factors): with an enabled factor the region renders the
 * sign-in exit, because a new sign-in completes the second factor; with
 * none it replaces the code's sentence with the missing two-step
 * verification and points at the enrollment surface through the host's
 * account-settings destination. Every other code keeps its existing
 * rendering, and so does a surface that offers neither exit.
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
   * The re-login exit for the tenant admission layer's SSO refusal. When
   * the scope settles the `authn.sso_required` failure, the region renders
   * this action under the resolved sentence; every other code keeps its
   * existing rendering. Omit it on a surface with no sign-in exit of its
   * own.
   */
  readonly ssoRelogin?: () => void
  /**
   * The sign-in or enrollment exit for the tenant admission layer's
   * multi-factor refusal. When the scope settles
   * `authn.insufficient_user_authentication`, the region reads the
   * caller's factors: with an enabled one it renders this sign-in exit
   * under the resolved sentence, and with none it replaces that sentence
   * with the missing two-step verification and points at the host's
   * account settings. Every other code keeps its existing rendering. Omit
   * it on a surface with no exit of its own.
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
  // again, which would meet the same refusal. The alert carries the
  // enrollment guidance instead, so the sentence and the link below it
  // agree.
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

/**
 * The text a scope's failure resolves to, or null when the slot holds no
 * failure. A page passes the same resolver the region binds, so an inline
 * row marker and the region show the one message.
 */
export function failureTextOf(
  outcome: WriteOutcome | null,
  resolve: (code: string) => string,
): string | null {
  return outcome !== null && outcome.kind === 'failure'
    ? resolve(outcome.code)
    : null
}

/**
 * A ref for a scope's region container that brings the region into view
 * when its scope settles a failure. The region sits directly under a
 * control group whose table may be taller than the viewport, so a row's
 * failure could otherwise be reported out of sight. Success never moves
 * the page, and neither does a cleared slot. An operator who asks for
 * reduced motion gets an instant jump instead of an animation.
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
