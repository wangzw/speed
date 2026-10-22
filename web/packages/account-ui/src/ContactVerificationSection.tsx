/**
 * ContactVerificationSection: the registration contact-verification surface.
 *
 * A self-registration opens an account whose email address is only a contact
 * until it is proved. The account reads as `pending` on GET
 * /api/v1/auth/me's `verification` until an email address is proved, and the
 * server keeps the account's restricted capabilities closed until then. This
 * section is that state's face: it reads the same profile query the other
 * account surfaces read and renders one of four states.
 *
 *   - `none` (an account that never self-registered) renders nothing.
 *   - `verified` renders the completion state, naming the proved address.
 *   - `pending` with an address renders the target address, a send/resend
 *     action, the code field and the change-address action.
 *   - `pending` with no address renders the add-email form. An account can
 *     reach `pending` with no target after its last proved address is
 *     removed; the section then offers the only path that lifts the
 *     restriction again, so the add cannot live only on the identifiers
 *     section.
 *
 * Sending a code (and changing the target) is POST /api/v1/auth/identifiers
 * with `{kind: 'email', value}`, which answers `pending` and the resend
 * interval; verification is POST /api/v1/auth/identifiers/verify with the code
 * alone (the address is the server's session-keyed record). A landed verify
 * refetches the profile (so `verification` flips to `verified`) and the
 * session list, because the route revokes the account's other sessions.
 *
 * The section never distinguishes a free address from one another account
 * holds while sending: the answer is the same shape, and no "already
 * registered" copy exists. A conflict is rendered only after a code proves
 * control of the address, and its text names the address as another
 * account's and points at the change action.
 *
 * A 401 authn.insufficient_user_authentication arms the shared step-up gate
 * with the refused write remembered; a won challenge re-runs exactly that
 * write once. Other failures render through the family's one role="alert"
 * banner. A resend control is disabled for the interval the server asked for
 * (or the Retry-After a 429 carried) and states why, and the interval's timer
 * stops once it expires; the "code sent" notice is announced once through a
 * role="status" region that is mounted empty and filled on landing, and focus
 * moves to the code field.
 *
 * A resend addresses the same target the section displays (the address a send
 * in this mount landed on, else the profile's pending contact), so a resend
 * never goes to the address a change just replaced.
 *
 * The host can land the section with the change-address entry already open
 * (`initialChangeOpen`), which is what the banner's change action asks for;
 * its verify action lands with the entry closed and the code step showing.
 *
 * The section takes the session and that one optional flag as its props -- the
 * step-up dialog drives the session -- while the reads and writes travel on the
 * shared access token through the generated calls.
 */

import { useEffect, useRef, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  getAuthnSessionListQueryKey,
  useAuthnLoginAddIdentifier,
  useAuthnLoginVerifyIdentifier,
} from '@speed/api-sdk'
import type { AuthSession } from '@speed/auth-core'
import { useStepUpGate } from '@speed/auth-core'
import {
  errorCodeOf,
  FORM_MAX_WIDTH,
  SectionCard,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import { InlineError } from './internal/inline-error.js'
import { useProfileQuery } from './internal/queries.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'

const STEP_UP_REQUIRED = 'authn.insufficient_user_authentication'

/** The resend note's id, referenced by the resend control while it counts down. */
const RESEND_NOTE_ID = 'contact-verification-resend-note'

export interface ContactVerificationSectionProps {
  /** The session whose step-up flow the dialog drives. */
  readonly session: AuthSession
  /** Whether the change-address entry starts open, so a host landing here
   * from a "change address" action reaches the form directly. Defaults to
   * closed (the code step). */
  readonly initialChangeOpen?: boolean
}

/** The write a step-up dialog is armed for, so a won challenge re-runs
 * exactly it: the send (which also carries a changed address) or the code's
 * verification. */
type VerificationAction =
  | { readonly step: 'send'; readonly email: string }
  | { readonly step: 'verify'; readonly code: string }

/** The Retry-After a 429 carried, in milliseconds, read structurally so this
 * package stays independent of @speed/api-client's concrete error class. */
function retryAfterMsOf(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null) {
    return undefined
  }
  const value = (error as { retryAfterMs?: unknown }).retryAfterMs
  return typeof value === 'number' && value > 0 ? value : undefined
}

export function ContactVerificationSection({
  session,
  initialChangeOpen = false,
}: ContactVerificationSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, refetch } = useProfileQuery()
  const addIdentifier = useAuthnLoginAddIdentifier()
  const verifyIdentifier = useAuthnLoginVerifyIdentifier()

  // The address a code went to during this mount. Null until a send lands;
  // the target the section displays otherwise comes from the profile's
  // verification.email, so a change that replaced the pending address
  // converges on the server's answer.
  const [sentTo, setSentTo] = useState<string | null>(null)
  const [code, setCode] = useState('')
  const [codeInvalid, setCodeInvalid] = useState(false)
  // The failure's code and the step it came from: a conflict and a bad code
  // mean different things on the verify step than on the send step.
  const [failure, setFailure] = useState<{
    readonly code: string
    readonly step: 'send' | 'verify'
  } | null>(null)
  // The address being entered in the change / first-add form.
  const [emailEntry, setEmailEntry] = useState('')
  const [entryOpen, setEntryOpen] = useState(initialChangeOpen)
  // The instant the resend control becomes usable again, and the ticking
  // clock the note and the disabled state read.
  const [resendUntil, setResendUntil] = useState(0)
  const [now, setNow] = useState(() => Date.now())
  // The live region's text: emptied when an attempt starts and filled when a
  // send lands, so the announcement happens on the empty-to-filled edge.
  const [announcement, setAnnouncement] = useState('')
  const codeRef = useRef<HTMLInputElement>(null)

  const busy = addIdentifier.isPending || verifyIdentifier.isPending
  const verification = data?.verification
  const state = verification?.state ?? ''
  const targetEmail = verification?.email ?? ''

  const gate = useStepUpGate<VerificationAction>((action) => {
    if (action.step === 'send') {
      void send(action.email)
    } else {
      void verify(action.code)
    }
  })

  // Tick the countdown only while one is running, and stop the timer at its
  // own expiry rather than leaving it ticking for the rest of the mount.
  useEffect(() => {
    if (resendUntil <= Date.now()) {
      return undefined
    }
    const timer = setInterval(() => {
      const current = Date.now()
      setNow(current)
      if (current >= resendUntil) {
        clearInterval(timer)
      }
    }, 1000)
    return () => {
      clearInterval(timer)
    }
  }, [resendUntil])

  // Move focus to the code field once a send lands (the field exists before
  // the send, so the focus move is a real hand-off, not a timing race).
  useEffect(() => {
    if (sentTo !== null) {
      codeRef.current?.focus()
    }
  }, [sentTo])

  const remainingSeconds = Math.max(
    0,
    Math.ceil((resendUntil - now) / 1000),
  )

  /** Refetch what a landed write changes: the profile's verification state
   * and identifiers, and the session list, because the verify route revokes
   * the account's other sessions. */
  async function invalidateAfterWrite(): Promise<void> {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: getAuthnProfileGetQueryKey() }),
      queryClient.invalidateQueries({
        queryKey: getAuthnSessionListQueryKey(),
      }),
    ])
  }

  async function send(nextEmail: string): Promise<void> {
    const address = nextEmail.trim()
    setFailure(null)
    setCodeInvalid(false)
    setAnnouncement('')
    try {
      const result = await addIdentifier.mutateAsync({
        data: { kind: 'email', value: address },
      })
      const interval =
        typeof result.resend_after === 'number' ? result.resend_after : 0
      setResendUntil(interval > 0 ? Date.now() + interval * 1000 : 0)
      setNow(Date.now())
      setSentTo(address)
      setCode('')
      setEmailEntry('')
      setEntryOpen(false)
      setAnnouncement(
        t('verification.sent', {
          email: address,
          interpolation: { escapeValue: false },
        }),
      )
      // The pending contact may have been replaced: re-read so the target
      // label and the verification state converge on the server's answer.
      await invalidateAfterWrite()
    } catch (error) {
      const failureCode = errorCodeOf(error)
      if (failureCode === STEP_UP_REQUIRED) {
        gate.arm({ step: 'send', email: address })
        return
      }
      setFailure({ code: failureCode, step: 'send' })
      const retryAfter = retryAfterMsOf(error)
      if (retryAfter !== undefined) {
        setResendUntil(Date.now() + retryAfter)
        setNow(Date.now())
      }
    }
  }

  async function verify(nextCode: string): Promise<void> {
    setFailure(null)
    setCodeInvalid(false)
    setAnnouncement('')
    try {
      await verifyIdentifier.mutateAsync({ data: { code: nextCode } })
      setCode('')
      setSentTo(null)
      setResendUntil(0)
      await invalidateAfterWrite()
    } catch (error) {
      const failureCode = errorCodeOf(error)
      if (failureCode === STEP_UP_REQUIRED) {
        gate.arm({ step: 'verify', code: nextCode })
        return
      }
      if (failureCode === 'authn.login_failed') {
        setCodeInvalid(true)
        setCode('')
      }
      setFailure({ code: failureCode, step: 'verify' })
    }
  }

  // The read's own state: a settled answer with no body is the error variant,
  // never "nothing to verify".
  if (isPending && data === undefined) {
    return (
      <SectionCard title={t('verification.title')}>
        <Box aria-busy="true" sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
          <Skeleton variant="text" width="40%" />
          <Skeleton variant="text" width="70%" />
        </Box>
      </SectionCard>
    )
  }
  if (data === undefined) {
    return (
      <SectionCard title={t('verification.error.title')}>
        <Typography variant="body2" color="text.secondary">
          {t('verification.error.description')}
        </Typography>
        <Button
          variant="outlined"
          size="small"
          sx={{ mt: 1 }}
          onClick={() => void refetch()}
        >
          {t('verification.retry')}
        </Button>
      </SectionCard>
    )
  }
  if (state === '' || state === 'none') {
    return null
  }
  if (state === 'verified') {
    return (
      <SectionCard title={t('verification.verifiedTitle')}>
        <Alert severity="success" role="status">
          {targetEmail === ''
            ? t('verification.verifiedDescriptionNoEmail')
            : t('verification.verifiedDescription', {
                email: targetEmail,
                interpolation: { escapeValue: false },
              })}
        </Alert>
      </SectionCard>
    )
  }

  // The failure banner: the wrong-code and taken-address answers render this
  // section's own wording only on the verify step; every other code renders
  // through the family's whitelist.
  let failureBanner = null
  if (failure !== null) {
    if (failure.step === 'verify' && failure.code === 'authn.login_failed') {
      failureBanner = (
        <Alert severity="error" role="alert" sx={{ width: '100%' }}>
          {t('verification.codeInvalid')}
        </Alert>
      )
    } else if (failure.step === 'verify' && failure.code === 'authn.conflict') {
      failureBanner = (
        <Alert severity="error" role="alert" sx={{ width: '100%' }}>
          {t('verification.addressTaken')}
        </Alert>
      )
    } else {
      failureBanner = <InlineError code={failure.code} />
    }
  }

  const sentNotice = sentTo ?? targetEmail
  const hasTarget = targetEmail !== ''

  // A pending account with no target: the add-email form is the only way back
  // to a verifiable address.
  if (!hasTarget && sentTo === null) {
    return (
      <>
        <SectionCard
          title={t('verification.addTitle')}
          description={t('verification.addDescription')}
        >
          <Box
            component="form"
            noValidate
            onSubmit={(event) => {
              event.preventDefault()
              if (emailEntry.trim() !== '') {
                void send(emailEntry)
              }
            }}
            sx={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'flex-start',
              gap: 2,
              maxWidth: FORM_MAX_WIDTH,
            }}
          >
            <Alert
              severity="success"
              role="status"
              sx={announcement === '' ? visuallyHiddenSx : { width: '100%' }}
            >
              {announcement}
            </Alert>
            {failureBanner}
            <TextField
              fullWidth
              type="email"
              autoComplete="email"
              label={t('verification.addLabel')}
              value={emailEntry}
              disabled={busy}
              onChange={(event) => setEmailEntry(event.target.value)}
            />
            <Button
              type="submit"
              variant="contained"
              disabled={busy || emailEntry.trim() === ''}
              loading={addIdentifier.isPending}
            >
              {t('verification.addSubmit')}
            </Button>
          </Box>
        </SectionCard>
        <StepUpChallenge
          open={gate.open}
          session={session}
          onSuccess={gate.succeed}
          onCancel={gate.cancel}
        />
      </>
    )
  }

  return (
    <>
      <SectionCard
        title={t('verification.title')}
        description={t('verification.description')}
      >
        <Box
          component="form"
          noValidate
          onSubmit={(event) => {
            event.preventDefault()
            if (code.trim() !== '') {
              void verify(code)
            }
          }}
          sx={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'flex-start',
            gap: 2,
            maxWidth: FORM_MAX_WIDTH,
          }}
        >
          <Alert
            severity="success"
            role="status"
            sx={announcement === '' ? visuallyHiddenSx : { width: '100%' }}
          >
            {announcement}
          </Alert>
          {failureBanner}
          <Typography variant="body2" color="text.secondary">
            {sentNotice === ''
              ? t('verification.description')
              : t('verification.target', {
                  email: sentNotice,
                  interpolation: { escapeValue: false },
                })}
          </Typography>
          <TextField
            fullWidth
            inputMode="numeric"
            autoComplete="one-time-code"
            label={t('verification.codeLabel')}
            helperText={t('verification.codeHelper')}
            value={code}
            error={codeInvalid}
            slotProps={{ htmlInput: { 'aria-invalid': codeInvalid } }}
            inputRef={codeRef}
            disabled={busy}
            onChange={(event) => {
              setCode(event.target.value)
              if (codeInvalid) {
                setCodeInvalid(false)
              }
            }}
          />
          <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap' }}>
            <Button
              type="submit"
              variant="contained"
              disabled={busy || code.trim() === ''}
              loading={verifyIdentifier.isPending}
            >
              {t('verification.verify')}
            </Button>
            <Button
              type="button"
              variant="outlined"
              disabled={busy || remainingSeconds > 0}
              loading={addIdentifier.isPending}
              aria-describedby={
                remainingSeconds > 0 ? RESEND_NOTE_ID : undefined
              }
              onClick={() => void send(sentNotice)}
            >
              {sentTo === null
                ? t('verification.send')
                : t('verification.resend')}
            </Button>
            <Button
              type="button"
              disabled={busy}
              onClick={() => {
                setEntryOpen((open) => !open)
                setEmailEntry('')
                setFailure(null)
              }}
            >
              {entryOpen
                ? t('verification.changeCancel')
                : t('verification.change')}
            </Button>
          </Stack>
          {remainingSeconds > 0 ? (
            <Typography
              id={RESEND_NOTE_ID}
              variant="body2"
              color="text.secondary"
            >
              {t('verification.resendIn', { seconds: remainingSeconds })}
            </Typography>
          ) : null}
        </Box>
        {/* The change-address entry is its own form: inside the code form,
            pressing Enter in the address field would submit the code. */}
        {entryOpen ? (
          <Box
            component="form"
            noValidate
            onSubmit={(event) => {
              event.preventDefault()
              if (emailEntry.trim() !== '') {
                void send(emailEntry)
              }
            }}
            sx={{ mt: 2, maxWidth: FORM_MAX_WIDTH }}
          >
            <Stack
              direction={{ xs: 'column', sm: 'row' }}
              spacing={1}
              sx={{ alignItems: { sm: 'flex-start' }, width: '100%' }}
            >
              <TextField
                fullWidth
                type="email"
                autoComplete="email"
                label={t('verification.changeLabel')}
                value={emailEntry}
                disabled={busy}
                onChange={(event) => setEmailEntry(event.target.value)}
              />
              <Button
                type="submit"
                variant="contained"
                disabled={busy || emailEntry.trim() === ''}
              >
                {t('verification.changeSubmit')}
              </Button>
            </Stack>
          </Box>
        ) : null}
      </SectionCard>

      <StepUpChallenge
        open={gate.open}
        session={session}
        onSuccess={gate.succeed}
        onCancel={gate.cancel}
      />
    </>
  )
}
