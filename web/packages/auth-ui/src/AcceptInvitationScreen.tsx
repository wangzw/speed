/**
 * AcceptInvitationScreen: the recipient's side of an invitation.
 *
 * An administrator issues an invitation from the console, and the invitee
 * redeems it through this screen: it reads the invitation a token points to
 * (the acceptView operation) and accepts it (the accept operation). It
 * performs no HTTP of its own -- the two operations arrive as an
 * `invitations` prop the host binds to the generated @speed/api-sdk
 * invitation operations, the same host-supplied-operations shape every
 * auth-ui component drives.
 *
 * The token grants joining the organization; it does not prove ownership of
 * the invited address. The inviter holds the token too, so acceptance needs
 * a signed-in account that holds the invited address as a proven email. The
 * address does not have to be one the account signs in with. An account that
 * has not proved it is refused with `authn.invitation_email_unproved`, and
 * the screen answers with a proof step: it names the address that must be
 * proved, points at the host's account surface when the host supplies
 * `onProveEmail`, and offers to accept again once the proof is in place. It
 * never says the address becomes a sign-in identifier, because proving an
 * address does not choose it as one. Acceptance never creates an account, so
 * the screen asks for no username, display name or password.
 *
 * The invitation read stays public: the token is the whole credential for
 * showing what the link points to, and the screen renders the invitation it
 * names before it asks for anything else. A host normally renders its own
 * sign-in surface ahead of the screen so the viewer arrives with an account
 * (the demo host does so, src/pages/accept-invitation-page.tsx); that is a
 * composition choice, and the screen itself reads no session state.
 *
 * The screen's titles render as `headingLevel` (default h1, the screen
 * normally being the page), and the accepted confirmation and the proof step
 * take the level one below. The expiry renders in the host's display time
 * zone when one is provided (ui-kit's display-time-zone context), else the
 * device zone.
 *
 * The read's view is judged before it is shown: the acceptView contract
 * returns the stored state even when the link can no longer be redeemed, so a
 * status other than pending, or a pending deadline at or past, renders the
 * same "no longer valid" state as a 409 -- without the summary or the accept
 * action. The two read failures the contract names get their own text: a bare
 * 404 ("no such invitation") and a 409 ("expired, revoked or already
 * accepted"); any other failure renders the answer's code through the
 * family's error banner with a retry. A refused acceptance renders its code
 * the same way and leaves the accept action ready for another try, except the
 * unproved-email refusal, which opens the proof step. A later attempt starts
 * from a clean result. Host callbacks (onAccepted) fire after the operation
 * settles and are contained -- a throwing callback is never a failure of the
 * acceptance.
 */

import { useEffect, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'
import { formatInstant } from '@speed/i18n'
import { errorCodeOf, useDisplayTimeZone, visuallyHiddenSx } from '@speed/ui-kit'
import { useAuthUiTranslation } from './internal/translation.js'
import { InlineError } from './internal/inline-error.js'
import { RetryAction } from './internal/retry-action.js'
import { nextHeadingLevel } from './internal/heading.js'
import type { HeadingLevel } from './internal/heading.js'
import { useStepFocus } from './internal/step-focus.js'

/** The invitation as the screen renders it: the acceptView fields the
 * recipient needs (the host adapts the generated view to this shape). */
export interface InvitationSummary {
  readonly tenant: string
  readonly email: string
  readonly status: string
  readonly expiresAt: string
}

/**
 * The invitation operations the screen drives. The host binds these to the
 * generated @speed/api-sdk invitation operations; auth-ui ships no HTTP of
 * its own and references no api-sdk type on its public surface.
 */
export interface InvitationOperations {
  /** Reads the invitation a token points to (GET /api/v1/auth/invitations/:token). */
  readonly readInvitation: (token: string) => Promise<InvitationSummary>
  /**
   * Redeems the invitation for the signed-in account
   * (POST /api/v1/auth/invitations/:token). The token is the route segment;
   * the caller is the request's identity, so the operation takes nothing
   * else.
   */
  readonly acceptInvitation: (token: string) => Promise<void>
}

export interface AcceptInvitationScreenProps {
  /** The token from the host's route. */
  readonly token: string
  /** The host-bound invitation operations. */
  readonly invitations: InvitationOperations
  /** Fired once after the acceptance lands; the host navigates. */
  readonly onAccepted?: () => void
  /**
   * Navigates to the host's account surface, where the signed-in account
   * adds and verifies an email address. The proof step renders its action
   * when this is supplied; without it the step only explains what to do and
   * offers to accept again.
   */
  readonly onProveEmail?: () => void
  /**
   * The heading element the screen's titles render as; `'h1'` by default
   * (the screen is normally the page). The accepted confirmation and the
   * proof step render one level below it.
   */
  readonly headingLevel?: HeadingLevel
}

/** Whether a failure is a bare 404 (no such invitation) or a 409 (expired,
 * revoked or accepted); anything else renders the code itself. */
type ReadFailure =
  | { readonly kind: 'missing' }
  | { readonly kind: 'unavailable' }
  | { readonly kind: 'error'; readonly code: string }

/**
 * The problem code the backend answers when the caller's account does not
 * hold the invited address as a proven email. It opens the proof step rather
 * than the whole-attempt banner, because telling the viewer to prove the
 * address is the action the refusal asks for.
 */
const UNPROVED_EMAIL_CODE = 'authn.invitation_email_unproved'

/**
 * Whether the read's view can still be redeemed: the stored state must be
 * pending and its deadline ahead. Expiry is not stored, so a pending view at
 * or past `expiresAt` is judged by that instant; an instant the engine cannot
 * read leaves the decision to the backend, which refuses the acceptance.
 */
function isRedeemable(invitation: InvitationSummary): boolean {
  if (invitation.status !== 'pending') {
    return false
  }
  const expiresAt = Date.parse(invitation.expiresAt)
  return Number.isNaN(expiresAt) || expiresAt > Date.now()
}

/** Whether a failed read is a bare 404 (no such invitation) or a 409 (expired,
 * revoked or accepted); anything else renders the code itself. */
function classifyReadFailure(error: unknown): ReadFailure {
  const candidate = (
    typeof error === 'object' && error !== null ? error : {}
  ) as { readonly status?: unknown; readonly code?: unknown }
  if (candidate.status === 404 || candidate.code === 'client.http.404') {
    return { kind: 'missing' }
  }
  if (candidate.status === 409 || candidate.code === 'client.http.409') {
    return { kind: 'unavailable' }
  }
  return { kind: 'error', code: errorCodeOf(error) }
}

type ScreenState =
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly invitation: InvitationSummary }
  | { readonly status: 'accepted'; readonly invitation: InvitationSummary }
  | { readonly status: 'failed'; readonly failure: ReadFailure }

export function AcceptInvitationScreen({
  token,
  invitations,
  onAccepted,
  onProveEmail,
  headingLevel = 'h1',
}: AcceptInvitationScreenProps) {
  const { t, i18n } = useAuthUiTranslation()
  // The host's display time zone when one is provided; absent, the device
  // zone (the same rendering as before the provider existed).
  const timeZone = useDisplayTimeZone()
  const [attempt, setAttempt] = useState(0)
  const [state, setState] = useState<ScreenState>({ status: 'loading' })
  const [accepting, setAccepting] = useState(false)
  const [acceptError, setAcceptError] = useState<string | null>(null)
  // Whether the last acceptance was refused because the signed-in account
  // has not proved the invited address. The proof step replaces the accept
  // action until a later attempt starts; a retry that is refused the same way
  // reopens it.
  const [unproved, setUnproved] = useState(false)
  // The proof step replaces the surface the viewer was working in, so its
  // first control takes focus after the commit (the accept action unmounts).
  const proofRef = useStepFocus(unproved ? 'email-unproved' : null)

  // One read per attempt; a cancelled closure (unmount, or a retry while a
  // read is in flight) never writes state.
  useEffect(() => {
    let cancelled = false
    setState({ status: 'loading' })
    invitations.readInvitation(token).then(
      (invitation) => {
        if (!cancelled) {
          setState(
            isRedeemable(invitation)
              ? { status: 'ready', invitation }
              : { status: 'failed', failure: { kind: 'unavailable' } },
          )
        }
      },
      (error: unknown) => {
        if (!cancelled) {
          setState({ status: 'failed', failure: classifyReadFailure(error) })
        }
      },
    )
    return () => {
      cancelled = true
    }
  }, [token, attempt, invitations])

  const accept = (): void => {
    // Every attempt starts from a clean result: the previous attempt's
    // refusal or proof step does not stand beside this attempt.
    setAcceptError(null)
    setUnproved(false)
    setAccepting(true)
    invitations.acceptInvitation(token).then(
      () => {
        setAccepting(false)
        setState((prev) =>
          prev.status === 'ready'
            ? { status: 'accepted', invitation: prev.invitation }
            : prev,
        )
        try {
          onAccepted?.()
        } catch {
          // A throwing host callback is not a failure of the acceptance: it
          // settled, so the screen stays in the accepted state.
        }
      },
      (error: unknown) => {
        setAccepting(false)
        const code = errorCodeOf(error)
        if (code === UNPROVED_EMAIL_CODE) {
          // The caller has not proved the invited address: show what to do
          // instead of a bare error. The invitation stays pending, so the
          // same action can be retried once the proof is in place.
          setUnproved(true)
          return
        }
        setAcceptError(code)
      },
    )
  }

  if (state.status === 'loading') {
    return (
      <Box
        role="status"
        sx={{ display: 'flex', alignItems: 'center', gap: 1.5, width: '100%' }}
      >
        <CircularProgress size={18} aria-hidden={true} />
        <Typography>{t('acceptInvitation.loading')}</Typography>
      </Box>
    )
  }

  if (state.status === 'failed') {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
        {state.failure.kind === 'error' ? (
          <>
            <Typography component={headingLevel} variant="h5">
              {t('acceptInvitation.error.title')}
            </Typography>
            <InlineError code={state.failure.code} />
            <RetryAction
              label={t('acceptInvitation.retry')}
              onClick={() => setAttempt((v) => v + 1)}
            />
          </>
        ) : (
          <>
            <Typography component={headingLevel} variant="h5">
              {state.failure.kind === 'missing'
                ? t('acceptInvitation.missing.title')
                : t('acceptInvitation.unavailable.title')}
            </Typography>
            <Typography color="text.secondary">
              {state.failure.kind === 'missing'
                ? t('acceptInvitation.missing.description')
                : t('acceptInvitation.unavailable.description')}
            </Typography>
          </>
        )}
      </Box>
    )
  }

  // ready or accepted: the invitation summary and either the accept action,
  // the proof step or the acceptance confirmation.
  const { invitation } = state
  const accepted = state.status === 'accepted'
  const expiry = formatInstant(invitation.expiresAt, i18n.language, {
    timeZone,
  })
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      <Typography component={headingLevel} variant="h5">
        {t('acceptInvitation.title')}
      </Typography>
      <Typography>
        {t('acceptInvitation.invited', { tenant: invitation.tenant })}
      </Typography>
      <Typography color="text.secondary">
        {t('acceptInvitation.email', { email: invitation.email })}
      </Typography>
      {/* The host's display zone is unvalidated, and the shared
          formatter degrades a zone the engine rejects to an empty
          string: an empty result means no expiry line, never a dated
          label with no value beside it. */}
      {expiry !== '' && (
        <Typography color="text.secondary">
          {t('acceptInvitation.expires', { expires: expiry })}
        </Typography>
      )}

      {/* The acceptance confirmation's status region stands for the whole
          ready and accepted life, born empty (and visually silent) while
          the accept action or the proof step is up: a live region announces
          only content changes that follow its own insertion, so a
          confirmation born together with its region would be silent. The
          acceptance fills the text into a region the screen reader already
          knows. */}
      <Box
        role="status"
        sx={
          accepted
            ? { display: 'flex', flexDirection: 'column', gap: 0.5 }
            : visuallyHiddenSx
        }
      >
        {accepted ? (
          <>
            <Typography component={nextHeadingLevel(headingLevel)} variant="h6">
              {t('acceptInvitation.accepted.title')}
            </Typography>
            <Typography color="text.secondary">
              {t('acceptInvitation.accepted.description')}
            </Typography>
          </>
        ) : null}
      </Box>
      {unproved && !accepted ? (
        <Box
          ref={proofRef}
          sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}
        >
          <Typography component={nextHeadingLevel(headingLevel)} variant="h6">
            {t('acceptInvitation.unproved.title')}
          </Typography>
          <Typography color="text.secondary">
            {t('acceptInvitation.unproved.description', {
              email: invitation.email,
            })}
          </Typography>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1 }}>
            {onProveEmail !== undefined ? (
              <Button
                type="button"
                variant="outlined"
                disabled={accepting}
                onClick={onProveEmail}
                sx={{ alignSelf: 'flex-start' }}
              >
                {t('acceptInvitation.unproved.manageAccount')}
              </Button>
            ) : null}
            <Button
              type="button"
              variant="contained"
              disabled={accepting}
              onClick={accept}
              sx={{ alignSelf: 'flex-start' }}
            >
              {t('acceptInvitation.unproved.retry')}
            </Button>
          </Box>
        </Box>
      ) : accepted ? null : (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
          <Button
            variant="contained"
            disabled={accepting}
            onClick={accept}
            sx={{ alignSelf: 'flex-start' }}
          >
            {t('acceptInvitation.accept')}
          </Button>
          <InlineError code={acceptError} />
        </Box>
      )}
    </Box>
  )
}
