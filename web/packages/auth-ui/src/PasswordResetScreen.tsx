/**
 * PasswordResetScreen: the anonymous password reset surface.
 *
 * The screen drives the host-bound `PasswordResetOperations`, not a
 * session: the host maps the deployment's published reset channels from
 * the discovery document into `channels`, binds the generated reset
 * operations, and navigates on `onDone` / `onBackToSignIn`. The session
 * prop is used only for the deployment's human-verification gate and
 * proof, because a send may have to present one.
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
 * the send control until it elapses; the resend control states its own
 * remaining interval.
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
  FormField,
  FormLayout,
  HumanVerification as HumanVerificationBlock,
  errorCodeOf,
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
 * screen words itself (the invalidated flow and the stated retry wait).
 * `restart` marks the refusals after which the bound record cannot
 * continue, so the surface offers a start-over exit with the banner. */
interface Failure {
  readonly kind: 'code' | 'text'
  readonly value: string
  readonly restart: boolean
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

/** The failure value that marks the stated Retry-After wait; the sentence
 * is filled from the deadline at render time. */
const RETRY_TEXT = 'retry'

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
  const currentReset = operations.currentReset
  const initialStage: Stage =
    currentReset === undefined ? { kind: 'request' } : { kind: 'checking' }
  const [stage, setStage] = useState<Stage>(initialStage)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<Failure | null>(null)
  const [retryUntil, setRetryUntil] = useState<number | null>(null)
  const [resendAt, setResendAt] = useState<number | null>(null)
  // The tick the resend countdown re-renders on; it is only a clock, never
  // a second source of the deadline.
  const [now, setNow] = useState(() => Date.now())
  const [chosenChannel, setChosenChannel] =
    useState<PasswordResetChannel | null>(channels[0] ?? null)
  const identifierInput = useRef<HTMLInputElement>(null)
  const codeInput = useRef<HTMLInputElement>(null)
  const newPasswordInput = useRef<HTMLInputElement>(null)
  const human = useHumanVerification(
    session,
    humanGate === undefined ? {} : { gate: humanGate },
  )
  const needsProof = humanGateNeedsProof(human.gate)

  // The published channel list is the deployment's: a choice that is no
  // longer offered falls back to the first one.
  const selectedChannel =
    chosenChannel !== null && channels.includes(chosenChannel)
      ? chosenChannel
      : (channels[0] ?? null)
  const sms = selectedChannel === 'sms'

  // Resume the bound record once per mounted resume operation. A refused
  // read (no binding, an expired or failed record) starts the surface
  // fresh; the record's own state is the only thing read.
  useEffect(() => {
    if (currentReset === undefined) {
      return undefined
    }
    let cancelled = false
    currentReset().then(
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
  }, [currentReset])

  // A stated Retry-After wait ends on its own: the send control
  // re-enables and the stale wait sentence goes away.
  useEffect(() => {
    if (retryUntil === null) {
      return undefined
    }
    const timer = setTimeout(
      () => {
        setRetryUntil(null)
        setFailure((held) => (held?.value === RETRY_TEXT ? null : held))
      },
      Math.max(0, retryUntil - Date.now()),
    )
    return () => clearTimeout(timer)
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
      // The sentence is rendered from the deadline, so its placeholder is
      // filled at render time rather than stored as a fixed number.
      setFailure({ kind: 'text', value: RETRY_TEXT, restart: false })
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
    setFailure(null)
    form.clearErrors()
    const proof = needsProof ? (human.proof ?? undefined) : undefined
    if (needsProof && proof === undefined) {
      return
    }
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
      setBusy(false)
    }
  }

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
      fail(error)
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
      fail(error)
      return
    } finally {
      setBusy(false)
    }
    await writePassword(newPassword)
  }

  const onSubmit: SubmitHandler<ResetFields> = async (values) => {
    if (busy || waiting) {
      return
    }
    setFailure(null)
    form.clearErrors()
    if (stage.kind === 'request') {
      if (selectedChannel === null) {
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
      await send(selectedChannel, identifier)
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
  const sendDisabled =
    busy ||
    waiting ||
    (showingRequest && needsProof && human.proof === null)
  const canResend =
    showingSent &&
    stage.channel !== null &&
    stage.identifier !== null &&
    resendRemaining === 0 &&
    (!needsProof || human.proof !== null)
  const resendLabel =
    resendRemaining > 0
      ? t('passwordReset.resendIn', { seconds: resendRemaining })
      : t('passwordReset.resend')
  const submitLabel = t('passwordReset.submit')
  // A proof is only useful where the surface can send: the request form,
  // or a sent flow this mount started (a resumed sent flow cannot resend).
  const canPresentProof =
    showingRequest || (showingSent && stage.channel !== null)

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
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
            {showingSent && stage.channel !== null && stage.identifier !== null ? (
              <Button
                type="button"
                variant="text"
                disabled={busy || waiting || !canResend}
                onClick={() => {
                  if (stage.channel === null || stage.identifier === null) {
                    return
                  }
                  void send(stage.channel, stage.identifier)
                }}
              >
                {resendLabel}
              </Button>
            ) : null}
            <Button
              type="submit"
              variant="contained"
              disabled={sendDisabled}
            >
              {showingRequest ? t('passwordReset.send') : submitLabel}
            </Button>
          </Box>
        }
      >
        {showingRequest ? (
          <>
            {channels.length > 1 ? (
              <ToggleButtonGroup
                exclusive
                value={selectedChannel}
                onChange={(_event, value: PasswordResetChannel | null) => {
                  if (value !== null) {
                    setChosenChannel(value)
                  }
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
            <FormField
              name="identifier"
              required
              render={({ field, invalid, errorText }) => (
                <TextField
                  {...field}
                  inputRef={identifierInput}
                  label={identifierLabel}
                  type={sms ? 'tel' : 'email'}
                  inputMode={sms ? 'tel' : 'email'}
                  autoComplete={sms ? 'tel' : 'email'}
                  fullWidth
                  error={invalid}
                  helperText={
                    errorText ?? (sms ? t('passwordReset.phoneHint') : undefined)
                  }
                />
              )}
            />
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
        {needsProof && canPresentProof ? (
          <HumanVerificationBlock
            gate={human.gate ?? undefined}
            proof={human.proof}
            status={human.status}
            onToken={human.adoptToken}
            onRetry={human.retry}
            disabled={busy}
          />
        ) : null}
      </FormLayout>

      {failure === null ? null : failure.kind === 'code' ? (
        <InlineError code={failure.value} />
      ) : (
        <Alert severity="error" role="alert" sx={{ width: '100%' }}>
          {failure.value === RETRY_TEXT
            ? t('passwordReset.retryAfter', {
                seconds: Math.max(1, Math.ceil(retryRemaining / 1000)),
              })
            : failure.value === FLOW_INVALID_TEXT
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

      {onBackToSignIn === undefined ? null : (
        <Button
          type="button"
          variant="text"
          onClick={back}
          sx={{ alignSelf: 'flex-start' }}
        >
          {t('passwordReset.backToSignIn')}
        </Button>
      )}
    </Box>
  )
}
