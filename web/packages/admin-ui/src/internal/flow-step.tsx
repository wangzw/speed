/**
 * FlowStep: renders one authn step and reports the answer under the caller's
 * answer step; TOTP renders a QR symbol, never the raw step JSON.
 */

import { useEffect, useMemo, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import CircularProgress from '@mui/material/CircularProgress'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import type { AuthnStep } from '@speed/auth-core'
import type { AuthnFlowAnswer } from '@speed/auth-core'
import { formatSecretForDisplay, parseOtpauth } from '@speed/auth-core/otpauth'
import { WebauthnError, runWebauthn, webauthnStepOf } from '@speed/auth-core/webauthn'
import {
  ActionButton,
  ActionRow,
  AnnouncementRegion,
  TotpEnrollmentPanel,
  useActivationGuard,
} from '@speed/ui-kit'
import { useAdminUiTranslation } from './translation.js'

/** The factor/enroll kinds the family has wording for; an unknown kind renders as given. */
const FACTOR_KIND_KEYS: Readonly<Record<string, string>> = {
  totp: 'stepUp.factorKind.totp',
  webauthn: 'stepUp.factorKind.webauthn',
  sms: 'stepUp.factorKind.sms',
}

function factorKindLabel(kind: string, t: (key: string) => string): string {
  const key = FACTOR_KIND_KEYS[kind]
  return key === undefined ? kind : t(key)
}

interface FactorChoice {
  readonly kind: string
  readonly label?: string
}

/**
 * The labels a factor step renders: the kind alone, or "Kind · Name"/"Kind (2)"
 * when one kind is offered more than once.
 */
function factorChoiceLabels(
  factors: readonly FactorChoice[],
  kindLabel: (kind: string) => string,
  named: (kind: string, name: string) => string,
  ordinal: (kind: string, position: number) => string,
): string[] {
  const counts = new Map<string, number>()
  for (const factor of factors) {
    counts.set(factor.kind, (counts.get(factor.kind) ?? 0) + 1)
  }
  const positions = new Map<string, number>()
  return factors.map((factor) => {
    const kind = kindLabel(factor.kind)
    const position = (positions.get(factor.kind) ?? 0) + 1
    positions.set(factor.kind, position)
    if ((counts.get(factor.kind) ?? 0) <= 1) {
      return kind
    }
    const name = factor.label ?? ''
    if (name.trim() !== '') {
      return named(kind, name)
    }
    return position === 1 ? kind : ordinal(kind, position)
  })
}

export interface FlowStepProps {
  /** The step to render. */
  readonly step: AuthnStep
  /** The flow's step number; the assertion effect keys on it and the challenge. */
  readonly sequence: number
  /**
   * The answer step a step travels under: `answer` for the first factor,
   * `factor` for the second, `enroll` for registration.
   */
  readonly answerStep: string
  readonly submitLabel: string
  readonly onSubmit: (answer: AuthnFlowAnswer) => void
  /**
   * The answer is in flight: the activated option (or the single submit
   * control) shows progress and stays focusable, and the rest of the group
   * refuses another answer.
   */
  readonly busy?: boolean
  /**
   * The step cannot be answered now (a throttled wait): the fields stay
   * editable and the controls stay focusable, but activation is refused.
   */
  readonly unavailable?: boolean
  /** The id of the page text explaining what holds the step back. */
  readonly unavailableDescribedBy?: string
  /** The passkey ceremony's failure, held by the caller so one banner stands. */
  readonly ceremonyError?: string | null
  /** Reports the ceremony's failure key, or null when a new attempt starts. */
  readonly onCeremonyError?: (errorKey: string | null) => void
}

export function FlowStep({
  step,
  sequence,
  answerStep,
  submitLabel,
  onSubmit,
  busy = false,
  unavailable = false,
  unavailableDescribedBy,
  ceremonyError,
  onCeremonyError,
}: FlowStepProps) {
  const { t } = useAdminUiTranslation()
  // The free-text answer of a TOTP code field, and a `challenge` step's fields.
  const [values, setValues] = useState<Record<string, string>>({})
  // The option activated for this step; the driver keys the renderer on the
  // step, so a settled answer remounts this state.
  const [activated, setActivated] = useState<string | null>(null)
  // The submit is guarded as well as the button: an implicit submission from a
  // field must be refused while the answer is in flight or the step is held.
  const handleSubmit = (event: FormEvent): void => {
    event.preventDefault()
    onSubmit({ step: answerStep, input: { ...values } })
  }
  const guardedSubmit = useActivationGuard(handleSubmit, { busy, unavailable })

  if (step.kind === 'complete') {
    return (
      <ActionRow sx={{ mt: 2 }}>
        <ActionButton
          type="button"
          emphasis="primary"
          busy={busy}
          unavailable={unavailable}
          unavailableDescribedBy={unavailable ? unavailableDescribedBy : undefined}
          onClick={() => onSubmit({ step: 'complete' })}
        >
          {submitLabel}
        </ActionButton>
      </ActionRow>
    )
  }

  if (step.kind === 'redirect') {
    return (
      <Box sx={{ mt: 2 }}>
        <Typography variant="body2" color="text.secondary">
          {t('stepUp.redirectNotice')}
        </Typography>
      </Box>
    )
  }

  if (step.kind === 'factor') {
    const factorLabels = factorChoiceLabels(
      step.factors,
      (kind) => factorKindLabel(kind, t),
      (kind, name) => t('stepUp.factorOptionNamed', { kind, name }),
      (kind, position) => t('stepUp.factorOptionOrdinal', { kind, position }),
    )
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1, mt: 2 }}>
        {step.factors.map((factor, index) => (
          <ActionButton
            key={factor.id}
            type="button"
            busy={busy && activated === factor.id}
            unavailable={unavailable || (busy && activated !== factor.id)}
            unavailableDescribedBy={
              !busy && unavailable ? unavailableDescribedBy : undefined
            }
            onClick={() => {
              setActivated(factor.id)
              onSubmit({ step: 'factor', input: { factor: factor.id } })
            }}
          >
            {factorLabels[index] ?? factorKindLabel(factor.kind, t)}
          </ActionButton>
        ))}
      </Box>
    )
  }

  if (step.kind === 'enroll') {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1, mt: 2 }}>
        {step.kinds.map((kind) => (
          <ActionButton
            key={kind}
            type="button"
            busy={busy && activated === kind}
            unavailable={unavailable || (busy && activated !== kind)}
            unavailableDescribedBy={
              !busy && unavailable ? unavailableDescribedBy : undefined
            }
            onClick={() => {
              setActivated(kind)
              onSubmit({ step: 'enroll', input: { kind } })
            }}
          >
            {factorKindLabel(kind, t)}
          </ActionButton>
        ))}
      </Box>
    )
  }

  if (step.kind === 'webauthn') {
    const options = step.options
    if (webauthnStepOf(options) === 'totp') {
      return (
        <TotpEnrollStep
          uri={otpauthUriOf(options)}
          code={values.code ?? ''}
          onCodeChange={(code) => setValues({ ...values, code })}
          submitLabel={submitLabel}
          busy={busy}
          unavailable={unavailable}
          unavailableDescribedBy={unavailableDescribedBy}
          onSubmit={(code) => onSubmit({ step: answerStep, input: { code } })}
        />
      )
    }
    return (
      <PasskeyPrompt
        options={options}
        sequence={sequence}
        answerStep={answerStep}
        submitLabel={submitLabel}
        onSubmit={onSubmit}
        busy={busy}
        unavailable={unavailable}
        unavailableDescribedBy={unavailableDescribedBy}
        ceremonyError={ceremonyError}
        onCeremonyError={onCeremonyError}
      />
    )
  }

  // challenge: one text field per requested field, submitted together.
  return (
    <Box component="form" onSubmit={guardedSubmit} sx={{ mt: 2 }}>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
        {step.fields.map((field) => (
          <TextField
            key={field.name}
            label={field.name}
            type={field.secret === true ? 'password' : 'text'}
            value={values[field.name] ?? ''}
            onChange={(event) => setValues({ ...values, [field.name]: event.target.value })}
            required={field.required === true}
            fullWidth
            autoComplete={field.secret === true ? 'one-time-code' : 'off'}
          />
        ))}
      </Box>
      <ActionRow sx={{ mt: 2 }}>
        <ActionButton
          type="submit"
          emphasis="primary"
          busy={busy}
          unavailable={unavailable}
          unavailableDescribedBy={unavailable ? unavailableDescribedBy : undefined}
        >
          {submitLabel}
        </ActionButton>
      </ActionRow>
    </Box>
  )
}

interface TotpEnrollStepProps {
  readonly uri: string | null
  /** The six-digit code typed so far. */
  readonly code: string
  readonly onCodeChange: (code: string) => void
  readonly submitLabel: string
  /** The answer is in flight; the confirm action shows progress. */
  readonly busy?: boolean
  /** The step cannot be answered now (a throttled wait). */
  readonly unavailable?: boolean
  /** The id of the page text explaining what holds the step back. */
  readonly unavailableDescribedBy?: string
  /** Reports the trimmed code; the caller answers the flow with it. */
  readonly onSubmit: (code: string) => void
}

/**
 * The TOTP enrolment block: the URI as a QR symbol and a manual-secret disclosure;
 * a URI that does not parse renders neither.
 */
function TotpEnrollStep({
  uri,
  code,
  onCodeChange,
  submitLabel,
  busy = false,
  unavailable = false,
  unavailableDescribedBy,
  onSubmit,
}: TotpEnrollStepProps) {
  const { t } = useAdminUiTranslation()
  const otpauth = useMemo(() => {
    if (uri === null) {
      return null
    }
    const parsed = parseOtpauth(uri)
    return parsed.ok ? parsed.otpauth : null
  }, [uri])

  const parameters =
    otpauth === null
      ? undefined
      : [
          { label: t('stepUp.totpAlgorithm'), value: otpauth.algorithm },
          { label: t('stepUp.totpDigits'), value: String(otpauth.digits) },
          { label: t('stepUp.totpPeriod'), value: String(otpauth.period) },
        ]

  // The wait the caller owns explains the control before a missing code does:
  // the operator cannot type their way past the wait.
  const codeEmpty = code.trim() === ''
  const explanation =
    unavailable && unavailableDescribedBy !== undefined
      ? { unavailableDescribedBy }
      : !unavailable && codeEmpty
        ? { unavailableReason: t('stepUp.codeRequired') }
        : {}

  return (
    <TotpEnrollmentPanel
      otpauthUri={otpauth === null ? undefined : (uri ?? undefined)}
      qrLabel={t('stepUp.totpQrLabel')}
      guidance={otpauth === null ? undefined : t('stepUp.enrollTotpHelp')}
      manualToggleLabel={t('stepUp.totpManualToggle')}
      secret={
        otpauth === null
          ? undefined
          : {
              value: otpauth.secret,
              display: formatSecretForDisplay(otpauth.secret),
            }
      }
      secretLabel={t('stepUp.totpSecretLabel')}
      copyLabel={t('stepUp.totpCopyLabel')}
      copiedLabel={t('stepUp.totpCopiedLabel')}
      copyVisibleLabel={t('stepUp.totpCopyVisibleLabel')}
      parameters={parameters}
      codeLabel={t('stepUp.codeLabel')}
      code={code}
      onCodeChange={onCodeChange}
      primaryAction={
        <ActionButton
          type="button"
          emphasis="primary"
          busy={busy}
          unavailable={unavailable || codeEmpty}
          {...explanation}
          onClick={() => onSubmit(code.trim())}
        >
          {submitLabel}
        </ActionButton>
      }
    />
  )
}

interface PasskeyPromptProps {
  readonly options: unknown
  /** The flow's step number; the arrival effect keys on it with the challenge. */
  readonly sequence: number
  readonly answerStep: string
  readonly submitLabel: string
  readonly onSubmit: (answer: AuthnFlowAnswer) => void
  /** The answer is in flight; the start/retry action shows progress. */
  readonly busy?: boolean
  /** The step cannot be answered now (a throttled wait). */
  readonly unavailable?: boolean
  /** The id of the page text explaining what holds the step back. */
  readonly unavailableDescribedBy?: string
  readonly ceremonyError?: string | null
  readonly onCeremonyError?: (errorKey: string | null) => void
}

/**
 * The passkey ceremony step: assertion starts on arrival, registration on a click;
 * cancel fails at once and a late answer is discarded by attempt.
 */
function PasskeyPrompt({
  options,
  sequence,
  answerStep,
  submitLabel,
  onSubmit,
  busy = false,
  unavailable = false,
  unavailableDescribedBy,
  ceremonyError,
  onCeremonyError,
}: PasskeyPromptProps) {
  const { t } = useAdminUiTranslation()
  const registering = webauthnStepOf(options) === 'register'
  // An assertion step starts on arrival, so its first render is already the
  // waiting state and the start control never flashes.
  const arrivalKey = assertionKey(options, sequence)
  const [working, setWorking] = useState(arrivalKey !== null)
  const [localError, setLocalError] = useState<string | null>(null)
  const controlled = onCeremonyError !== undefined
  const errorKey = controlled ? (ceremonyError ?? null) : localError
  const reportError = (key: string | null): void => {
    if (controlled) {
      onCeremonyError(key)
    } else {
      setLocalError(key)
    }
  }
  const cancelRef = useRef<HTMLButtonElement>(null)
  // The ceremony being waited on. Cancel clears it before the failure renders,
  // so a late settlement and a retry's attempt stay apart.
  const pendingRef = useRef<{
    attempt: number
    controller: AbortController
  } | null>(null)
  const attemptRef = useRef(0)
  // The latest `run`, read by the arrival effect, which keys on the challenge
  // and sequence rather than this function or the options object.
  const runRef = useRef<() => Promise<void>>(async () => {})

  // An unmounted step has no flow to answer, so abandon the ceremony.
  useEffect(
    () => () => {
      const pending = pendingRef.current
      pendingRef.current = null
      pending?.controller.abort()
    },
    [],
  )

  // The cancel control takes the focus of the control that unmounted.
  useEffect(() => {
    if (working) {
      cancelRef.current?.focus()
    }
  }, [working])

  async function run(): Promise<void> {
    const attempt = attemptRef.current + 1
    attemptRef.current = attempt
    const controller = new AbortController()
    pendingRef.current = { attempt, controller }
    setWorking(true)
    reportError(null)
    try {
      const credential = await runWebauthn(options, controller.signal)
      if (pendingRef.current?.attempt === attempt) {
        onSubmit({ step: answerStep, input: { credential } })
      }
    } catch (failure) {
      if (pendingRef.current?.attempt === attempt) {
        reportError(webauthnErrorKey(failure, registering))
      }
    } finally {
      if (pendingRef.current?.attempt === attempt) {
        pendingRef.current = null
        setWorking(false)
      }
    }
  }
  // Refresh `runRef` a render before the arrival effect reads it, without
  // restarting the ceremony.
  useEffect(() => {
    runRef.current = run
  })

  // Key on the challenge and sequence so a re-render does not restart the ceremony;
  // StrictMode's first mount is abandoned by the cleanup.
  useEffect(() => {
    if (arrivalKey === null) {
      return
    }
    void runRef.current()
    return () => {
      const pending = pendingRef.current
      pendingRef.current = null
      pending?.controller.abort()
    }
  }, [arrivalKey])

  function cancel(): void {
    const pending = pendingRef.current
    pendingRef.current = null
    pending?.controller.abort()
    // Fail now; the ceremony's own rejection and settlement are ignored because
    // its attempt no longer matches.
    setWorking(false)
    reportError('stepUp.passkeyCancelled')
  }

  return (
    <Box sx={{ mt: 2 }}>
      <Typography variant="body2" color="text.secondary">
        {registering
          ? t('stepUp.passkeyRegisterPrompt')
          : t('stepUp.passkeyPrompt')}
      </Typography>
      {errorKey !== null && (
        <Box sx={{ mt: 1 }}>
          <Alert severity="error" role="alert">
            {t(errorKey)}
          </Alert>
        </Box>
      )}
      {working ? (
        <>
          <Box
            sx={{ display: 'flex', alignItems: 'center', gap: 1.5, mt: 2 }}
          >
            <CircularProgress size={18} aria-hidden={true} />
            <AnnouncementRegion
              message={t('stepUp.passkeyWaiting')}
              sx={{ typography: 'body1' }}
            />
          </Box>
          <ActionRow sx={{ mt: 1.5 }}>
            <ActionButton ref={cancelRef} type="button" onClick={cancel}>
              {t('stepUp.passkeyCancel')}
            </ActionButton>
          </ActionRow>
        </>
      ) : (
        <ActionRow sx={{ mt: 2 }}>
          <ActionButton
            type="button"
            emphasis="primary"
            busy={busy}
            unavailable={unavailable}
            unavailableDescribedBy={
              unavailable ? unavailableDescribedBy : undefined
            }
            onClick={() => void run()}
          >
            {errorKey === null ? submitLabel : t('stepUp.passkeyRetry')}
          </ActionButton>
        </ActionRow>
      )}
    </Box>
  )
}

/**
 * The identity of an assertion step's ceremony: the step number plus the
 * server's challenge, so a re-render does not restart it while a new challenge
 * rebinds it. A registration step returns `null`, since it starts on a click.
 */
function assertionKey(options: unknown, sequence: number): string | null {
  if (webauthnStepOf(options) !== 'assert') {
    return null
  }
  let challenge = ''
  if (typeof options === 'object' && options !== null) {
    const value = (options as Record<string, unknown>).challenge
    if (typeof value === 'string') {
      challenge = value
    }
  }
  return `assert:${sequence}:${challenge}`
}

/** The bundle key of a failed ceremony's message, by reason and ceremony. */
function webauthnErrorKey(failure: unknown, registering: boolean): string {
  if (failure instanceof WebauthnError) {
    switch (failure.reason) {
      case 'unsupported':
        return 'stepUp.passkeyUnsupported'
      case 'cancelled':
        return 'stepUp.passkeyCancelled'
      case 'already-registered':
        return 'stepUp.passkeyAlreadyRegistered'
      default:
        return registering
          ? 'stepUp.passkeyFailedRegister'
          : 'stepUp.passkeyFailedSignIn'
    }
  }
  return registering
    ? 'stepUp.passkeyFailedRegister'
    : 'stepUp.passkeyFailedSignIn'
}

function otpauthUriOf(options: unknown): string | null {
  if (typeof options !== 'object' || options === null) {
    return null
  }
  const uri = (options as Record<string, unknown>).otpauth_uri
  return typeof uri === 'string' && uri !== '' ? uri : null
}
