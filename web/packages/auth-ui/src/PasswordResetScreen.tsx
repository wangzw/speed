/** The anonymous password reset surface. */

import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
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
  useActivationGuard,
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
import { firstControl } from './internal/step-focus.js'

/** One reset request. */
export interface PasswordResetRequest {
  /** The channel the identifier belongs to. */
  readonly channel: PasswordResetChannel
  /** The channel's identifier: an email address, or an E.164 phone number. */
  readonly identifier: string
  /** The human-verification proof a gated send presents. */
  readonly humanProof?: string
}

/** The answer to a code request. */
export interface PasswordResetSent {
  /** A code was sent for the identifier. */
  readonly state: 'sent'
  /** How long the code lives, in whole seconds. */
  readonly expiresIn: number
  /** How soon another code may be asked for, in whole seconds. */
  readonly resendAfter: number
}

/** The answer to a proved code. */
export interface PasswordResetVerified {
  /** The code was proved. */
  readonly state: 'verified'
  /** How long the proved ticket lives, in whole seconds. */
  readonly expiresIn: number
}

/**
 * The reset operations the screen drives, bound by the host to the generated
 * reset operations. `currentReset` and `abandonReset` are optional: without
 * them the surface starts fresh and cannot resume or abandon server-side.
 */
export interface PasswordResetOperations {
  /** Sends a code, or a suppressed record, for the identifier. */
  readonly requestReset: (input: PasswordResetRequest) => Promise<PasswordResetSent>
  /** Proves the code held for the bound record. */
  readonly verifyReset: (input: { readonly code: string }) => Promise<PasswordResetVerified>
  /** Writes the new password for the proved record. */
  readonly completeReset: (input: {
    readonly newPassword: string
  }) => Promise<void>
  /** Reads the bound record on mount. */
  readonly currentReset?: () => Promise<PasswordResetSent | PasswordResetVerified>
  /** Drops the bound record. */
  readonly abandonReset?: () => Promise<void>
}

/** The host supplies the session, the channels and the bound reset
 * operations; the screen owns the reset record's stage state. */
export interface PasswordResetScreenProps {
  /** The session the human-verification gate and proof come from. */
  readonly session: AuthSession
  /** The reset channels the deployment publishes, in its order. An empty list
   * renders the unavailable notice. */
  readonly channels: readonly PasswordResetChannel[]
  /** The host-bound reset operations. */
  readonly operations: PasswordResetOperations
  /** The human-verification gate the host already read. Omitted, the screen
   * reads it through the session before a send. */
  readonly humanGate?: AuthnHumanGate
  /** The heading element the screen's titles render as; `'h1'` by default. */
  readonly headingLevel?: HeadingLevel
  /** Fired once after the password write lands; the host navigates. */
  readonly onDone?: () => void
  /** Fired when the viewer asks to return to sign-in; an in-flight record is
   * abandoned first. */
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

interface Failure {
  readonly kind: 'code' | 'text'
  readonly value: string
  /** Whether the bound record cannot continue, so a start-over exit is
   * offered with the banner. */
  readonly restart: boolean
  readonly retryAfterSeconds?: number
}

interface ResetFields {
  identifier: string
  code: string
  newPassword: string
  confirmPassword: string
}

type BusyAction = 'send' | 'resend' | 'submit'

interface PendingSend {
  readonly channel: PasswordResetChannel
  readonly identifier: string
  readonly origin: 'send' | 'resend'
}

/** The server's one collapsed refusal: a wrong or expired code, a suppressed
 * record and an exhausted attempt budget all answer under this code. */
const LOGIN_FAILED = 'authn.login_failed'

const BAD_REQUEST = 'authn.bad_request'

const THROTTLED = 'authn.throttled'

/** The reset surface's own text for an invalidated flow: the generic
 * flow-invalid sentence speaks of a sign-in attempt. */
const FLOW_INVALID_TEXT = 'flow-invalid'

const REQUIRED_KEY = 'form.required'

const RESTART_CODES = new Set<string>(['authn.password_reset_unavailable'])

/**
 * The anonymous reset surface: drives the host-bound reset operations through
 * request, sent, verified and done states, using the session only for the
 * human-verification gate and proof a send may present.
 */
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
  const preferredCountry = usePhoneCountryDefault()
  const [country, setCountry] = useState(() => preferredCountry)
  const [nationalNumber, setNationalNumber] = useState('')
  const [phoneAttempted, setPhoneAttempted] = useState(false)
  // Captured once: a host binding operations inline hands a new function
  // identity each render, which would re-read the bound record on every field
  // change.
  const [resume] = useState(() => operations.currentReset)
  const initialStage: Stage =
    resume === undefined ? { kind: 'request' } : { kind: 'checking' }
  const [stage, setStage] = useState<Stage>(initialStage)
  const [busy, setBusy] = useState(false)
  const [busyAction, setBusyAction] = useState<
    BusyAction | null
  >(null)
  const [failure, setFailure] = useState<Failure | null>(null)
  const [retryUntil, setRetryUntil] = useState<number | null>(null)
  const [resendAt, setResendAt] = useState<number | null>(null)
  const [now, setNow] = useState(() => Date.now())
  const [, setRetryTick] = useState(0)
  const [chosenChannel, setChosenChannel] =
    useState<PasswordResetChannel | null>(channels[0] ?? null)
  const identifierInput = useRef<HTMLInputElement>(null)
  const codeInput = useRef<HTMLInputElement>(null)
  const newPasswordInput = useRef<HTMLInputElement>(null)
  const formRef = useRef<HTMLDivElement>(null)
  // The control that held focus when the viewer asked for the code. The
  // action row stays mounted across both stages, so only focus still at this
  // origin (or dropped to the body) belongs to the switch.
  const sendFocusOrigin = useRef<Element | null>(null)
  // The stage the previous commit rendered, so the send that leaves the
  // request state is told apart from the resume read at mount.
  const previousStageKind = useRef<Stage['kind']>(initialStage.kind)
  // The done surface's heading is where focus lands when the completed write
  // removes the form; the flag is set by that write, so only a removal it
  // caused hands focus over.
  const doneFocusPending = useRef(false)
  const doneHeading = useRef<HTMLHeadingElement>(null)
  const waitNoteId = useId()
  const selectedChannel =
    chosenChannel !== null && channels.includes(chosenChannel)
      ? chosenChannel
      : (channels[0] ?? null)
  const sms = selectedChannel === 'sms'
  // A send waiting on the gate and proof it needs. The gate is read and the
  // proof obtained only while this is set, so the request form alone shows no
  // challenge.
  const [pendingSend, setPendingSend] = useState<PendingSend | null>(null)
  // The direct path and the deferred path that waits for a proof can reach
  // the send in the same tick.
  const sendingRef = useRef(false)
  const humanScope = sms ? 'phone' : 'other'
  const human = useHumanVerification(session, {
    scope: humanScope,
    enabled: pendingSend !== null,
    ...(humanGate === undefined ? {} : { gate: humanGate }),
  })
  const gateDocument = humanGate ?? human.gate
  const needsProof = humanGateNeedsProof(gateDocument, humanScope)

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

  // Hand focus to the done heading only when the removal is what took it: a
  // connected control means the operator moved focus during the write.
  useLayoutEffect(() => {
    if (stage.kind !== 'done' || !doneFocusPending.current) {
      return
    }
    doneFocusPending.current = false
    const active = document.activeElement
    if (
      active !== null &&
      active !== document.body &&
      (!(active instanceof HTMLElement) || active.isConnected)
    ) {
      return
    }
    doneHeading.current?.focus()
  }, [stage])

  // The send replaces the identifier field. Under the step-focus rule, focus
  // moves only when it was still at the submit origin, on the body, or on a
  // node that left the document; only a commit from the request state counts.
  useLayoutEffect(() => {
    const previous = previousStageKind.current
    previousStageKind.current = stage.kind
    if (previous !== 'request' || stage.kind !== 'sent') {
      return
    }
    const container = formRef.current
    if (container === null) {
      return
    }
    const active = document.activeElement
    const focusWasWithTheSend =
      active === sendFocusOrigin.current ||
      active === null ||
      active === document.body ||
      active === document.documentElement ||
      (active instanceof HTMLElement && !active.isConnected)
    if (!focusWasWithTheSend) {
      return
    }
    firstControl(container)?.focus()
  }, [stage.kind])

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

  // An interval rather than a self-rescheduling timeout keeps the clock
  // moving under a test's advanced fake clock.
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
      // Leaving is best-effort: a failed abandon does not keep the viewer here.
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
    origin: PendingSend['origin'],
  ): Promise<void> => {
    if (sendingRef.current) {
      return
    }
    setFailure(null)
    form.clearErrors()
    const proof = needsProof ? (human.proof ?? undefined) : undefined
    if (needsProof && proof === undefined) {
      return
    }
    sendingRef.current = true
    setBusy(true)
    setBusyAction(origin)
    try {
      const pending = operations.requestReset({
        channel,
        identifier,
        ...(proof === undefined ? {} : { humanProof: proof }),
      })
      if (proof !== undefined) {
        // A proof is single-use: the next one is obtained whether the send is
        // accepted or refused.
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
      setBusyAction(null)
    }
  }

  const sendRef = useRef(send)
  sendRef.current = send

  /**
   * Ask for a code: sends now when the gate wants no proof, otherwise holds
   * the send until the effect below sees a proof.
   */
  const askToSend = (
    channel: PasswordResetChannel,
    identifier: string,
    origin: PendingSend['origin'],
  ): void => {
    if (pendingSend !== null) {
      return
    }
    if (origin === 'send') {
      sendFocusOrigin.current = document.activeElement
    }
    setFailure(null)
    form.clearErrors()
    if (
      !human.pending &&
      gateDocument !== null &&
      (!needsProof || human.proof !== null)
    ) {
      void send(channel, identifier, origin)
      return
    }
    setPendingSend({ channel, identifier, origin })
  }

  // A send held by the gate leaves as soon as the gate is known and its proof
  // is in hand. The `busy` guard is defensive and currently unreachable: the
  // code submit abandons a held resend before it starts its own write.
  useEffect(() => {
    if (pendingSend === null || human.pending || gateDocument === null) {
      return
    }
    if (needsProof && human.proof === null) {
      return
    }
    if (busy) {
      return
    }
    const { channel, identifier, origin } = pendingSend
    setPendingSend(null)
    void sendRef.current(channel, identifier, origin)
  }, [
    pendingSend,
    human.pending,
    gateDocument,
    needsProof,
    human.proof,
    busy,
  ])

  const writePassword = async (newPassword: string): Promise<void> => {
    setBusy(true)
    setBusyAction('submit')
    try {
      await operations.completeReset({ newPassword })
      setFailure(null)
      if (onDone === undefined) {
        doneFocusPending.current = true
      }
      setStage({ kind: 'done' })
      try {
        onDone?.()
      } catch {
        // A throwing host callback is not a failed write: the password changed.
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
      setBusyAction(null)
    }
  }

  const verifyThenWrite = async (
    code: string,
    newPassword: string,
  ): Promise<void> => {
    setBusy(true)
    setBusyAction('submit')
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
      setBusyAction(null)
    }
    await writePassword(newPassword)
  }

  const onSubmit: SubmitHandler<ResetFields> = async (values) => {
    // A send waiting on its proof is not a write in flight, so the code
    // submit stays usable.
    if (busy || waiting) {
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
          return
        }
        askToSend(selectedChannel, phone, 'send')
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
      askToSend(selectedChannel, identifier, 'send')
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
    // Using the code in hand abandons a resend still waiting on its proof: the
    // next code would invalidate the one being submitted.
    setPendingSend(null)
    await verifyThenWrite(code, values.newPassword)
  }

  const awaitingProof = pendingSend !== null
  const resendBusy = busyAction === 'resend' || awaitingProof
  // In the request stage the primary control is the send control; in the sent
  // and verified stages it submits the code and/or password, so only its own
  // write makes it busy.
  const submitBusy =
    busyAction === 'submit' ||
    (stage.kind === 'request' && (busyAction === 'send' || awaitingProof))
  const submitHeldByWait = waiting
  const submitHeldBySibling =
    stage.kind !== 'request' && busyAction === 'resend'
  const submitUnavailable = submitHeldByWait || submitHeldBySibling
  const guardedSubmit = useActivationGuard(onSubmit, {
    busy: submitBusy,
    unavailable: submitUnavailable,
  })

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
          {/* The heading is the focus landing when the host binds no way back:
              a programmatic target, not a tab stop. */}
          <Typography
            ref={doneHeading}
            component={headingLevel}
            variant="h5"
            tabIndex={-1}
          >
            {t('passwordReset.doneTitle')}
          </Typography>
          <Typography color="text.secondary">
            {t('passwordReset.doneDescription')}
          </Typography>
        </Box>
        {onBackToSignIn === undefined ? null : (
          <Button
            variant="contained"
            onClick={back}
            sx={{ alignSelf: 'flex-start' }}
          >
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
        ref={formRef}
        form={form}
        onSubmit={guardedSubmit}
        actions={
          <>
            {onBackToSignIn === undefined ? null : (
              <ActionButton type="button" onClick={back}>
                {t('passwordReset.backToSignIn')}
              </ActionButton>
            )}
            {showingSent &&
            stage.channel !== null &&
            stage.identifier !== null ? (
              <ActionButton
                type="button"
                busy={resendBusy}
                unavailable={
                  waiting || !canResend || busyAction === 'submit'
                }
                unavailableDescribedBy={
                  waiting && !resendBusy ? waitNoteId : undefined
                }
                onClick={() => {
                  if (stage.channel === null || stage.identifier === null) {
                    return
                  }
                  askToSend(stage.channel, stage.identifier, 'resend')
                }}
              >
                {resendLabel}
              </ActionButton>
            ) : null}
            <ActionButton
              type="submit"
              emphasis="primary"
              busy={submitBusy}
              unavailable={submitUnavailable}
              unavailableDescribedBy={
                submitHeldByWait ? waitNoteId : undefined
              }
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
                  // A channel's errors, wait and held send do not cross to the
                  // next face.
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
                      // Typing a held code ends a resend still waiting on its
                      // proof, which would invalidate what they are entering.
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
        {/* Appears only once a send is held for a proof; the send leaves by
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

      {failure === null ? null : (
        <Box id={waitNoteId}>
          {failure.kind === 'code' ? (
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
        </Box>
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
