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
 * The acceptance is public: the accept endpoint needs no credential of its
 * own -- the token is the whole credential -- and the membership lands on the
 * invited address's account, not on whoever is signed in. A host may render
 * its own sign-in / register surface ahead of the screen (the demo host does
 * so, src/pages/accept-invitation-page.tsx); that is a composition choice,
 * not what authorizes or binds the acceptance. The screen itself reads
 * nothing from storage and consumes no auth-core hook.
 *
 * When the invited address has no account yet the acceptance creates one, so
 * the screen requires a username and offers an optional display name and
 * password for that case (the accept body's username, display_name and
 * password); all three are ignored when the address already has an account.
 * The acceptView answer carries no signal of whether the invited address
 * already has an account, so the screen offers the inputs whenever the host
 * keeps them and the server ignores them on the existing-account path.
 * A new account is opened with a login name, so a submit with the field blank
 * is refused here rather than sent, and the server answers the same refusal
 * as a 400 when a request reaches it without one. A host that binds the
 * account creation itself restricts the inputs through `newAccountFields`;
 * hiding all of them also hides the hint that would describe fields that are
 * not there. The screen's titles render as `headingLevel`
 * (default h1, the screen normally being the page) and the accepted
 * confirmation takes the level one below. The expiry renders in the host's
 * display time zone when one is provided (ui-kit's display-time-zone
 * context), else the device zone.
 *
 * The read's view is judged before it is shown: the acceptView contract
 * returns the stored state even when the link can no longer be redeemed, so a
 * status other than pending, or a pending deadline at or past, renders the
 * same "no longer valid" state as a 409 -- without the summary or the accept
 * action. The two read failures the contract names get their own text: a bare
 * 404 ("no such invitation") and a 409 ("expired, revoked or already
 * accepted"); any other failure renders the answer's code through the
 * family's error banner with a retry. A refused acceptance renders its code
 * the same way and leaves the accept action ready for another try, except a
 * refusal of the login name it would open the account with -- taken,
 * reserved, or a shape the server cannot normalize -- which the username
 * field states. Every attempt begins from a clean result: editing the name
 * and a submit the client refuses before sending both drop the previous
 * attempt's server refusal. Host
 * callbacks (onAccepted) fire after the operation settles and are contained
 * -- a throwing callback is never a failure of the acceptance.
 */

import { useEffect, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { formatInstant } from '@speed/i18n'
import { errorCodeOf, useDisplayTimeZone, visuallyHiddenSx } from '@speed/ui-kit'
import { useAuthUiTranslation } from './internal/translation.js'
import { InlineError } from './internal/inline-error.js'
import { nextHeadingLevel } from './internal/heading.js'
import type { HeadingLevel } from './internal/heading.js'

/** The invitation as the screen renders it: the acceptView fields the
 * recipient needs (the host adapts the generated view to this shape). */
export interface InvitationSummary {
  readonly tenant: string
  readonly email: string
  readonly status: string
  readonly expiresAt: string
}

/** The body an acceptance may carry (the username, display name and initial
 * password for an address that has no account yet). */
export interface AcceptInvitationInput {
  readonly username?: string
  readonly displayName?: string
  readonly password?: string
}

/**
 * The invitation operations the screen drives. The host binds these to the
 * generated @speed/api-sdk invitation operations; auth-ui ships no HTTP of
 * its own and references no api-sdk type on its public surface.
 */
export interface InvitationOperations {
  /** Reads the invitation a token points to (GET /api/v1/auth/invitations/:token). */
  readonly readInvitation: (token: string) => Promise<InvitationSummary>
  /** Redeems the invitation (POST /api/v1/auth/invitations/:token). */
  readonly acceptInvitation: (
    token: string,
    input: AcceptInvitationInput,
  ) => Promise<void>
}

export interface AcceptInvitationScreenProps {
  /** The token from the host's route. */
  readonly token: string
  /** The host-bound invitation operations. */
  readonly invitations: InvitationOperations
  /** Fired once after the acceptance lands; the host navigates. */
  readonly onAccepted?: () => void
  /**
   * Which of the new-account inputs to offer (all by default). The username,
   * display name and password only matter when the invited address has no
   * account yet; a host that binds that flow itself hides one or more.
   * Hiding all of them also hides the explanatory hint, which would
   * otherwise describe fields that are not there.
   */
  readonly newAccountFields?: {
    readonly username?: boolean
    readonly displayName?: boolean
    readonly password?: boolean
  }
  /**
   * The heading element the screen's titles render as; `'h1'` by default
   * (the screen is normally the page). The accepted confirmation renders
   * one level below it.
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

/** The bundle key a refused login name renders under, or null for a failure
 * the whole-attempt banner owns. */
function usernameRejectionKey(code: string): string | null {
  switch (code) {
    case 'authn.username_taken':
      return 'acceptInvitation.usernameTaken'
    case 'authn.username_reserved':
      return 'acceptInvitation.usernameReserved'
    case 'authn.username_invalid':
      return 'acceptInvitation.usernameInvalid'
    default:
      return null
  }
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
  newAccountFields,
  headingLevel = 'h1',
}: AcceptInvitationScreenProps) {
  const { t, i18n } = useAuthUiTranslation()
  // The host's display time zone when one is provided; absent, the device
  // zone (the same rendering as before the provider existed).
  const timeZone = useDisplayTimeZone()
  const showUsername = newAccountFields?.username !== false
  const showDisplayName = newAccountFields?.displayName !== false
  const showPassword = newAccountFields?.password !== false
  const showNewAccountFields = showUsername || showDisplayName || showPassword
  const [attempt, setAttempt] = useState(0)
  const [state, setState] = useState<ScreenState>({ status: 'loading' })
  const [accepting, setAccepting] = useState(false)
  const [acceptError, setAcceptError] = useState<string | null>(null)
  // The username, display name and password for an address that has no
  // account yet; the username is required then and the other two are
  // optional. All three are ignored by the backend when the address already
  // has an account. usernameMissing marks a submit with the required field
  // blank, which never reaches the server; usernameRejection holds the
  // server's own refusal of the name (taken, reserved, or a shape it cannot
  // normalize), which the field states rather than the banner.
  const [username, setUsername] = useState('')
  const [usernameMissing, setUsernameMissing] = useState(false)
  const [usernameRejection, setUsernameRejection] = useState<string | null>(
    null,
  )
  const [displayName, setDisplayName] = useState('')
  const [password, setPassword] = useState('')

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
    const name = username.trim()
    // Every attempt starts from a clean result, including one the client
    // refuses before sending: the previous attempt's server refusal does
    // not stand beside this attempt's field error.
    setAcceptError(null)
    setUsernameRejection(null)
    if (showUsername && name === '') {
      // An account is opened with a login name: a submit without one can only
      // make a round trip that answers 400, so it is refused here and the
      // field carries the reason.
      setUsernameMissing(true)
      return
    }
    setUsernameMissing(false)
    setAccepting(true)
    const input: AcceptInvitationInput = {
      ...(name === '' ? {} : { username: name }),
      ...(displayName.trim() === '' ? {} : { displayName: displayName.trim() }),
      ...(password === '' ? {} : { password }),
    }
    invitations.acceptInvitation(token, input).then(
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
        const rejection = usernameRejectionKey(code)
        if (rejection !== null && showUsername) {
          // The server refused the login name the acceptance would open the
          // account with: the name is the value this screen can change, so
          // the field states it instead of the whole-attempt banner.
          setUsernameRejection(rejection)
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
            <Button variant="outlined" onClick={() => setAttempt((v) => v + 1)}>
              {t('acceptInvitation.retry')}
            </Button>
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

  // ready or accepted: the invitation summary and either the accept action or
  // the acceptance confirmation.
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
          the form is up: a live region announces only content changes
          that follow its own insertion, so a confirmation born together
          with its region would be silent. The acceptance fills the text
          into a region the screen reader already knows. */}
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
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
          {showUsername ? (
            <TextField
              size="small"
              required
              autoComplete="username"
              value={username}
              error={usernameMissing || usernameRejection !== null}
              helperText={
                usernameMissing
                  ? t('acceptInvitation.usernameRequired')
                  : usernameRejection === null
                    ? undefined
                    : t(usernameRejection)
              }
              slotProps={{
                htmlInput: {
                  'aria-invalid': usernameMissing || usernameRejection !== null,
                },
              }}
              onChange={(event) => {
                setUsername(event.target.value)
                if (usernameMissing) {
                  setUsernameMissing(false)
                }
                if (usernameRejection !== null) {
                  setUsernameRejection(null)
                }
                // Editing the name starts a new attempt: the previous
                // attempt's server refusal does not stand beside the edited
                // input.
                setAcceptError(null)
              }}
              label={t('acceptInvitation.usernameLabel')}
            />
          ) : null}
          {showDisplayName ? (
            <TextField
              size="small"
              value={displayName}
              onChange={(event) => setDisplayName(event.target.value)}
              label={t('acceptInvitation.displayNameLabel')}
            />
          ) : null}
          {showPassword ? (
            <TextField
              size="small"
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              label={t('acceptInvitation.passwordLabel')}
            />
          ) : null}
          {showNewAccountFields ? (
            <Typography variant="caption" color="text.secondary">
              {t('acceptInvitation.newAccountHint')}
            </Typography>
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
