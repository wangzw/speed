/**
 * PasswordResetScreen: the anonymous password reset surface.
 *
 * The screen drives the host-bound `PasswordResetOperations`, not a
 * session: the host maps the deployment's published reset channels from
 * the discovery document into `channels`, binds the generated reset
 * operations, and navigates on `onDone` / `onBackToSignIn`. The session
 * prop is used only for the deployment's human-verification gate and
 * proof, because a send may have to present one. The block appears only
 * once the viewer asks for a send the gate holds for a proof, and that send
 * leaves by itself when the proof arrives; a send waiting on its proof
 * holds back the control that asked, while the back-to-sign-in exit stays
 * reachable.
 *
 * The surface has four states. In `request` the viewer picks one of the
 * published channels, enters that channel's identifier and asks for a
 * code. In `sent` the code, the new password and the confirmation are
 * shown together: one submit verifies the code and then writes the
 * password, so the server's two requests stay behind one action. In
 * `verified` -- a resumed proved ticket, or the state a policy refusal
 * leaves behind -- only the password and confirmation remain, and a
 * retry writes no code again. `done` reports the settled reset and hands
 * the viewer back. A `currentReset` operation lets a page reload resume
 * the bound flow; a sent record names no channel or identifier, so a
 * resumed surface offers no resend.
 *
 * A sent notice names no account and no address: it says a code was sent
 * if the details match an account that can be reset, which is the same
 * sentence for every identifier. A wrong or expired code, a suppressed
 * record and an exhausted attempt budget all answer one collapsed
 * refusal, which the code field carries. A password-policy refusal stays
 * on the verified surface with the new-password field, and a closed
 * deployment, an unavailable dependency or an invalidated flow render
 * through the family's error banner. A stated Retry-After wait disables
 * the send control until it elapses and counts down in the banner; once it
 * does, the control re-enables and the banner stays with the wait-free
 * sentence. The resend control states its own remaining interval.
 * Switching channels in the request state drops the
 * previous channel's whole-attempt failure, its stated wait and its field
 * errors, so no channel's error crosses to the next face.
 *
 * Leaving through `onBackToSignIn` abandons an in-flight record through
 * `abandonReset` (best-effort: a failed abandon does not keep the viewer
 * on the surface). A flow-invalid or account-unavailable refusal offers a
 * start-over exit, which does the same and returns to the request state.
 */

import { useEffect, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Alert from '@mui/material/Alert'
import TextField from '@mui/material/TextField'
import ToggleButton from '@mui/material/ToggleButton'
import ToggleButtonGroup from '@mui/material/ToggleButtonGroup'
import Typography from '@mui/material/Typography'
import { useForm } from 'react-hook-form'
import type { SubmitHandler } from 'react-hook-form'
import {
  ActionButton,
  FormField,
  FormLayout,
  HumanVerification as HumanVerificationBlock,
  PhoneNumberField,
  errorCodeOf,
  toE164,
  usePhoneCountryDefault,
} from '@speed/ui-kit'
import {
  humanGateNeedsProof,
  useHumanVerification,
} from '@speed/auth-core'
import type {
  AuthSession,
  AuthnHumanGate,
  PasswordResetChannel,
} from '@speed/auth-core'
import { useAuthUiTranslation } from './internal/translation.js'
import { InlineError } from './internal/inline-error.js'
import type { HeadingLevel } from './internal/heading.js'
import { retryWaitMs } from './internal/retry-wait.js'

/** One reset request: the channel, its identifier, and the human proof a
 * gated send presents. */
export interface PasswordResetRequest {
  readonly channel: PasswordResetChannel
  readonly identifier: string
  readonly humanProof?: string
}

/** The answer to a code request: how long the code lives and how soon
 * another may be asked for, both in whole seconds. */
export interface PasswordResetSent {
  readonly state: 'sent'
  readonly expiresIn: number
  readonly resendAfter: number
}

/** The answer to a proved code: how long the proved ticket lives, in
 * whole seconds. */
export interface PasswordResetVerified {
  readonly state: 'verified'
  readonly expiresIn: number
}

/**
 * The reset operations the screen drives. The host binds them to the
 * generated @speed/api-sdk reset operations; auth-ui ships no HTTP of its
 * own and references no api-sdk type on its public surface. `currentReset`
 * and `abandonReset` are optional: a host without them gets a surface
 * that starts fresh and cannot resume or abandon server-side.
 */
export interface PasswordResetOperations {
  /** Sends a code, or a suppressed record, for the identifier
   * (POST /api/v1/auth/password-reset). */
  readonly requestReset: (input: PasswordResetRequest) => Promise<PasswordResetSent>
  /** Proves the code held for the bound record
   * (POST /api/v1/auth/password-reset/verify). */
  readonly verifyReset: (input: { readonly code: string }) => Promise<PasswordResetVerified>
  /** Writes the new password for the proved record
   * (POST /api/v1/auth/password-reset/complete). */
  readonly completeReset: (input: {
    readonly newPassword: string
  }) => Promise<void>
  /** Reads the bound record on mount
   * (GET /api/v1/auth/password-reset/current). */
  readonly currentReset?: () => Promise<PasswordResetSent | PasswordResetVerified>
  /** Drops the bound record (DELETE /api/v1/auth/password-reset/current). */
  readonly abandonReset?: () => Promise<void>
}

export interface PasswordResetScreenProps {
  /** The session the human-verification gate and proof come from. */
  readonly session: AuthSession
  /** The reset channels the deployment publishes, in its order. An empty
   * list renders the unavailable notice. */
  readonly channels: readonly PasswordResetChannel[]
  /** The host-bound reset operations. */
  readonly operations: PasswordResetOperations
  /**
   * The human-verification gate the host already read, when it has one.
   * Omitted, the screen reads it through the session before a send.
   */
  readonly humanGate?: AuthnHumanGate
  /** The heading element the screen's titles render as; `'h1'` by default
   * (the screen is normally the page). */
  readonly headingLevel?: HeadingLevel
  /** Fired once after the password write lands; the host navigates. */
  readonly onDone?: () => void
  /** Fired when the viewer asks to return to the sign-in surface. The
   * screen abandons an in-flight record first. */
  readonly onBackToSignIn?: () => void
}

type Stage =
  | { readonly kind: 'checking' }
  | { readonly kind: 'request' }
  | {
      readonly kind: 'sent'
      readonly channel: PasswordResetChannel | null
      readonly identifier: string | null
    }
  | { readonly kind: 'verified' }
  | { readonly kind: 'done' }

/** One whole-attempt failure: a reachable problem code, or a sentence the
 * screen words itself (the invalidated flow). A throttle is the code with
 * the wait the answer stated, so the banner counts it down.
 * `restart` marks the refusals after which the bound record cannot
 * continue, so the surface offers a start-over exit with the banner. */
interface Failure {
  readonly kind: 'code' | 'text'
  readonly value: string
  readonly restart: boolean
  /** The wait a throttled answer stated, in whole seconds: the stable
   * sentence assistive technology reads while the visible one counts down. */
  readonly retryAfterSeconds?: number
}

interface ResetFields {
  identifier: string
  code: string
  newPassword: string
  confirmPassword: string
}

/** The collapsed wrong-code refusal: a wrong or expired code, a suppressed
 * record and an exhausted attempt budget all answer under this one code. */
const LOGIN_FAILED = 'authn.login_failed'

/** The password write's only 400 is the password policy. */
const BAD_REQUEST = 'authn.bad_request'

/** The code a throttled send answers with: its sentence names the wait the
 * response stated, and stays as the generic sentence once the wait ends. */
const THROTTLED = 'authn.throttled'

/** The failure value that marks an invalidated flow, whose dedicated text
 * is the reset surface's own (the generic flow-invalid sentence speaks of
 * a sign-in attempt). */
const FLOW_INVALID_TEXT = 'flow-invalid'

/** The ui-kit namespace key FormField renders for a missing required
 * value; the screen sets it on the values its own trim would empty. */
const REQUIRED_KEY = 'form.required'

/** The refusals after which the bound record cannot continue and a
 * start-over exit is offered. The invalidated flow is worded separately
 * (see FLOW_INVALID_TEXT) and carries the same exit. */
const RESTART_CODES = new Set<string>(['authn.password_reset_unavailable'])

export function PasswordResetScreen({
  session,
  channels,
  operations,
  humanGate,
  headingLevel = 'h1',
  onDone,
  onBackToSignIn,
}: PasswordResetScreenProps) {
  const { t } = useAuthUiTranslation()
  const form = useForm<ResetFields>({
    defaultValues: {
      identifier: '',
      code: '',
      newPassword: '',
      confirmPassword: '',
    },
  })
  // The SMS channel takes the identifier the same way the SMS sign-in does:
  // a country/region selector and the local number, assembled to E.164 only
  // at submit. The country starts on the host's preferred one when it gives
  // one, else the browser locale's own region.
  const preferredCountry = usePhoneCountryDefault()
  const [country, setCountry] = useState(() => preferredCountry)
  const [nationalNumber, setNationalNumber] = useState('')
  const [phoneAttempted, setPhoneAttempted] = useState(false)
  // The resume read is captured once at mount: a host that binds the
  // operations inline hands a new function identity on every render, and
  // re-running the resume on each one would read the bound record again
  // after every field change.
  const [resume] = useState(() => operations.currentReset)
  const initialStage: Stage =
    resume === undefined ? { kind: 'request' } : { kind: 'checking' }
  const [stage, setStage] = useState<Stage>(initialStage)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<Failure | null>(null)
  const [retryUntil, setRetryUntil] = useState<number | null>(null)
  const [resendAt, setResendAt] = useState<number | null>(null)
  // The tick the resend countdown re-renders on; it is only a clock, never
  // a second source of the deadline.
  const [now, setNow] = useState(() => Date.now())
  // The tick the throttle banner re-renders on: also only a clock.
  const [, setRetryTick] = useState(0)
  const [chosenChannel, setChosenChannel] =
    useState<PasswordResetChannel | null>(channels[0] ?? null)
  const identifierInput = useRef<HTMLInputElement>(null)
  const codeInput = useRef<HTMLInputElement>(null)
  const newPasswordInput = useRef<HTMLInputElement>(null)
  // The published channel list is the deployment's: a choice that is no
  // longer offered falls back to the first one.
  const selectedChannel =
    chosenChannel !== null && channels.includes(chosenChannel)
      ? chosenChannel
      : (channels[0] ?? null)
  const sms = selectedChannel === 'sms'
  // The send the viewer asked for, waiting on the gate and the proof that
  // send needs: the channel and the identifier it will send to. The gate is
  // read and the proof obtained only while this is set, so the request form
  // on its own shows no challenge. Both the first send and a resend wait
  // here.
  const [pendingSend, setPendingSend] = useState<{
    readonly channel: PasswordResetChannel
    readonly identifier: string
  } | null>(null)
  // One send at a time: the direct path and the deferred path that waits
  // for a proof can both reach the send in the same tick.
  const sendingRef = useRef(false)
  // The channel names the surface: SMS reaches a phone number and runs the
  // phone surface's mechanism, an email code runs the other surface's.
  const humanScope = sms ? 'phone' : 'other'
  const human = useHumanVerification(session, {
    scope: humanScope,
    // The gate is read and the proof obtained only once the viewer asks for
    // a send the gate holds; the request form alone reads and solves
    // nothing.
    enabled: pendingSend !== null,
    ...(humanGate === undefined ? {} : { gate: humanGate }),
  })
  // A gate the host already read is known before the hook reads anything; a
  // screen without one learns it through the hook once a send asks for it.
  const gateDocument = humanGate ?? human.gate
  const needsProof = humanGateNeedsProof(gateDocument, humanScope)

  // Resume the bound record once per mounted resume operation. A refused
  // read (no binding, an expired or failed record) starts the surface
  // fresh; the record's own state is the only thing read.
  useEffect(() => {
    if (resume === undefined) {
      return undefined
    }
    let cancelled = false
    resume().then(
      (state) => {
        if (cancelled) {
          return
        }
        setStage(
          state.state === 'sent'
            ? {
                kind: 'sent',
                channel: null,
                identifier: null,
              }
            : { kind: 'verified' },
        )
      },
      () => {
        if (!cancelled) {
          setStage({ kind: 'request' })
        }
      },
    )
    return () => {
      cancelled = true
    }
  }, [resume])

  // A stated Retry-After wait counts down on its own: one tick a second
  // re-renders the banner while the deadline is ahead, and the tick that
  // crosses it clears the deadline so the send control re-enables. The
  // failure itself stays, so the banner remains with the wait-free sentence
  // instead of vanishing.
  useEffect(() => {
    if (retryUntil === null) {
      return undefined
    }
    if (Date.now() >= retryUntil) {
      setRetryUntil(null)
      return undefined
    }
    const timer = setInterval(() => {
      if (Date.now() >= retryUntil) {
        setRetryUntil(null)
        clearInterval(timer)
        return
      }
      setRetryTick((tick) => tick + 1)
    }, 1000)
    return () => clearInterval(timer)
  }, [retryUntil])

  // The resend countdown ticks while its deadline is ahead and stops at
  // it. An interval (rather than a self-rescheduling timeout) keeps the
  // clock moving under a test's advanced fake clock, where a re-render
  // only happens after the batch of timer callbacks.
  useEffect(() => {
    if (resendAt === null) {
      return undefined
    }
    const timer = setInterval(() => {
      const tick = Date.now()
      setNow(tick)
      if (tick >= resendAt) {
        clearInterval(timer)
      }
    }, 1000)
    return () => clearInterval(timer)
  }, [resendAt])

  const retryRemaining =
    retryUntil === null ? 0 : Math.max(0, retryUntil - Date.now())
  const waiting = retryRemaining > 0
  // The seconds the shared banner paints: 0 once the deadline is cleared,
  // so the visible sentence drops its number while the failure remains.
  const liveRetryAfterSeconds =
    retryUntil === null
      ? 0
      : Math.max(0, Math.ceil((retryUntil - Date.now()) / 1000))
  const resendRemaining =
    resendAt === null ? 0 : Math.max(0, Math.ceil((resendAt - now) / 1000))

  const abandon = (): void => {
    try {
      const pending = operations.abandonReset?.()
      void pending?.catch(() => undefined)
    } catch {
      // Leaving is best-effort: a failed abandon does not keep the viewer
      // on the surface.
    }
  }

  const fail = (error: unknown): void => {
    const code = errorCodeOf(error)
    if (code === 'authn.flow_invalid') {
      setFailure({ kind: 'text', value: FLOW_INVALID_TEXT, restart: true })
      return
    }
    setFailure({
      kind: 'code',
      value: code,
      restart: RESTART_CODES.has(code),
    })
  }

  const waitOrFail = (error: unknown): void => {
    const wait = retryWaitMs(error)
    if (wait !== undefined) {
      setRetryUntil(Date.now() + wait)
      setFailure({
        kind: 'code',
        value: THROTTLED,
        restart: false,
        retryAfterSeconds: Math.max(1, Math.ceil(wait / 1000)),
      })
      return
    }
    if (errorCodeOf(error) === BAD_REQUEST && stage.kind === 'request') {
      form.setError('identifier', {
        type: 'server',
        message: t('passwordReset.invalidIdentifier'),
      })
      identifierInput.current?.focus()
      return
    }
    fail(error)
  }

  const send = async (
    channel: PasswordResetChannel,
    identifier: string,
  ): Promise<void> => {
    if (sendingRef.current) {
      return
    }
    setFailure(null)
    form.clearErrors()
    const proof = needsProof ? (human.proof ?? undefined) : undefined
    if (needsProof && proof === undefined) {
      // A required proof that is not ready yet keeps the send from leaving:
      // the block above the control is already showing why.
      return
    }
    sendingRef.current = true
    setBusy(true)
    try {
      const pending = operations.requestReset({
        channel,
        identifier,
        ...(proof === undefined ? {} : { humanProof: proof }),
      })
      if (proof !== undefined) {
        // A proof is single-use: the server consumed it for this send, so
        // the block obtains the next one whether the send is accepted or
        // refused.
        human.consume()
      }
      const sent = await pending
      setRetryUntil(null)
      setResendAt(Date.now() + sent.resendAfter * 1000)
      setNow(Date.now())
      setStage({
        kind: 'sent',
        channel,
        identifier,
      })
    } catch (error) {
      waitOrFail(error)
    } finally {
      sendingRef.current = false
      setBusy(false)
    }
  }

  const sendRef = useRef(send)
  sendRef.current = send

  /**
   * Ask for a code: send now when the gate is known to want no proof,
   * otherwise hold the send and let the block obtain one; the effect below
   * sends the postponed request when the proof arrives. A new attempt
   * clears the previous failure and field errors here, whichever path it
   * takes.
   */
  const askToSend = (
    channel: PasswordResetChannel,
    identifier: string,
  ): void => {
    if (pendingSend !== null) {
      // A send is already waiting on its proof; asking again changes nothing.
      return
    }
    setFailure(null)
    form.clearErrors()
    if (
      !human.pending &&
      gateDocument !== null &&
      (!needsProof || human.proof !== null)
    ) {
      void send(channel, identifier)
      return
    }
    setPendingSend({ channel, identifier })
  }

  // A send asked for while the gate held it leaves as soon as the gate is
  // known and the proof it wants is in hand: the viewer's one request is
  // not turned into a click followed by a second one.
  useEffect(() => {
    if (pendingSend === null || human.pending || gateDocument === null) {
      return
    }
    if (needsProof && human.proof === null) {
      return
    }
    const { channel, identifier } = pendingSend
    setPendingSend(null)
    void sendRef.current(channel, identifier)
  }, [pendingSend, human.pending, gateDocument, needsProof, human.proof])

  const writePassword = async (newPassword: string): Promise<void> => {
    setBusy(true)
    try {
      await operations.completeReset({ newPassword })
      setFailure(null)
      setStage({ kind: 'done' })
      try {
        onDone?.()
      } catch {
        // A throwing host callback is not a failed write: the password
        // changed, so the screen stays in its done state.
      }
    } catch (error) {
      if (errorCodeOf(error) === BAD_REQUEST) {
        form.setError('newPassword', {
          type: 'server',
          message: t('passwordReset.policyFailure'),
        })
        newPasswordInput.current?.focus()
        return
      }
      waitOrFail(error)
    } finally {
      setBusy(false)
    }
  }

  const verifyThenWrite = async (
    code: string,
    newPassword: string,
  ): Promise<void> => {
    setBusy(true)
    try {
      await operations.verifyReset({ code })
      setStage({ kind: 'verified' })
    } catch (error) {
      if (errorCodeOf(error) === LOGIN_FAILED) {
        form.setError('code', {
          type: 'server',
          message: t('passwordReset.codeRejected'),
        })
        codeInput.current?.focus()
        return
      }
      waitOrFail(error)
      return
    } finally {
      setBusy(false)
    }
    await writePassword(newPassword)
  }

  const onSubmit: SubmitHandler<ResetFields> = async (values) => {
    if (busy || waiting || pendingSend !== null) {
      return
    }
    setFailure(null)
    form.clearErrors()
    if (stage.kind === 'request') {
      if (selectedChannel === null) {
        return
      }
      if (selectedChannel === 'sms') {
        setPhoneAttempted(true)
        const phone = toE164(country, nationalNumber)
        if (phone === null) {
          // The control states the per-country reason under its own field;
          // nothing is sent.
          return
        }
        askToSend(selectedChannel, phone)
        return
      }
      const identifier = values.identifier.trim()
      if (identifier === '') {
        form.setError('identifier', {
          type: 'required',
          message: REQUIRED_KEY,
        })
        return
      }
      askToSend(selectedChannel, identifier)
      return
    }
    if (stage.kind !== 'sent' && stage.kind !== 'verified') {
      return
    }
    if (values.newPassword === '') {
      form.setError('newPassword', {
        type: 'required',
        message: REQUIRED_KEY,
      })
      return
    }
    if (values.confirmPassword !== values.newPassword) {
      form.setError('confirmPassword', {
        type: 'server',
        message: t('passwordReset.mismatch'),
      })
      return
    }
    if (stage.kind === 'verified') {
      await writePassword(values.newPassword)
      return
    }
    const code = values.code.trim()
    if (code === '') {
      form.setError('code', {
        type: 'required',
        message: REQUIRED_KEY,
      })
      return
    }
    await verifyThenWrite(code, values.newPassword)
  }

  const startOver = (): void => {
    abandon()
    setFailure(null)
    setRetryUntil(null)
    setResendAt(null)
    setPendingSend(null)
    form.reset()
    setStage({ kind: 'request' })
  }

  const back = (): void => {
    if (stage.kind === 'sent' || stage.kind === 'verified') {
      abandon()
    }
    try {
      onBackToSignIn?.()
    } catch {
      // A throwing host callback is not a failure of leaving.
    }
  }

  const title = (value: string) => (
    <Typography component={headingLevel} variant="h5">
      {value}
    </Typography>
  )

  if (stage.kind === 'checking') {
    return (
      <Box
        role="status"
        sx={{ display: 'flex', alignItems: 'center', gap: 1.5, width: '100%' }}
      >
        <CircularProgress size={18} aria-hidden={true} />
        <Typography>{t('passwordReset.loading')}</Typography>
      </Box>
    )
  }

  if (channels.length === 0) {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
        {title(t('passwordReset.title'))}
        <Typography color="text.secondary">
          {t('passwordReset.unavailable')}
        </Typography>
        {onBackToSignIn === undefined ? null : (
          <Button variant="outlined" onClick={back} sx={{ alignSelf: 'flex-start' }}>
            {t('passwordReset.backToSignIn')}
          </Button>
        )}
      </Box>
    )
  }

  if (stage.kind === 'done') {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
        <Box role="status" sx={{ display: 'flex', flexDirection: 'column', gap: 0.5 }}>
          <Typography component={headingLevel} variant="h5">
            {t('passwordReset.doneTitle')}
          </Typography>
          <Typography color="text.secondary">
            {t('passwordReset.doneDescription')}
          </Typography>
        </Box>
        {onBackToSignIn === undefined ? null : (
          <Button variant="contained" onClick={back} sx={{ alignSelf: 'flex-start' }}>
            {t('passwordReset.backToSignIn')}
          </Button>
        )}
      </Box>
    )
  }

  const showingRequest = stage.kind === 'request'
  const showingSent = stage.kind === 'sent'
  const identifierLabel = sms
    ? t('passwordReset.phoneLabel')
    : t('passwordReset.emailLabel')
  // One action at a time: while a send waits on its proof the primary
  // control is held in either stage, so the block is the only thing to act
  // on until the proof arrives or an edit cancels the wait.
  const sendDisabled = busy || waiting || pendingSend !== null
  const canResend =
    showingSent &&
    stage.channel !== null &&
    stage.identifier !== null &&
    resendRemaining === 0
  const resendLabel =
    resendRemaining > 0
      ? t('passwordReset.resendIn', { seconds: resendRemaining })
      : t('passwordReset.resend')
  const submitLabel = t('passwordReset.submit')

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      {title(t('passwordReset.title'))}
      {showingRequest ? (
        <Typography color="text.secondary">
          {t('passwordReset.description')}
        </Typography>
      ) : null}
      {showingSent ? (
        <Alert severity="info" role="status" sx={{ width: '100%' }}>
          {t('passwordReset.sentNotice')}
        </Alert>
      ) : null}

      <FormLayout
        form={form}
        onSubmit={onSubmit}
        actions={
          <>
            {onBackToSignIn === undefined ? null : (
              <ActionButton type="button" onClick={back}>
                {t('passwordReset.backToSignIn')}
              </ActionButton>
            )}
            {showingSent &&
            stage.channel !== null &&
            stage.identifier !== null &&
            pendingSend === null ? (
              <ActionButton
                type="button"
                disabled={busy || waiting || !canResend}
                onClick={() => {
                  if (stage.channel === null || stage.identifier === null) {
                    return
                  }
                  askToSend(stage.channel, stage.identifier)
                }}
              >
                {resendLabel}
              </ActionButton>
            ) : null}
            <ActionButton
              type="submit"
              emphasis="primary"
              disabled={sendDisabled}
            >
              {showingRequest ? t('passwordReset.send') : submitLabel}
            </ActionButton>
          </>
        }
      >
        {showingRequest ? (
          <>
            {channels.length > 1 ? (
              <ToggleButtonGroup
                exclusive
                value={selectedChannel}
                onChange={(_event, value: PasswordResetChannel | null) => {
                  if (value === null || value === selectedChannel) {
                    return
                  }
                  // Leaving a channel drops its whole-attempt failure, its
                  // stated wait and its field errors: a channel's errors do
                  // not cross to the next face. A send waiting on a proof
                  // belongs to the channel that asked for it, so it is
                  // dropped too.
                  setFailure(null)
                  setRetryUntil(null)
                  form.clearErrors()
                  setPendingSend(null)
                  setChosenChannel(value)
                }}
                aria-label={t('passwordReset.channelLegend')}
              >
                {channels.map((channel) => (
                  <ToggleButton key={channel} value={channel}>
                    {t(`passwordReset.channel.${channel}`)}
                  </ToggleButton>
                ))}
              </ToggleButtonGroup>
            ) : null}
            {sms ? (
              <Box sx={{ width: '100%' }}>
                <PhoneNumberField
                  country={country}
                  nationalNumber={nationalNumber}
                  onCountryChange={(next) => {
                    form.clearErrors('identifier')
                    // A send waiting on its proof names the identifier it
                    // was asked for; editing the field cancels it rather
                    // than sending to a value no longer on screen.
                    setPendingSend(null)
                    setCountry(next)
                  }}
                  onNationalNumberChange={(next) => {
                    form.clearErrors('identifier')
                    setPendingSend(null)
                    setNationalNumber(next)
                  }}
                  countryLabel={t('smsSignIn.countryLabel')}
                  numberLabel={t('passwordReset.phoneLabel')}
                  numberHelperText={t('smsSignIn.phoneHint')}
                  showValidationError={phoneAttempted}
                />
              </Box>
            ) : (
              <FormField
                name="identifier"
                required
                render={({ field, invalid, errorText }) => (
                  <TextField
                    {...field}
                    inputRef={identifierInput}
                    onChange={(event) => {
                      field.onChange(event)
                      // A send waiting on its proof names the identifier it
                      // was asked for; editing the field cancels it rather
                      // than sending to a value no longer on screen.
                      setPendingSend(null)
                    }}
                    label={identifierLabel}
                    type="email"
                    inputMode="email"
                    autoComplete="email"
                    fullWidth
                    error={invalid}
                    helperText={errorText ?? undefined}
                  />
                )}
              />
            )}
          </>
        ) : (
          <>
            {showingSent ? (
              <FormField
                name="code"
                required
                render={({ field, invalid, errorText }) => (
                  <TextField
                    {...field}
                    inputRef={codeInput}
                    onChange={(event) => {
                      field.onChange(event)
                      // Typing the code the viewer already holds ends a
                      // resend still waiting on its proof: the next code
                      // would invalidate what they are entering.
                      setPendingSend(null)
                    }}
                    label={t('passwordReset.codeLabel')}
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    fullWidth
                    error={invalid}
                    helperText={errorText ?? undefined}
                  />
                )}
              />
            ) : null}
            <FormField
              name="newPassword"
              required
              render={({ field, invalid, errorText }) => (
                <TextField
                  {...field}
                  inputRef={newPasswordInput}
                  onChange={(event) => {
                    field.onChange(event)
                    setPendingSend(null)
                  }}
                  label={t('passwordReset.newPasswordLabel')}
                  type="password"
                  autoComplete="new-password"
                  fullWidth
                  error={invalid}
                  helperText={errorText ?? undefined}
                />
              )}
            />
            <FormField
              name="confirmPassword"
              required
              render={({ field, invalid, errorText }) => (
                <TextField
                  {...field}
                  onChange={(event) => {
                    field.onChange(event)
                    setPendingSend(null)
                  }}
                  label={t('passwordReset.confirmPasswordLabel')}
                  type="password"
                  autoComplete="new-password"
                  fullWidth
                  error={invalid}
                  helperText={errorText ?? undefined}
                />
              )}
            />
          </>
        )}
        {/* The deployment's human-verification block. It appears only once
            the viewer asks for a send the gate holds for a proof, so the
            request form on its own shows no challenge; the send leaves by
            itself when the proof arrives. */}
        {pendingSend !== null ? (
          <HumanVerificationBlock
            scope={humanScope}
            gate={human.gate ?? humanGate}
            proof={human.proof}
            status={human.status}
            captcha={human.captcha}
            onToken={human.adoptToken}
            onRetry={human.retry}
            onRefreshCaptcha={human.refreshCaptcha}
            onRequestAudio={human.requestAudio}
            audioRetryAfterSeconds={human.audioRetryAfterSeconds}
            disabled={busy}
          />
        ) : null}
      </FormLayout>

      {failure === null ? null : failure.kind === 'code' ? (
        <InlineError
          code={failure.value}
          retryAfterSeconds={failure.retryAfterSeconds}
          liveRetryAfterSeconds={
            failure.retryAfterSeconds === undefined
              ? undefined
              : liveRetryAfterSeconds
          }
        />
      ) : (
        <Alert severity="error" role="alert" sx={{ width: '100%' }}>
          {failure.value === FLOW_INVALID_TEXT
            ? t('passwordReset.flowInvalid')
            : failure.value}
        </Alert>
      )}

      {failure?.restart === true ? (
        <Button
          type="button"
          variant="outlined"
          onClick={startOver}
          sx={{ alignSelf: 'flex-start' }}
        >
          {t('passwordReset.startOver')}
        </Button>
      ) : null}

    </Box>
  )
}
