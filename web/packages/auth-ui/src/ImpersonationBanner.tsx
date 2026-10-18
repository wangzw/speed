/**
 * ImpersonationBanner: the persistent banner a host mounts in its app
 * frame while the session is impersonating, and the one control that
 * ends the impersonation.
 *
 * The component is purely presentational. It holds no session, issues no
 * request and reads no auth-core hook: the host tells it whether an
 * impersonation is current (`impersonation`) and binds the end operation
 * (`onEnd`). The host mounts it for the whole shell, so the banner can
 * appear and disappear on any page without a remount of the component
 * itself.
 *
 * One polite live region is mounted for the component's lifetime, never
 * conditionally: empty while nothing is happening, filled with the
 * "acting as" announcement when an impersonation appears, and filled
 * with the end confirmation once `onEnd` resolves. The announcement is
 * filled on the tick after the region is in the tree, so a first mount
 * that already has an impersonation -- the frame mounting after a start,
 * or a whole-page reload while impersonating -- is announced too. The confirmation
 * therefore survives the banner's own unmount (the host clears
 * `impersonation` after re-reading the session) and stands until the
 * next attempt or a page reload replaces it. The banner that renders
 * while impersonating is a named `region` landmark; it adds no live
 * region of its own, so the failure alert inside it is not announced
 * twice.
 *
 * The end control does not depend on `tenant` or `expiresAt`: those are
 * the host's status read, both are optional, and the control renders and
 * works even when neither arrived. A click is one write attempt through
 * the write-feedback scope -- the control is disabled and marked busy
 * while it is in flight, a resolved `onEnd` confirms with the success
 * message in the live region, and a rejection renders the answer's code
 * text inside the banner while leaving the control retryable. A bare 404
 * answering "there is no impersonation to end" is the host's concern:
 * its `onEnd` treats that as settled, and this component never inspects
 * an HTTP status -- a rejection that reaches it is a failure with a code
 * like any other.
 *
 * The component never counts down and never acts at `expiresAt`; the
 * host re-reads the session when the impersonation expires.
 */

import { useEffect, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Typography from '@mui/material/Typography'
import { InlineSuccess, useWriteFeedback, visuallyHiddenSx } from '@speed/ui-kit'
import { useAuthUiTranslation } from './internal/translation.js'
import { InlineError } from './internal/inline-error.js'

/** What the host knows about the current impersonation. Both optional
 * fields come from the host's status read; when it could not read them
 * the banner and the end control still render. */
export interface ImpersonationBannerState {
  /** The impersonated account's display name, already resolved to text. */
  readonly subject: string
  /** The tenant identifier the impersonation runs in, when known. */
  readonly tenant?: string
  /** When the impersonation ends, when known. */
  readonly expiresAt?: Date
}

export interface ImpersonationBannerProps {
  /** The current impersonation, or null when none is active. */
  readonly impersonation: ImpersonationBannerState | null
  /** Ends the impersonation. A resolved promise confirms; a rejection is
   * a failure whose code text renders inside the banner. */
  readonly onEnd: () => Promise<void>
}

/** The theme's warning surface, the banner's own visual language: the
 * dark warning role pairs with the palette's own contrast text. */
const bannerSx = {
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  columnGap: 2,
  rowGap: 1,
  px: 2,
  py: 1,
  backgroundColor: 'warning.dark',
  color: 'warning.contrastText',
} as const

/** Formats the expiry in the active language; a malformed date renders
 * nothing rather than throwing out of Intl. */
function formatExpiry(expiresAt: Date, language: string): string | null {
  if (Number.isNaN(expiresAt.getTime())) {
    return null
  }
  return new Intl.DateTimeFormat(language, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(expiresAt)
}

export function ImpersonationBanner({
  impersonation,
  onEnd,
}: ImpersonationBannerProps) {
  const { t, i18n } = useAuthUiTranslation()
  const feedback = useWriteFeedback()
  const resetFeedback = feedback.reset

  // The announcement is committed one tick after the impersonation
  // appears. The live region is therefore already in the tree and empty
  // when its text arrives, including on the component's very first
  // mount with an impersonation already current -- a start that mounts
  // the frame, or a reload while impersonating -- which would otherwise
  // be silent. A new impersonation also replaces the previous one's
  // confirmation: a standing "impersonation ended" would describe a
  // context that is gone.
  const subject = impersonation?.subject ?? null
  const [announcedSubject, setAnnouncedSubject] = useState<string | null>(null)
  useEffect(() => {
    setAnnouncedSubject(subject)
    if (subject !== null) {
      resetFeedback()
    }
  }, [subject, resetFeedback])

  const successMessage =
    feedback.outcome !== null && feedback.outcome.kind === 'success'
      ? feedback.outcome.message
      : null
  const failureCode =
    feedback.outcome !== null && feedback.outcome.kind === 'failure'
      ? feedback.outcome.code
      : null

  const announcement =
    announcedSubject === null
      ? null
      : t('impersonation.actingAs', { subject: announcedSubject })

  const end = (): void => {
    void feedback.run(async () => {
      await onEnd()
      return t('impersonation.ended')
    })
  }

  const expiry =
    impersonation?.expiresAt == null
      ? null
      : formatExpiry(impersonation.expiresAt, i18n.language ?? 'en-US')

  return (
    <>
      {/* The component's one live region, mounted for its whole life:
          empty while idle, the start announcement while impersonating,
          and the end confirmation afterwards. Its confirmation stays
          here after the banner below unmounts. MUI's message slot
          carries `overflow: auto`; squeezed to the 1px clipped width it
          would overflow into a scroll container, which Chromium treats
          as a keyboard focus stop. The hidden branch keeps the slot
          non-scrollable, so the first Tab in the banner reaches the end
          control. */}
      <Box
        sx={
          successMessage !== null
            ? { width: '100%' }
            : {
                ...visuallyHiddenSx,
                '& .MuiAlert-message': { overflow: 'hidden' },
              }
        }
      >
        <InlineSuccess message={successMessage ?? announcement} />
      </Box>
      {impersonation !== null && (
        <Box
          component="section"
          aria-label={t('impersonation.regionLabel')}
          sx={bannerSx}
        >
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              gap: 0.5,
              flexGrow: 1,
              minWidth: 0,
            }}
          >
            <Typography variant="body2" sx={{ fontWeight: 600 }}>
              {t('impersonation.actingAs', {
                subject: impersonation.subject,
              })}
            </Typography>
            {impersonation.tenant !== undefined && (
              <Typography variant="body2">
                {t('impersonation.tenant', { tenant: impersonation.tenant })}
              </Typography>
            )}
            {expiry !== null && (
              <Typography variant="body2">
                {t('impersonation.expiresAt', { expiresAt: expiry })}
              </Typography>
            )}
            <Typography variant="body2">
              {t('impersonation.managementUnavailable')}
            </Typography>
          </Box>
          <Button
            type="button"
            variant="outlined"
            color="inherit"
            loading={feedback.pending}
            onClick={end}
          >
            {t(
              feedback.pending
                ? 'impersonation.ending'
                : 'impersonation.end',
            )}
          </Button>
          {failureCode !== null && (
            // The failure belongs to the banner's own width, below the
            // text and the control, not beside them.
            <Box sx={{ flexBasis: '100%' }}>
              <InlineError code={failureCode} />
            </Box>
          )}
        </Box>
      )}
    </>
  )
}
