/** The recipient's side of an invitation: reads the invitation a token points
 * to and accepts it, through host-bound operations. */

import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'
import {
  ActionButton,
  errorCodeOf,
  useFormatInstant,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import { useAuthUiTranslation } from './internal/translation.js'
import { InlineError } from './internal/inline-error.js'
import { RetryAction } from './internal/retry-action.js'
import { nextHeadingLevel } from './internal/heading.js'
import type { HeadingLevel } from './internal/heading.js'
import { useStepFocus } from './internal/step-focus.js'

/** The invitation as the screen renders it: the public read's fields the
 * recipient needs (the host adapts the generated view to this shape). */
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
 * The invitation operations the screen drives, bound by the host to the
 * generated invitation operations.
 */
export interface InvitationOperations {
  /** Reads the invitation a token points to. */
  readonly readInvitation: (token: string) => Promise<InvitationSummary>
  /**
   * Reads the signed-in caller's eligibility. Its 401 means the session is not
   * usable, which the screen treats as an anonymous visitor. Omitted, the
   * screen cannot pre-flight the proof step.
   */
  readonly readEligibility?: (token: string) => Promise<InvitationEligibility>
  /** Redeems the invitation for the signed-in account. */
  readonly acceptInvitation: (token: string) => Promise<void>
}

/** The host supplies the token, the bound invitation operations, its session
 * state and the navigation callbacks; the screen owns the read, proof and
 * terminal state. */
export interface AcceptInvitationScreenProps {
  /** The token from the host's route. */
  readonly token: string
  /** The host-bound invitation operations. */
  readonly invitations: InvitationOperations
  /**
   * Whether the host's session is authenticated; the screen reads no session
   * state itself. Without a session it offers the sign-in and create-account
   * actions.
   */
  readonly signedIn?: boolean
  /** Fired once after the acceptance lands; the host navigates. */
  readonly onAccepted?: () => void
  /** Navigates to the host's account surface, where the address is verified;
   * the proof step renders its action only when this is supplied. */
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
   * Whether the current session is a member of the organization, told apart
   * from "this invitation was used"; `undefined` means not settled yet, so the
   * screen waits instead of guessing.
   */
  readonly isMemberOf?: (tenant: string) => boolean | undefined
  /**
   * The heading element the screen's titles render as; `'h1'` by default
   * (the screen is normally the page). The confirmation, terminal and proof
   * blocks render one level below it.
   */
  readonly headingLevel?: HeadingLevel
}

type ReadFailure =
  | { readonly kind: 'missing' }
  | { readonly kind: 'unavailable' }
  | { readonly kind: 'error'; readonly code: string }

const UNPROVED_EMAIL_CODE = 'authn.invitation_email_unproved'

const TENANT_INACTIVE_CODE = 'authn.invitation_tenant_inactive'

const MEMBERSHIP_INACTIVE_CODE = 'authn.membership_inactive'

const INVITATION_TERMINAL_CODE = 'authn.invitation_terminal'

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

/** The server's own verdict: a public read already terminal wins, and the later
 * eligibility read can move a pending link to terminal but never revive one.
 * Expiry is the server's, never derived from `expiresAt` locally. */
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

/** A bare 404 is the missing link, a 409 the moved state; anything else
 * renders its code. */
function classifyReadFailure(error: unknown): ReadFailure {
  if (isStatus(error, 404)) {
    return { kind: 'missing' }
  }
  if (isStatus(error, 409)) {
    return { kind: 'unavailable' }
  }
  return { kind: 'error', code: errorCodeOf(error) }
}

function organizationName(invitation: InvitationSummary): string {
  const displayName = invitation.tenantDisplayName?.trim() ?? ''
  return displayName === '' ? invitation.tenant : displayName
}

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

/**
 * Reads the invitation a token points to, pre-flights the proof step when the
 * host reports a session, and accepts on the viewer's action; acceptance never
 * creates an account.
 */
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
  const formatInstant = useFormatInstant()
  const [attempt, setAttempt] = useState(0)
  const [state, setState] = useState<ScreenState>({ status: 'loading' })
  const [accepting, setAccepting] = useState(false)
  const [acceptError, setAcceptError] = useState<string | null>(null)
  // The proof step replaces the accept action until a later attempt starts; a
  // retry refused the same way reopens it.
  const [unproved, setUnproved] = useState(false)
  // The explanation stands beside the accept action until the next attempt;
  // the invitation stays pending.
  const [tenantInactive, setTenantInactive] = useState(false)
  const proofRef = useStepFocus(unproved ? 'email-unproved' : null)

  // One read per attempt; a cancelled closure never writes state. The
  // eligibility read runs only for a signed-in host: its 401 is an anonymous
  // visitor, and any other failure leaves the proof decision to the backend.
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

  const accepted = state.status === 'loaded' && state.accepted

  // Focus moves to the completion heading only when the removal took it; a
  // control still in the document is where the operator moved focus.
  const completionHeading = useRef<HTMLHeadingElement>(null)
  useLayoutEffect(() => {
    if (!accepted) {
      return
    }
    const active = document.activeElement
    if (
      active !== null &&
      active !== document.body &&
      (!(active instanceof HTMLElement) || active.isConnected)
    ) {
      return
    }
    completionHeading.current?.focus()
  }, [accepted])

  const accept = (): void => {
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
          // A throwing host callback is not a failure of the acceptance.
        }
      },
      (error: unknown) => {
        setAccepting(false)
        const code = errorCodeOf(error)
        if (code === UNPROVED_EMAIL_CODE) {
          setUnproved(true)
          return
        }
        if (isStatus(error, 409)) {
          if (code === TENANT_INACTIVE_CODE) {
            // The organization is disabled, not the link, so a re-read would
            // answer pending again; explain it and keep the action.
            setTenantInactive(true)
            return
          }
          if (code === MEMBERSHIP_INACTIVE_CODE) {
            // The backend revoked the invitation as it refused; the refusal's
            // own sentence is the only remedy that exists.
            setAcceptError(code)
            return
          }
          if (code === INVITATION_TERMINAL_CODE) {
            // The link moved: re-read for the terminal state, keeping the code
            // in the banner in case the re-read still answers pending.
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

  const { invitation, eligibility } = state
  const judgedState = terminalStateOf(invitation, eligibility)

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
      // undefined means the host has not settled the fact: wait rather than
      // tell a member their own link was used.
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

      {/* Born empty while the landing is up: a live region announces only
          changes that follow its own insertion. */}
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
            {/* The focus landing for the unmounted action area: a
                programmatic target, not a tab stop. */}
            <Typography
              ref={completionHeading}
              component={nextHeadingLevel(headingLevel)}
              variant="h6"
              tabIndex={-1}
            >
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
              <ActionButton
                type="button"
                unavailable={accepting}
                onClick={onProveEmail}
                sx={{ alignSelf: 'flex-start' }}
              >
                {t('acceptInvitation.unproved.manageAccount')}
              </ActionButton>
            ) : null}
            <ActionButton
              type="button"
              emphasis="primary"
              busy={accepting}
              onClick={accept}
              sx={{ alignSelf: 'flex-start' }}
            >
              {t('acceptInvitation.unproved.retry')}
            </ActionButton>
          </Box>
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
          <ActionButton
            emphasis="primary"
            busy={accepting}
            onClick={accept}
            sx={{ alignSelf: 'flex-start' }}
          >
            {t('acceptInvitation.accept')}
          </ActionButton>
          <InlineError code={acceptError} />
        </Box>
      )}
    </Box>
  )
}

/** One key per combination of optional inviter and role, so no sentence is
 * built by concatenating translated fragments. */
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

/** A terminal state's title and explanation, static rather than a live region
 * because a screen reader reaches them on arrival. */
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
