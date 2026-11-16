/**
 * AcceptInvitationScreen: the recipient's side of an invitation.
 *
 * An administrator issues an invitation from the console, and the invitee
 * redeems it through this screen: it reads the invitation a token points to
 * (the readInvitation operation) and accepts it (the acceptInvitation
 * operation). It performs no HTTP of its own -- the operations arrive as an
 * `invitations` prop the host binds to the generated @speed/api-sdk
 * invitation operations, the same host-supplied-operations shape every
 * auth-ui component drives.
 *
 * The token grants joining the organization; it does not prove ownership of
 * the invited address. The inviter holds the token too, so acceptance needs
 * a signed-in account that holds the invited address as a proven email. The
 * address does not have to be one the account signs in with. When the host
 * reports a signed-in session, the screen also reads the caller's eligibility
 * (the readEligibility operation) so an account that has not proved the
 * address meets the proof step on arrival instead of after a refused
 * acceptance. A 401 from that read is treated as an anonymous visitor: the
 * eligibility endpoint is guarded, and a session the server no longer accepts
 * is not one this screen can reason about. Acceptance never creates an
 * account, so the screen asks for no username, display name or password.
 *
 * The invitation read stays public: the token is the whole credential for
 * showing what the link points to, and an anonymous viewer sees the invitation
 * itself -- the inviter's display name, the role when one was attached, the
 * organization, the masked address and the deadline -- with the host's
 * sign-in and create-account actions, rather than a generic sign-in page. The
 * screen renders no gate of its own: it reads no session state and reports
 * the two actions through the callbacks the host supplies.
 *
 * Terminal links are told apart: an expired invitation asks the viewer to
 * contact the inviter for a new one, a revoked invitation says it was
 * revoked, and an accepted invitation whose organization the current session
 * already belongs to shows membership and offers the organization, while an
 * accepted link opened by anyone else only says it was used. The host answers
 * membership through `isMemberOf`, because the public read carries the
 * organization's names but no membership fact.
 *
 * The read's view is judged before it is shown: the contract answers the
 * server's four-state verdict, and the screen renders that state as answered --
 * expiry is the server's judgement, never a comparison against the deadline on
 * the local clock. A bare 404 is the invalid link, whose text names the "use
 * the most recent email" remedy; any other read failure renders the answer's
 * code through the family's error banner with a retry. A refused acceptance
 * renders its code the same way and leaves the accept action ready for another
 * try, except 409 refusals split by their problem code: a disabled
 * organization answers `invitation-tenant-inactive`, which explains that the
 * organization is disabled and the invitation stays pending; a suspended
 * membership answers `membership-inactive`, which keeps its own sentence
 * because the backend revoked the invitation as it refused and a suspended
 * member cannot be invited again; and a moved link (`invitation-terminal`)
 * re-reads the invitation and renders its terminal state, with the refusal
 * left in the banner if the re-read still answers pending. The unproved-email
 * refusal opens the proof step, which carries the same banner and
 * disabled-organization explanation as the landing.
 * Host callbacks (onAccepted, onSignIn, onRegister, onOpenTenant) fire after
 * the operation settles and are contained -- a throwing callback is never a
 * failure of the action it follows.
 *
 * The screen's titles render as `headingLevel` (default h1, the screen
 * normally being the page), and the confirmation, terminal and proof blocks
 * take the level one below. The expiry renders in the host's display time
 * zone when one is provided (ui-kit's display-time-zone context), else the
 * device zone.
 */

import { useEffect, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'
import { errorCodeOf, useFormatInstant, visuallyHiddenSx } from '@speed/ui-kit'
import { useAuthUiTranslation } from './internal/translation.js'
import { InlineError } from './internal/inline-error.js'
import { RetryAction } from './internal/retry-action.js'
import { nextHeadingLevel } from './internal/heading.js'
import type { HeadingLevel } from './internal/heading.js'
import { useStepFocus } from './internal/step-focus.js'

/**
 * The invitation as the screen renders it: the public read's fields the
 * recipient needs (the host adapts the generated view to this shape).
 *
 * `tenant` is the organization's request-surface name and `tenantDisplayName`
 * is what it chose to be shown by; a surface shows the display name when it
 * is non-empty and falls back to the name. The accepted state answers both
 * names so the membership message can address the organization without the
 * address or the inviter.
 */
export interface InvitationSummary {
  /** The organization's request-surface name. */
  readonly tenant: string
  /** The organization's display name; empty when it chose none. */
  readonly tenantDisplayName?: string
  /** The invited address, masked on the server. */
  readonly email: string
  /** The server-judged state: pending, expired, accepted or revoked. */
  readonly status: string
  /** The RFC 3339 deadline; empty when the state carries none. */
  readonly expiresAt: string
  /**
   * The inviter's display name. Absent for a host-command issuance, an
   * account that no longer exists, or an account whose display name is empty.
   */
  readonly invitedByName?: string
  /** The role the invitation grants, when it carries one. */
  readonly role?: string
}

/**
 * The signed-in caller's eligibility for a token: the state the server judges
 * and whether the caller holds the invited address as a proven email. It
 * never carries the address, the inviter or the organization.
 */
export interface InvitationEligibility {
  /** The server-judged state: pending, expired, accepted or revoked. */
  readonly state: string
  /** Whether the signed-in account has proved the invited address. */
  readonly emailProved: boolean
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
   * Reads the signed-in caller's eligibility
   * (GET /api/v1/auth/invitations/:token/eligibility). The endpoint is
   * guarded: its 401 means the session is not usable, and the screen treats
   * that as an anonymous visitor. Omitted, the screen cannot pre-flight the
   * proof step and leaves that decision to the acceptance.
   */
  readonly readEligibility?: (token: string) => Promise<InvitationEligibility>
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
  /**
   * Whether the host's session is authenticated. The screen reads no session
   * state itself; the host reports the fact so the screen can pre-flight the
   * proof step and, without a session, offer the sign-in and create-account
   * actions.
   */
  readonly signedIn?: boolean
  /** Fired once after the acceptance lands; the host navigates. */
  readonly onAccepted?: () => void
  /**
   * Navigates to the host's account surface, where the signed-in account
   * adds and verifies an email address. The proof step renders its action
   * when this is supplied; without it the step only explains what to do and
   * offers to accept again.
   */
  readonly onProveEmail?: () => void
  /** The host's sign-in action for an anonymous viewer. */
  readonly onSignIn?: () => void
  /** The host's create-account action for an anonymous viewer. */
  readonly onRegister?: () => void
  /**
   * Opens the accepted invitation's organization. Rendered on an accepted
   * link when `isMemberOf` says the current session already belongs to it;
   * the host switches the current organization and navigates.
   */
  readonly onOpenTenant?: (tenant: string) => void
  /**
   * Whether the current session is a member of the named organization. An
   * accepted link uses this to tell "you are already a member" apart from
   * "this invitation was used", which the public read cannot answer.
   * `undefined` means the host has not settled that fact yet; the screen then
   * waits instead of guessing, so a still-loading tenant list cannot make a
   * member's own link read as used.
   */
  readonly isMemberOf?: (tenant: string) => boolean | undefined
  /**
   * The heading element the screen's titles render as; `'h1'` by default
   * (the screen is normally the page). The confirmation, terminal and proof
   * blocks render one level below it.
   */
  readonly headingLevel?: HeadingLevel
}

/** Whether a failure is a bare 404 (no such invitation) or a 409 (the state
 * moved); anything else renders the code itself. */
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
 * The problem code the backend answers when the invitation's organization is
 * disabled. The invitation stays pending, so a re-read would still answer
 * pending; the screen explains the refusal instead of re-reading into the
 * same landing. The organization can be re-enabled, and the same token then
 * redeems, so the accept action stays.
 */
const TENANT_INACTIVE_CODE = 'authn.invitation_tenant_inactive'

/**
 * The problem code the backend answers when the caller's membership in the
 * organization is suspended. The backend revokes the invitation as it
 * refuses, and a suspended member cannot be invited again, so re-reading
 * would render the revoked landing with a remedy that does not exist. The
 * refusal's own sentence is the answer, and the link has not moved.
 */
const MEMBERSHIP_INACTIVE_CODE = 'authn.membership_inactive'

/**
 * The code that means the link moved (expired, revoked or accepted): the
 * screen re-reads so the terminal state answers the click.
 */
const INVITATION_TERMINAL_CODE = 'authn.invitation_terminal'

/** Whether a failure carries an HTTP status, read defensively. */
function isStatus(error: unknown, status: number): boolean {
  if (typeof error !== 'object' || error === null) {
    return false
  }
  const candidate = error as {
    readonly status?: unknown
    readonly code?: unknown
  }
  return (
    candidate.status === status || candidate.code === `client.http.${status}`
  )
}

/** The state the screen renders: the server's own verdict. A public read that
 * already reached a terminal state is authoritative; the eligibility read can
 * move a pending link to a terminal state, because it was judged at a later
 * instant, but it cannot revive one the public read found ended. The deadline
 * is not a state: the server judges expiry, so the screen never derives
 * "expired" from `expiresAt` on the local clock. */
function terminalStateOf(
  invitation: InvitationSummary,
  eligibility: Eligibility,
): string {
  if (invitation.status !== 'pending') {
    return invitation.status
  }
  if (eligibility.kind === 'proved' || eligibility.kind === 'unproved') {
    return eligibility.state
  }
  return 'pending'
}

/** Whether a failed read is a bare 404 (no such invitation) or a 409 (the
 * state moved); anything else renders the code itself. */
function classifyReadFailure(error: unknown): ReadFailure {
  if (isStatus(error, 404)) {
    return { kind: 'missing' }
  }
  if (isStatus(error, 409)) {
    return { kind: 'unavailable' }
  }
  return { kind: 'error', code: errorCodeOf(error) }
}

/** The organization's name as a surface shows it: its display name when it
 * chose one, else the request-surface name. */
function organizationName(invitation: InvitationSummary): string {
  const displayName = invitation.tenantDisplayName?.trim() ?? ''
  return displayName === '' ? invitation.tenant : displayName
}

/** The eligibility answer as the screen uses it. */
type Eligibility =
  | { readonly kind: 'anonymous' }
  | { readonly kind: 'unproved'; readonly state: string }
  | { readonly kind: 'proved'; readonly state: string }
  | { readonly kind: 'unknown' }

type ScreenState =
  | { readonly status: 'loading' }
  | {
      readonly status: 'loaded'
      readonly invitation: InvitationSummary
      readonly eligibility: Eligibility
      readonly accepted: boolean
    }
  | { readonly status: 'failed'; readonly failure: ReadFailure }

export function AcceptInvitationScreen({
  token,
  invitations,
  signedIn = false,
  onAccepted,
  onProveEmail,
  onSignIn,
  onRegister,
  onOpenTenant,
  isMemberOf,
  headingLevel = 'h1',
}: AcceptInvitationScreenProps) {
  const { t } = useAuthUiTranslation()
  // The host's display time zone when one is provided, else the browser's
  // zone, else UTC: the screen never hides the line over an unusable zone.
  const formatInstant = useFormatInstant()
  const [attempt, setAttempt] = useState(0)
  const [state, setState] = useState<ScreenState>({ status: 'loading' })
  const [accepting, setAccepting] = useState(false)
  const [acceptError, setAcceptError] = useState<string | null>(null)
  // Whether the last acceptance was refused because the signed-in account
  // has not proved the invited address. The proof step replaces the accept
  // action until a later attempt starts; a retry that is refused the same way
  // reopens it.
  const [unproved, setUnproved] = useState(false)
  // Whether the last acceptance was refused because the organization is
  // disabled. The invitation stays pending, so the landing is still the right
  // surface; the explanation stands beside the accept action until the next
  // attempt.
  const [tenantInactive, setTenantInactive] = useState(false)
  // The proof step replaces the surface the viewer was working in, so its
  // first control takes focus after the commit (the accept action unmounts).
  const proofRef = useStepFocus(unproved ? 'email-unproved' : null)

  // One read per attempt; a cancelled closure (unmount, or a retry while a
  // read is in flight) never writes state. The eligibility read runs only
  // when the host reports a session; its 401 is an anonymous visitor, and any
  // other failure leaves the proof decision to the backend.
  useEffect(() => {
    let cancelled = false
    setState({ status: 'loading' })
    const read = async (): Promise<void> => {
      const invitation = await invitations.readInvitation(token)
      let eligibility: Eligibility = { kind: 'anonymous' }
      if (signedIn && invitations.readEligibility !== undefined) {
        try {
          const answer = await invitations.readEligibility(token)
          eligibility = answer.emailProved
            ? { kind: 'proved', state: answer.state }
            : { kind: 'unproved', state: answer.state }
        } catch (error: unknown) {
          eligibility = isStatus(error, 401)
            ? { kind: 'anonymous' }
            : { kind: 'unknown' }
        }
      } else if (signedIn) {
        eligibility = { kind: 'unknown' }
      }
      if (!cancelled) {
        setState({ status: 'loaded', invitation, eligibility, accepted: false })
      }
    }
    read().then(undefined, (error: unknown) => {
      if (!cancelled) {
        setState({ status: 'failed', failure: classifyReadFailure(error) })
      }
    })
    return () => {
      cancelled = true
    }
  }, [token, attempt, invitations, signedIn])

  const accept = (): void => {
    // Every attempt starts from a clean result: the previous attempt's
    // refusal or proof step does not stand beside this attempt.
    setAcceptError(null)
    setUnproved(false)
    setTenantInactive(false)
    setAccepting(true)
    invitations.acceptInvitation(token).then(
      () => {
        setAccepting(false)
        setState((prev) =>
          prev.status === 'loaded' ? { ...prev, accepted: true } : prev,
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
        if (isStatus(error, 409)) {
          if (code === TENANT_INACTIVE_CODE) {
            // The organization is disabled, not the link: the invitation
            // stays pending, so a re-read would answer pending and leave the
            // viewer with no explanation. Say what the refusal means and
            // keep the action, because re-enabling the organization makes
            // the same token redeemable.
            setTenantInactive(true)
            return
          }
          if (code === MEMBERSHIP_INACTIVE_CODE) {
            // The caller's membership is suspended, and the backend revoked
            // the invitation as it refused. The refusal's own sentence is
            // the remedy that exists: not a re-read into the revoked
            // landing, because a suspended member cannot be invited again.
            setAcceptError(code)
            return
          }
          if (code === INVITATION_TERMINAL_CODE) {
            // The link moved while the viewer was here: re-read and render
            // the terminal state it now has. The code stays in the banner so
            // a re-read that still answers pending leaves the refusal
            // visible instead of returning the viewer to an unchanged page.
            setAcceptError(code)
            setAttempt((value) => value + 1)
            return
          }
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
    if (state.failure.kind === 'missing') {
      return (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
          <Typography component={headingLevel} variant="h5">
            {t('acceptInvitation.missing.title')}
          </Typography>
          <Typography color="text.secondary">
            {t('acceptInvitation.missing.description')}
          </Typography>
          <Typography color="text.secondary">
            {t('acceptInvitation.missing.hint')}
          </Typography>
        </Box>
      )
    }
    if (state.failure.kind === 'unavailable') {
      return (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
          <Typography component={headingLevel} variant="h5">
            {t('acceptInvitation.unavailable.title')}
          </Typography>
          <Typography color="text.secondary">
            {t('acceptInvitation.unavailable.description')}
          </Typography>
        </Box>
      )
    }
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
        <Typography component={headingLevel} variant="h5">
          {t('acceptInvitation.error.title')}
        </Typography>
        <InlineError code={state.failure.code} />
        <RetryAction
          label={t('acceptInvitation.retry')}
          onClick={() => setAttempt((v) => v + 1)}
        />
      </Box>
    )
  }

  const { invitation, eligibility, accepted } = state
  // The public read supplies the organization names; the eligibility read
  // can only move a pending link to a terminal state, because the two calls
  // are separate instants. A public read that already reached a terminal
  // state wins: the eligibility answer for a pending link is not about an
  // accepted one.
  const judgedState = terminalStateOf(invitation, eligibility)

  // A link that just landed shows the acceptance confirmation; it never
  // re-enters the terminal or landing branches, because the acceptance is
  // this screen's own success.
  if (!accepted) {
    if (judgedState === 'expired') {
      return (
        <TerminalBlock
          headingLevel={headingLevel}
          title={t('acceptInvitation.expired.title')}
          description={t('acceptInvitation.expired.description')}
        />
      )
    }
    if (judgedState === 'revoked') {
      return (
        <TerminalBlock
          headingLevel={headingLevel}
          title={t('acceptInvitation.revoked.title')}
          description={t('acceptInvitation.revoked.description')}
        />
      )
    }
    if (judgedState === 'accepted') {
      const member = isMemberOf?.(invitation.tenant)
      // A signed-in host that reports the fact has not settled it yet: wait
      // rather than telling a member their own organization's link was used.
      // An anonymous viewer cannot be a member, so the fact is not pending
      // for them and the used wording is the honest answer.
      if (signedIn && isMemberOf !== undefined && member === undefined) {
        return (
          <Box
            role="status"
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 1.5,
              width: '100%',
            }}
          >
            <CircularProgress size={18} aria-hidden={true} />
            <Typography>{t('acceptInvitation.loading')}</Typography>
          </Box>
        )
      }
      if (member === true) {
        return (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
            <Typography component={headingLevel} variant="h5">
              {t('acceptInvitation.member.title', {
                tenant: organizationName(invitation),
              })}
            </Typography>
            <Typography color="text.secondary">
              {t('acceptInvitation.member.description')}
            </Typography>
            {onOpenTenant !== undefined ? (
              <Button
                type="button"
                variant="contained"
                onClick={() => {
                  try {
                    onOpenTenant(invitation.tenant)
                  } catch {
                    // A throwing host callback is not a failure of this view.
                  }
                }}
                sx={{ alignSelf: 'flex-start' }}
              >
                {t('acceptInvitation.member.open')}
              </Button>
            ) : null}
          </Box>
        )
      }
      return (
        <TerminalBlock
          headingLevel={headingLevel}
          title={t('acceptInvitation.used.title')}
          description={t('acceptInvitation.used.description')}
        />
      )
    }
    if (judgedState !== 'pending') {
      return (
        <TerminalBlock
          headingLevel={headingLevel}
          title={t('acceptInvitation.unavailable.title')}
          description={t('acceptInvitation.unavailable.description')}
        />
      )
    }
  }

  const expiry = formatInstant(invitation.expiresAt)
  const anonymous =
    eligibility.kind === 'anonymous' ||
    (eligibility.kind === 'unknown' && !signedIn)
  const proofRequired =
    !accepted &&
    !anonymous &&
    (unproved || eligibility.kind === 'unproved')

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      <Typography component={headingLevel} variant="h5">
        {t('acceptInvitation.title')}
      </Typography>
      {expiry !== '' ? (
        <Typography color="text.secondary">
          {t('acceptInvitation.expires', { expires: expiry })}
        </Typography>
      ) : null}

      {/* The acceptance confirmation's status region stands for the whole
          loaded life, born empty (and visually silent) while the landing or
          the proof step is up: a live region announces only content changes
          that follow its own insertion, so a confirmation born together with
          its region would be silent. The acceptance fills the text into a
          region the screen reader already knows. */}
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

      {accepted ? null : (
        <>
          <Typography>
            {invitationSentence(t, {
              inviter: invitation.invitedByName?.trim() ?? '',
              role: invitation.role?.trim() ?? '',
              organization: organizationName(invitation),
            })}
          </Typography>
          {invitation.email !== '' ? (
            <Typography color="text.secondary">
              {t('acceptInvitation.email', { email: invitation.email })}
            </Typography>
          ) : null}
        </>
      )}

      {accepted ? null : anonymous ? (
        <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1 }}>
          {onSignIn !== undefined ? (
            <Button
              type="button"
              variant="contained"
              onClick={() => {
                try {
                  onSignIn()
                } catch {
                  // A throwing host callback is not a failure of this view.
                }
              }}
              sx={{ alignSelf: 'flex-start' }}
            >
              {t('acceptInvitation.signIn')}
            </Button>
          ) : null}
          {onRegister !== undefined ? (
            <Button
              type="button"
              variant="outlined"
              onClick={() => {
                try {
                  onRegister()
                } catch {
                  // A throwing host callback is not a failure of this view.
                }
              }}
              sx={{ alignSelf: 'flex-start' }}
            >
              {t('acceptInvitation.createAccount')}
            </Button>
          ) : null}
        </Box>
      ) : proofRequired ? (
        <Box
          ref={proofRef}
          sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}
        >
          {tenantInactive ? (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5 }}>
              <Typography
                component={nextHeadingLevel(headingLevel)}
                variant="h6"
              >
                {t('acceptInvitation.tenantInactive.title')}
              </Typography>
              <Typography color="text.secondary">
                {t('acceptInvitation.tenantInactive.description')}
              </Typography>
            </Box>
          ) : null}
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
          {/* The proof step's "accept again" is an acceptance like any
              other, so its whole-attempt failures render here too. */}
          <InlineError code={acceptError} />
        </Box>
      ) : (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
          {tenantInactive ? (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5 }}>
              <Typography
                component={nextHeadingLevel(headingLevel)}
                variant="h6"
              >
                {t('acceptInvitation.tenantInactive.title')}
              </Typography>
              <Typography color="text.secondary">
                {t('acceptInvitation.tenantInactive.description')}
              </Typography>
            </Box>
          ) : null}
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

/** The invitation sentence: the inviter's display name and the role are
 * optional, and each combination has its own key so no sentence is built by
 * concatenating translated fragments. */
function invitationSentence(
  t: ReturnType<typeof useAuthUiTranslation>['t'],
  values: {
    readonly inviter: string
    readonly role: string
    readonly organization: string
  },
): string {
  if (values.inviter !== '' && values.role !== '') {
    return t('acceptInvitation.invitedByWithRole', {
      inviter: values.inviter,
      role: values.role,
      tenant: values.organization,
    })
  }
  if (values.inviter !== '') {
    return t('acceptInvitation.invitedBy', {
      inviter: values.inviter,
      tenant: values.organization,
    })
  }
  if (values.role !== '') {
    return t('acceptInvitation.invitedWithRole', {
      role: values.role,
      tenant: values.organization,
    })
  }
  return t('acceptInvitation.invited', { tenant: values.organization })
}

/** A terminal state: a title and its explanation, both static (not a live
 * region: a page-level result that a screen reader reaches on arrival). */
function TerminalBlock({
  headingLevel,
  title,
  description,
}: {
  readonly headingLevel: HeadingLevel
  readonly title: string
  readonly description: string
}) {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      <Typography component={headingLevel} variant="h5">
        {title}
      </Typography>
      <Typography color="text.secondary">{description}</Typography>
    </Box>
  )
}
