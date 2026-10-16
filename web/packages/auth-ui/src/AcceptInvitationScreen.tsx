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
 * the screen offers an optional display name and password for that case (the
 * accept body's display_name and password); both are ignored when the address
 * already has an account, so leaving them blank accepts on the existing one.
 *
 * The two read failures the contract names get their own text: a bare 404
 * ("no such invitation") and a 409 ("expired, revoked or already accepted");
 * any other failure renders the answer's code through the family's error
 * banner with a retry. A refused acceptance renders its code the same way and
 * leaves the accept action ready for another try. Host callbacks (onAccepted)
 * fire after the operation settles and are contained -- a throwing callback
 * is never a failure of the acceptance.
 */

import { useEffect, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { errorCodeOf } from '@speed/ui-kit'
import { useAuthUiTranslation } from './internal/translation.js'
import { InlineError } from './internal/inline-error.js'

/** The invitation as the screen renders it: the acceptView fields the
 * recipient needs (the host adapts the generated view to this shape). */
export interface InvitationSummary {
  readonly tenant: string
  readonly email: string
  readonly status: string
  readonly expiresAt: string
}

/** The body an acceptance may carry (display name and initial password for
 * an address that has no account yet). */
export interface AcceptInvitationInput {
  readonly displayName?: string
  readonly password?: string
}

/**
 * The invitation operations the screen drives. The host binds these to the
 * generated @speed/api-sdk invitation operations; auth-ui ships no HTTP of
 * its own and references no api-sdk type on its public surface.
 */
export interface InvitationOperations {
  /** Reads the invitation a token points to (GET /auth/invitations/:token). */
  readonly readInvitation: (token: string) => Promise<InvitationSummary>
  /** Redeems the invitation (POST /auth/invitations/:token). */
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
}

/** Whether a failure is a bare 404 (no such invitation) or a 409 (expired,
 * revoked or accepted); anything else renders the code itself. */
type ReadFailure =
  | { readonly kind: 'missing' }
  | { readonly kind: 'unavailable' }
  | { readonly kind: 'error'; readonly code: string }

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
}: AcceptInvitationScreenProps) {
  const { t, i18n } = useAuthUiTranslation()
  const [attempt, setAttempt] = useState(0)
  const [state, setState] = useState<ScreenState>({ status: 'loading' })
  const [accepting, setAccepting] = useState(false)
  const [acceptError, setAcceptError] = useState<string | null>(null)
  // The display name and password for an address that has no account yet;
  // both optional, and ignored by the backend when the address already has
  // an account.
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
          setState({ status: 'ready', invitation })
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
    setAccepting(true)
    setAcceptError(null)
    const input: AcceptInvitationInput = {
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
        setAcceptError(errorCodeOf(error))
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
            <Typography component="h1" variant="h5">
              {t('acceptInvitation.error.title')}
            </Typography>
            <InlineError code={state.failure.code} />
            <Button variant="outlined" onClick={() => setAttempt((v) => v + 1)}>
              {t('acceptInvitation.retry')}
            </Button>
          </>
        ) : (
          <>
            <Typography component="h1" variant="h5">
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
  const expires = formatInstant(invitation.expiresAt, i18n.language)
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      <Typography component="h1" variant="h5">
        {t('acceptInvitation.title')}
      </Typography>
      <Typography>
        {t('acceptInvitation.invited', { tenant: invitation.tenant })}
      </Typography>
      <Typography color="text.secondary">
        {t('acceptInvitation.email', { email: invitation.email })}
      </Typography>
      <Typography color="text.secondary">
        {t('acceptInvitation.expires', { expires })}
      </Typography>

      {state.status === 'accepted' ? (
        <Box
          role="status"
          sx={{ display: 'flex', flexDirection: 'column', gap: 0.5 }}
        >
          <Typography component="h2" variant="h6">
            {t('acceptInvitation.accepted.title')}
          </Typography>
          <Typography color="text.secondary">
            {t('acceptInvitation.accepted.description')}
          </Typography>
        </Box>
      ) : (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
          <TextField
            size="small"
            value={displayName}
            onChange={(event) => setDisplayName(event.target.value)}
            label={t('acceptInvitation.displayNameLabel')}
          />
          <TextField
            size="small"
            type="password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            label={t('acceptInvitation.passwordLabel')}
          />
          <Typography variant="caption" color="text.secondary">
            {t('acceptInvitation.newAccountHint')}
          </Typography>
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

/**
 * Formats a server instant in the current language through Intl, holding
 * the locale rules (never formatting a date by hand). An absent or malformed
 * value renders as empty text so a bad field can never reach Intl and throw.
 */
function formatInstant(iso: string, language: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) {
    return ''
  }
  return new Intl.DateTimeFormat(language, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(date)
}
