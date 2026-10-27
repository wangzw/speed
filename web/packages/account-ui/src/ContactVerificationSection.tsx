/**
 * ContactVerificationSection: the account's email-verification surface.
 *
 * An account with an email address but no proved email address reads as
 * `pending` on GET /api/v1/auth/me's `verification` until an address is
 * proved, and the server keeps the account's restricted capabilities closed
 * until then -- however the address arrived (self-registration, administrator
 * provisioning or an import). This section is that state's face: it reads the
 * same profile query the other account surfaces read and renders one of four
 * states.
 *
 *   - `none` (an account with no unproved email address) renders nothing.
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
 * The verification also replaces this session's credential, so it runs
 * inside an identity-transition window (session.beginIdentityTransition):
 * a read that lands with the outgoing credential and answers anonymous is
 * discarded instead of ending the session, and the window closes once the
 * refetch that follows the write has settled. The send does not rotate the
 * credential and runs outside the window.
 *
 * The section never distinguishes a free address from one another account
 * holds while sending: the answer is the same shape, and no "already
 * registered" copy exists. A conflict is rendered only after a code proves
 * control of the address, and its text names the address as another
 * account's and points at the change action.
 *
 * A host that lands this section from a verification link sets `autoSend`:
 * once per mount, when the account is `pending` with a target address and the
 * change-address form is closed, the section sends the code itself so the
 * visitor arrives with one already on its way. The demo host mounts the
 * section on the account page and sets the flag only for a `?verify=1`
 * landing, so an ordinary account visit sends nothing. A mount that
 * lands with the form already open (`initialChangeOpen`, the
 * `?change=email` entry) never sends automatically, even after the form is
 * closed: that visitor arrived to replace the address, and a code for the
 * old one is not what they asked for. The automatic
 * send never arms the step-up gate -- a gate needs the visitor's own click --
 * so a 401 leaves the manual send action in place with an explanation, and
 * that click then takes the ordinary gated path. A 429 still starts the
 * resend countdown, and any other failure renders the failure banner.
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
 * The section takes the session and the two optional flags as its props -- the
 * step-up dialog drives the session -- while the reads and writes travel on the
 * shared access token through the generated calls.
 *
 * A send also presents the deployment's human-verification proof when its
 * gate requires one. The gate comes from the methods document (or from a
 * `humanGate` a host already read), and the block stands above the send
 * controls; the send waits for a proof, and a resend obtains a fresh one
 * because the previous proof was spent. The automatic landing send has no
 * visitor's click to obtain a proof with, so it waits for the block to
 * hold one before it goes, and stays a manual action otherwise.
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
import type { AuthSession, AuthnHumanGate } from '@speed/auth-core'
import { humanGateNeedsProof, stepUpReasonOf, useStepUpGate, useHumanVerification } from '@speed/auth-core'
import {
  errorCodeOf,
  FORM_MAX_WIDTH,
  HumanVerification,
  SectionCard,
  useWriteFeedback,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { runIdentityTransition } from './internal/identity-transition.js'
import { useProfileQuery } from './internal/queries.js'
import { retryAfterMsOf } from './internal/retry-after.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

/** The resend note's id, referenced by the resend control while it counts down. */
const RESEND_NOTE_ID = 'contact-verification-resend-note'

export interface ContactVerificationSectionProps {
  /** The session whose step-up flow the dialog drives. */
  readonly session: AuthSession
  /** Whether the change-address entry starts open, so a host landing here
   * from a "change address" action reaches the form directly. Defaults to
   * closed (the code step). A mount that starts open never auto-sends,
   * even after the form is closed: the visitor arrived to change the
   * address, so the old one gets no code from this mount. */
  readonly initialChangeOpen?: boolean
  /** Whether a pending account with a target address sends a code once when
   * the section mounts, so a verification landing opens with one already on
   * its way. Defaults to false; the account page turns it on only for a
   * `?verify=1` landing and leaves an ordinary visit quiet. The automatic
   * send never opens the step-up dialog: a refused send leaves the manual
   * action and its explanation. */
  readonly autoSend?: boolean
  /**
   * The human-verification gate the deployment reported, when the host
   * already read the discovery document. Omitted, the section reads the
   * methods document itself.
   */
  readonly humanGate?: AuthnHumanGate
}

/** The write a step-up dialog is armed for, so a won challenge re-runs
 * exactly it: the send (which also carries a changed address) or the code's
 * verification. */
type VerificationAction =
  | { readonly step: 'send'; readonly email: string }
  | { readonly step: 'verify'; readonly code: string }

export function ContactVerificationSection({
  session,
  initialChangeOpen = false,
  autoSend = false,
  humanGate,
}: ContactVerificationSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useProfileQuery()
  const addIdentifier = useAuthnLoginAddIdentifier()
  const verifyIdentifier = useAuthnLoginVerifyIdentifier()
  // The deployment's human-verification gate and this section's proof. The
  // send (a resend included) presents it; the code's verification sends
  // nothing, so it does not.
  const human = useHumanVerification(
    session,
    humanGate === undefined ? {} : { gate: humanGate },
  )
  const needsProof = humanGateNeedsProof(human.gate)
  // Only a known gate that requires a proof blocks a manual send. The
  // automatic send additionally waits for the read to settle (see its
  // effect), because it has no visitor's click to obtain a proof with.
  const proofBlocked = needsProof && human.proof === null

  // The address a code went to during this mount. Null until a send lands;
  // the target the section displays otherwise comes from the profile's
  // verification.email, so a change that replaced the pending address
  // converges on the server's answer.
  const [sentTo, setSentTo] = useState<string | null>(null)
  // The sent notice's live region must never mount together with its text:
  // the region is mounted from the first render, and its text fills one
  // commit after a send lands, so the announcement is a content change the
  // screen reader already knows.
  const [sentCommitted, setSentCommitted] = useState(false)
  const [code, setCode] = useState('')
  const [codeInvalid, setCodeInvalid] = useState(false)
  // Send and verify are two control groups: each owns its own feedback scope
  // and region, so one group's outcome never clears the other's.
  const sendFeedback = useWriteFeedback()
  const verifyFeedback = useWriteFeedback()
  // The verify group's non-code failure source: the wrong-code and
  // taken-address answers render the section's own wording on the verify
  // step. It occupies the verify region's result position and is mutually
  // exclusive with that region's code failure.
  const [verifyFailureText, setVerifyFailureText] = useState<string | null>(
    null,
  )
  // The address being entered in the change / first-add form.
  const [emailEntry, setEmailEntry] = useState('')
  const [entryOpen, setEntryOpen] = useState(initialChangeOpen)
  // The instant the resend control becomes usable again, and the ticking
  // clock the note and the disabled state read.
  const [resendUntil, setResendUntil] = useState(0)
  const [now, setNow] = useState(() => Date.now())
  // Whether the mount's automatic send was refused for step-up. The dialog
  // is the visitor's to open, so the section states why it could not send
  // and leaves the manual action in place.
  const [autoStepUp, setAutoStepUp] = useState(false)
  // The mount-local guards for the automatic send: one attempt per mount,
  // however many times a refetch re-renders the section, and no attempt at
  // all when the host landed with the change form open (the `?change=email`
  // entry). That second guard is fixed at mount: closing the form later
  // does not turn the automatic send back on, because the visitor arrived
  // to change the address, not to receive a code for the old one.
  const autoSent = useRef(false)
  const autoSendSuppressed = useRef(initialChangeOpen)
  const codeRef = useRef<HTMLInputElement>(null)

  // Disabled through the whole attempt, including the refetches that follow
  // the mutation, so no control changes context under a pending scope.
  const busy =
    addIdentifier.isPending ||
    verifyIdentifier.isPending ||
    sendFeedback.pending ||
    verifyFeedback.pending
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

  // Arm the sent notice's live region one commit later so it fills an
  // already-mounted region, and request the focus hand-off when the send lands.
  // The field exists before the send, so the hand-off is a real move rather
  // than a timing race -- but the send scope keeps the controls disabled until
  // it settles (including the refetches that follow the mutation), and a
  // disabled input cannot take focus. The request is therefore discharged by
  // the first commit in which the scope is no longer busy, not here.
  const focusCodeWhenSettled = useRef(false)
  useEffect(() => {
    setSentCommitted(sentTo !== null)
    if (sentTo !== null) {
      focusCodeWhenSettled.current = true
    }
  }, [sentTo])

  useEffect(() => {
    if (!focusCodeWhenSettled.current || busy) {
      return
    }
    focusCodeWhenSettled.current = false
    codeRef.current?.focus()
  }, [busy, sentTo])

  const remainingSeconds = Math.max(
    0,
    Math.ceil((resendUntil - now) / 1000),
  )

  /** Refetch what a landed write changes: the profile's verification state
   * and identifiers, and the session list, because proving the address
   * revokes the account's other sessions. */
  async function invalidateAfterWrite(): Promise<void> {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: getAuthnProfileGetQueryKey() }),
      queryClient.invalidateQueries({
        queryKey: getAuthnSessionListQueryKey(),
      }),
    ])
  }

  function send(nextEmail: string, automatic = false): Promise<void> {
    // A send waits for the gate and for a required proof. The automatic
    // send's effect re-runs when the proof arrives, so it proceeds then
    // instead of sending blind.
    if (proofBlocked) {
      return Promise.resolve()
    }
    const address = nextEmail.trim()
    setCodeInvalid(false)
    setAutoStepUp(false)
    return sendFeedback.run(async () => {
      const presented = needsProof ? human.proof : null
      let result
      try {
        result = await addIdentifier.mutateAsync({
          data:
            presented === null
              ? { kind: 'email', value: address }
              : { kind: 'email', value: address, human_proof: presented },
        })
      } catch (error) {
        const failureCode = errorCodeOf(error)
        const reason = stepUpReasonOf(failureCode)
        if (reason !== null) {
          // A step-up refusal precedes the proof check, so the proof is
          // still valid for the re-run.
          // A dialog the visitor did not ask for is not opened for them: the
          // automatic attempt states what happened instead, and the next
          // click on the send action takes the ordinary gated path.
          if (automatic) {
            setAutoStepUp(true)
            return null
          }
          gate.arm({ step: 'send', email: address }, reason)
          return null
        }
        // Any other refusal reached the send's own checks, which spend the
        // single-use proof on the way: obtain a fresh one.
        if (presented !== null) {
          human.consume()
        }
        const retryAfter = retryAfterMsOf(error)
        if (retryAfter !== undefined) {
          setResendUntil(Date.now() + retryAfter)
          setNow(Date.now())
        }
        throw error
      }
      if (presented !== null) {
        human.consume()
      }
      const interval =
        typeof result.resend_after === 'number' ? result.resend_after : 0
      setResendUntil(interval > 0 ? Date.now() + interval * 1000 : 0)
      setNow(Date.now())
      setSentTo(address)
      setCode('')
      setEmailEntry('')
      setEntryOpen(false)
      // The pending contact may have been replaced: re-read so the target
      // label and the verification state converge on the server's answer.
      await invalidateAfterWrite()
      // The "code sent" destination text is the code step's own state, not a
      // settled action: it is announced once through its own live region and
      // leaves the send scope's slot empty.
      return null
    })
  }

  function verify(nextCode: string): Promise<void> {
    setCodeInvalid(false)
    return verifyFeedback.run(async () => {
      setVerifyFailureText(null)
      try {
        // The verification writes the address with its proof and rotates
        // this session's credential, so the window is open before the write
        // and closes once the refetch that follows it has settled: a read
        // still carrying the outgoing credential is discarded instead of
        // ending the session.
        await runIdentityTransition(
          session,
          () => verifyIdentifier.mutateAsync({ data: { code: nextCode } }),
          async () => {
            setCode('')
            setSentTo(null)
            setResendUntil(0)
            await invalidateAfterWrite()
          },
        )
      } catch (error) {
        const failureCode = errorCodeOf(error)
        const reason = stepUpReasonOf(failureCode)
        if (reason !== null) {
          gate.arm({ step: 'verify', code: nextCode }, reason)
          return null
        }
        if (failureCode === 'authn.login_failed') {
          setCodeInvalid(true)
          setCode('')
          setVerifyFailureText('verification.codeInvalid')
          return null
        }
        if (
          failureCode === 'authn.conflict' ||
          failureCode === 'authn.email_taken'
        ) {
          setVerifyFailureText('verification.addressTaken')
          return null
        }
        throw error
      }
      // The section re-reads into `verified`, whose completion state names
      // the proved address: that is the verification's success.
      return null
    })
  }

  // The verification landing's single automatic send: it fires when the
  // profile first settles on a pending target with the change form closed,
  // and the mount-local refs keep every later render, refetch and
  // form close from sending.
  useEffect(() => {
    if (
      !autoSend ||
      autoSent.current ||
      autoSendSuppressed.current ||
      entryOpen
    ) {
      return
    }
    if (state !== 'pending' || targetEmail === '') {
      return
    }
    // An automatic send has no visitor's click to obtain a proof with; it
    // waits until the gate is known and the block has one, then goes.
    if (human.pending || proofBlocked) {
      return
    }
    autoSent.current = true
    void send(targetEmail, true)
  }, [autoSend, entryOpen, state, targetEmail, send, human.pending, proofBlocked])

  // A settled answer with no body is the error variant, never "nothing to
  // verify" (the async section below renders the retry). A settled state
  // carrying no verification renders nothing at all.
  if (data !== undefined && (state === '' || state === 'none')) {
    return null
  }

  // The verify group's non-code failure, beside its region and mutually
  // exclusive with it: the wrong-code and taken-address answers render this
  // section's own wording, every other code through the whitelist.
  const verifyFailureBanner =
    verifyFailureText === null ? null : (
      <Alert severity="error" role="alert" sx={{ width: '100%' }}>
        {t(verifyFailureText)}
      </Alert>
    )

  const sentNotice = sentTo ?? targetEmail
  const hasTarget = targetEmail !== ''

  // The deployment's human-verification block, above the send control the
  // current face shows. It renders nothing when no proof is required.
  const humanBlock = (
    <HumanVerification
      gate={human.gate ?? undefined}
      proof={human.proof}
      status={human.status}
      onToken={human.adoptToken}
      onRetry={human.retry}
      disabled={busy}
    />
  )

  // The settled verification states. The read's pending and failure
  // branches belong to the async section below, so a failed refresh over
  // already rendered content keeps that content under its stale notice.
  const verifiedCard = (
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

  // A pending account with no target: the add-email form is the only way back
  // to a verifiable address.
  const addForm = (
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
        <TextField
          fullWidth
          type="email"
          autoComplete="email"
          label={t('verification.addLabel')}
          value={emailEntry}
          disabled={busy}
          onChange={(event) => setEmailEntry(event.target.value)}
        />
        {humanBlock}
        <Button
          type="submit"
          variant="contained"
          disabled={busy || proofBlocked || emailEntry.trim() === ''}
          loading={addIdentifier.isPending}
        >
          {t('verification.addSubmit')}
        </Button>
        {/* The add form is the send group's control here: its result
            sits below the action that made it. */}
        <WriteFeedbackRegion
          outcome={sendFeedback.outcome}
          sx={{ alignSelf: 'stretch', width: '100%' }}
        />
      </Box>
    </SectionCard>
  )

  const verificationForm = (
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
          {autoStepUp ? (
            <Alert severity="info" sx={{ width: '100%' }}>
              {t('verification.autoStepUp')}
            </Alert>
          ) : null}
          <Typography variant="body2" color="text.secondary">
            {sentNotice === ''
              ? t('verification.description')
              : t('verification.target', {
                  email: sentNotice,
                  interpolation: { escapeValue: false },
                })}
          </Typography>
          {/* The sent notice's live region: mounted from the first render,
              filled one commit after a code lands, so it announces the
              destination once without becoming a second result in the send
              scope's slot. */}
          <Box role="status" sx={visuallyHiddenSx}>
            {sentCommitted && sentTo !== null
              ? t('verification.sent', {
                  email: sentTo,
                  interpolation: { escapeValue: false },
                })
              : ''}
          </Box>
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
          {/* The verify control group: its result sits below the code field
              and the verify action. */}
          <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap' }}>
            <Button
              type="submit"
              variant="contained"
              disabled={busy || code.trim() === ''}
              loading={verifyIdentifier.isPending}
            >
              {t('verification.verify')}
            </Button>
          </Stack>
          <WriteFeedbackRegion
            outcome={verifyFeedback.outcome}
            sx={{ alignSelf: 'stretch', width: '100%' }}
          />
          {verifyFailureBanner}
          {/* The send control group: its own result sits below the resend and
              change actions. The proof block stands above them, because a
              resend sends another code. */}
          {humanBlock}
          <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap' }}>
            <Button
              type="button"
              variant="outlined"
              disabled={busy || proofBlocked || remainingSeconds > 0}
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
                disabled={busy || proofBlocked || emailEntry.trim() === ''}
              >
                {t('verification.changeSubmit')}
              </Button>
            </Stack>
          </Box>
        ) : null}
        {/* The send control group's result, below every control that writes
            into it -- the resend action above and the change-address form
            just above -- so a refused send or change renders under the form
            that made it. */}
        <WriteFeedbackRegion
          outcome={sendFeedback.outcome}
          sx={{ alignSelf: 'stretch', width: '100%' }}
        />
      </SectionCard>
    </>
  )

  return (
    <>
      <AccountAsyncSection
        pending={isPending && data === undefined}
        payload={data}
        empty={false}
        error={error}
        refreshing={isFetching}
        loading={
          <SectionCard title={t('verification.title')}>
            <Box
              aria-busy="true"
              sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}
            >
              <Skeleton variant="text" width="40%" />
              <Skeleton variant="text" width="70%" />
            </Box>
          </SectionCard>
        }
        errorState={{
          title: t('verification.error.title'),
          description: t('verification.error.description'),
          retryLabel: t('verification.retry'),
          onRetry: () => void refetch(),
        }}
      >
        {() =>
          state === 'verified' ? (
            verifiedCard
          ) : !hasTarget && sentTo === null ? (
            addForm
          ) : (
            verificationForm
          )
        }
      </AccountAsyncSection>

      <StepUpChallenge
        open={gate.open}
        reason={gate.reason}
        session={session}
        onSuccess={gate.succeed}
        onCancel={gate.cancel}
      />
    </>
  )
}
