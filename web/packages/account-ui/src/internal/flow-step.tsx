/**
 * FlowStep: renders one step of an authn flow and reports the answer back to
 * the caller's driver.
 */

import { useEffect, useMemo, useRef, useState } from 'react'
import type { FormEvent, ReactNode } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import CircularProgress from '@mui/material/CircularProgress'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import type { AuthnStep } from '@speed/auth-core'
import { formatSecretForDisplay, parseOtpauth } from '@speed/auth-core/otpauth'
import { WebauthnError, runWebauthn, webauthnStepOf } from '@speed/auth-core/webauthn'
import {
  ActionButton,
  ActionRow,
  AnnouncementRegion,
  TotpEnrollmentPanel,
  useActivationGuard,
} from '@speed/ui-kit'
import { useAccountUiTranslation } from './translation.js'

/** A name outside this map renders verbatim, never a translated guess. */
const FIELD_LABEL_KEYS: Readonly<Record<string, string>> = {
  identifier: 'flowStep.field.identifier',
  password: 'flowStep.field.password',
  new_password: 'flowStep.field.newPassword',
  phone: 'flowStep.field.phone',
  code: 'flowStep.codeLabel',
}

/** An unknown kind renders its own name, never a generic label. */
const FACTOR_KIND_KEYS: Readonly<Record<string, string>> = {
  totp: 'mfa.factor.kind.totp',
  webauthn: 'mfa.factor.kind.webauthn',
  sms: 'mfa.factor.kind.sms',
}

function fieldLabel(name: string, t: (key: string) => string): string {
  const key = FIELD_LABEL_KEYS[name]
  return key === undefined ? name : t(key)
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
 * Labels a factor step's entries. A kind offered once reads as its kind name;
 * a repeated kind is told apart by its own name, or from the second on by an
 * ordinal, so two entries never read the same.
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

export interface FlowStepAnswer {
  readonly step: string
  readonly input?: Readonly<Record<string, string>>
}

export interface FlowStepProps {
  readonly step: AuthnStep
  /** The flow's step number (`seq`). The assertion ceremony keys on it with
   * the challenge, so a re-sent challenge starts afresh and a re-render does
   * not. */
  readonly sequence: number
  /** True while the answer is in flight: fields turn read-only and other
   * answer controls refuse activation. */
  readonly busy: boolean
  /** True while a throttled answer's wait holds the primary action back: the
   * fields stay editable and the held controls describe the wait through
   * `submitUnavailableDescribedBy`. */
  readonly submitDisabled?: boolean
  /** The id of the page text explaining what holds the step back. */
  readonly submitUnavailableDescribedBy?: string
  /** The answer step the input travels under: `answer` in the first factor,
   * `factor` in the second, `enroll` in a registration. */
  readonly answerStep: string
  readonly submitLabel: string
  readonly onSubmit: (answer: FlowStepAnswer) => void
  /** Reports a `redirect` step's URL upward; the host navigates. */
  readonly onRedirect?: (url: string) => void
  /** The id the challenge form carries when the host renders the submit
   * control outside the step; the form then renders no button of its own. */
  readonly formId?: string
  /** A secondary control rendered before the primary submit, or below a
   * choice list. */
  readonly secondaryAction?: ReactNode
  /** Challenge fields rendered read-only with their value; the answer carries
   * that value. */
  readonly lockedFields?: Readonly<Record<string, string>>
}

export function FlowStep({
  step,
  sequence,
  busy,
  submitDisabled = false,
  submitUnavailableDescribedBy,
  answerStep,
  submitLabel,
  onSubmit,
  onRedirect,
  formId,
  secondaryAction,
  lockedFields,
}: FlowStepProps) {
  const { t } = useAccountUiTranslation()
  const [values, setValues] = useState<Record<string, string>>({})
  const [activated, setActivated] = useState<string | null>(null)
  // Guard the form's own submit too: a field's implicit submission must be
  // refused while the answer is in flight or the step is held.
  const handleSubmit = (event: FormEvent): void => {
    event.preventDefault()
    onSubmit({ step: answerStep, input: { ...values, ...lockedFields } })
  }
  const guardedSubmit = useActivationGuard(handleSubmit, {
    busy,
    unavailable: submitDisabled,
  })
  const heldDescribedBy = submitDisabled
    ? submitUnavailableDescribedBy
    : undefined

  if (step.kind === 'complete') {
    return (
      <ActionRow sx={{ mt: 2 }}>
        {secondaryAction}
        <ActionButton
          type="button"
          emphasis="primary"
          busy={busy}
          unavailable={submitDisabled}
          unavailableDescribedBy={heldDescribedBy}
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
        <Alert severity="info">
          {t('flowStep.redirectNotice')}
        </Alert>
        <ActionRow sx={{ mt: 1.5 }}>
          {secondaryAction}
          <ActionButton
            type="button"
            emphasis="primary"
            busy={busy}
            unavailable={submitDisabled || onRedirect === undefined}
            unavailableDescribedBy={heldDescribedBy}
            onClick={() => onRedirect?.(step.url)}
          >
            {t('flowStep.continueLabel')}
          </ActionButton>
        </ActionRow>
      </Box>
    )
  }

  if (step.kind === 'factor') {
    const factorLabels = factorChoiceLabels(
      step.factors,
      (kind) => factorKindLabel(kind, t),
      (kind, name) => t('mfa.factor.optionNamed', { kind, name }),
      (kind, position) => t('mfa.factor.optionOrdinal', { kind, position }),
    )
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1, mt: 2 }}>
        {step.factors.map((factor, index) => (
          <ActionButton
            key={factor.id}
            type="button"
            busy={busy && activated === factor.id}
            unavailable={submitDisabled || (busy && activated !== factor.id)}
            unavailableDescribedBy={!busy ? heldDescribedBy : undefined}
            onClick={() => {
              setActivated(factor.id)
              onSubmit({ step: 'factor', input: { factor: factor.id } })
            }}
          >
            {factorLabels[index] ?? factorKindLabel(factor.kind, t)}
          </ActionButton>
        ))}
        {secondaryAction === undefined ? null : (
          <Box sx={{ alignSelf: 'flex-start', mt: 0.5 }}>{secondaryAction}</Box>
        )}
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
            unavailable={submitDisabled || (busy && activated !== kind)}
            unavailableDescribedBy={!busy ? heldDescribedBy : undefined}
            onClick={() => {
              setActivated(kind)
              onSubmit({ step: 'enroll', input: { kind } })
            }}
          >
            {factorKindLabel(kind, t)}
          </ActionButton>
        ))}
        {secondaryAction === undefined ? null : (
          <Box sx={{ alignSelf: 'flex-start', mt: 0.5 }}>{secondaryAction}</Box>
        )}
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
          busy={busy}
          unavailable={submitDisabled}
          unavailableDescribedBy={heldDescribedBy}
          submitLabel={submitLabel}
          onSubmit={(code) => onSubmit({ step: answerStep, input: { code } })}
          secondaryAction={secondaryAction}
        />
      )
    }
    return (
      <PasskeyPrompt
        options={options}
        sequence={sequence}
        busy={busy}
        unavailable={submitDisabled}
        unavailableDescribedBy={heldDescribedBy}
        answerStep={answerStep}
        submitLabel={submitLabel}
        onSubmit={onSubmit}
        secondaryAction={secondaryAction}
      />
    )
  }

  const external = formId !== undefined
  return (
    <Box component="form" id={formId} onSubmit={guardedSubmit} sx={{ mt: 2 }}>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
        {step.fields.map((field) => {
          const locked = lockedFields?.[field.name]
          return (
            <TextField
              key={field.name}
              label={fieldLabel(field.name, t)}
              type={field.secret === true ? 'password' : 'text'}
              value={locked ?? values[field.name] ?? ''}
              onChange={(event) =>
                setValues({ ...values, [field.name]: event.target.value })
              }
              // Read-only, not disabled: the value stays readable, focusable
              // and in the tab order.
              slotProps={{
                input: { readOnly: locked !== undefined || busy },
              }}
              helperText={
                locked === undefined
                  ? undefined
                  : field.name === 'phone'
                    ? t('flowStep.lockedPhoneHelp')
                    : t('flowStep.lockedIdentifierHelp')
              }
              required={field.required === true}
              fullWidth
              autoComplete={field.secret === true ? 'one-time-code' : 'off'}
            />
          )
        })}
      </Box>
      {external ? null : (
        <ActionRow sx={{ mt: 2 }}>
          {secondaryAction}
          <ActionButton
            type="submit"
            emphasis="primary"
            busy={busy}
            unavailable={submitDisabled}
            unavailableDescribedBy={heldDescribedBy}
          >
            {submitLabel}
          </ActionButton>
        </ActionRow>
      )}
    </Box>
  )
}

interface TotpEnrollStepProps {
  readonly uri: string | null
  readonly code: string
  readonly onCodeChange: (code: string) => void
  readonly busy: boolean
  readonly unavailable: boolean
  readonly unavailableDescribedBy?: string
  readonly submitLabel: string
  readonly onSubmit: (code: string) => void
  readonly secondaryAction?: ReactNode
}

/**
 * The authenticator-app enrolment block, wrapping ui-kit's panel. The URI is
 * never rendered as text; one that does not parse renders neither the QR
 * symbol nor the manual-entry block, so the panel advertises no code it did
 * not draw.
 */
function TotpEnrollStep({
  uri,
  code,
  onCodeChange,
  busy,
  unavailable,
  unavailableDescribedBy,
  submitLabel,
  onSubmit,
  secondaryAction,
}: TotpEnrollStepProps) {
  const { t } = useAccountUiTranslation()
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
          { label: t('flowStep.totpAlgorithm'), value: otpauth.algorithm },
          { label: t('flowStep.totpDigits'), value: String(otpauth.digits) },
          { label: t('flowStep.totpPeriod'), value: String(otpauth.period) },
        ]
  const codeEmpty = code.trim() === ''
  const explanation =
    unavailable && unavailableDescribedBy !== undefined
      ? { unavailableDescribedBy }
      : !unavailable && codeEmpty
        ? { unavailableReason: t('flowStep.codeRequired') }
        : {}

  return (
    <TotpEnrollmentPanel
      otpauthUri={otpauth === null ? undefined : (uri ?? undefined)}
      qrLabel={t('flowStep.totpQrLabel')}
      guidance={otpauth === null ? undefined : t('flowStep.enrollTotpHelp')}
      manualToggleLabel={t('flowStep.totpManualToggle')}
      secret={
        otpauth === null
          ? undefined
          : {
              value: otpauth.secret,
              display: formatSecretForDisplay(otpauth.secret),
            }
      }
      secretLabel={t('flowStep.totpSecretLabel')}
      copyLabel={t('flowStep.totpCopyLabel')}
      copiedLabel={t('flowStep.totpCopiedLabel')}
      copyVisibleLabel={t('flowStep.totpCopyVisibleLabel')}
      parameters={parameters}
      codeLabel={t('flowStep.codeLabel')}
      code={code}
      onCodeChange={onCodeChange}
      busy={busy}
      secondaryAction={secondaryAction}
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
  /** The flow's step number (`seq`); see the arrival effect. */
  readonly sequence: number
  readonly busy: boolean
  readonly unavailable: boolean
  readonly unavailableDescribedBy?: string
  readonly answerStep: string
  readonly submitLabel: string
  readonly onSubmit: (answer: FlowStepAnswer) => void
  readonly secondaryAction?: ReactNode
}

/**
 * The passkey ceremony step: drives the browser's WebAuthn ceremony and
 * reports the credential it returns. An assertion step starts on arrival with
 * no start control; a registration waits for a click because a creation
 * ceremony needs user activation. Cancel fails at once and only best-effort
 * aborts the ceremony; a late answer from an abandoned attempt is discarded.
 */
function PasskeyPrompt({
  options,
  sequence,
  busy,
  unavailable,
  unavailableDescribedBy,
  answerStep,
  submitLabel,
  onSubmit,
  secondaryAction,
}: PasskeyPromptProps) {
  const { t } = useAccountUiTranslation()
  const step = webauthnStepOf(options)
  const registering = step === 'register'
  const arrivalKey = assertionKey(options, sequence)
  const [working, setWorking] = useState(arrivalKey !== null)
  const [failure, setFailure] = useState<PasskeyFailure | null>(null)
  const cancelRef = useRef<HTMLButtonElement>(null)
  // The ceremony being waited on. Cancel clears it first, so a late answer
  // cannot be mistaken for a retry's.
  const pendingRef = useRef<{
    attempt: number
    controller: AbortController
  } | null>(null)
  const attemptRef = useRef(0)
  // The latest `run`, read by the arrival effect.
  const runRef = useRef<() => Promise<void>>(async () => {})

  // Abandon the ceremony with the surface.
  useEffect(
    () => () => {
      const pending = pendingRef.current
      pendingRef.current = null
      pending?.controller.abort()
    },
    [],
  )

  // The control that had focus unmounted, so focus moves to cancel.
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
    setFailure(null)
    try {
      const credential = await runWebauthn(options, controller.signal)
      if (pendingRef.current?.attempt === attempt) {
        onSubmit({ step: answerStep, input: { credential } })
      }
    } catch (error) {
      if (pendingRef.current?.attempt === attempt) {
        setFailure(webauthnErrorKey(error, registering))
      }
    } finally {
      if (pendingRef.current?.attempt === attempt) {
        pendingRef.current = null
        setWorking(false)
      }
    }
  }
  // Declared before the arrival effect so `runRef` is current when it fires.
  useEffect(() => {
    runRef.current = run
  })

  // Keyed on the challenge and sequence, not the options object: a re-render
  // leaves the ceremony alone, a new or re-sent challenge restarts it.
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
    setWorking(false)
    setFailure({ key: 'flowStep.passkeyCancelled' })
  }

  return (
    <Box sx={{ mt: 2 }}>
      <Typography variant="body2" color="text.secondary">
        {registering
          ? t('flowStep.passkeyRegisterPrompt')
          : t('flowStep.passkeyPrompt')}
      </Typography>
      {failure !== null && (
        <Box sx={{ mt: 1 }}>
          <Alert severity="error" role="alert">
            {failure.actionKey === undefined
              ? t(failure.key)
              : t(failure.key, { action: t(failure.actionKey) })}
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
              message={t('flowStep.passkeyWaiting')}
              sx={{ typography: 'body1' }}
            />
          </Box>
          <ActionRow sx={{ mt: 1.5 }}>
            <ActionButton
              ref={cancelRef}
              type="button"
              unavailable={busy}
              onClick={cancel}
            >
              {t('flowStep.passkeyCancel')}
            </ActionButton>
          </ActionRow>
        </>
      ) : (
        <ActionRow sx={{ mt: 2 }}>
          {secondaryAction}
          <ActionButton
            type="button"
            emphasis="primary"
            busy={busy}
            unavailable={unavailable}
            unavailableDescribedBy={unavailable ? unavailableDescribedBy : undefined}
            onClick={() => void run()}
          >
            {failure === null ? submitLabel : t('flowStep.passkeyRetry')}
          </ActionButton>
        </ActionRow>
      )}
    </Box>
  )
}

/** The identity of an assertion step's ceremony: the step number plus the
 * challenge. A registration step returns `null` because it starts on a click. */
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

interface PasskeyFailure {
  readonly key: string
  readonly actionKey?: string
}

function webauthnErrorKey(failure: unknown, registering: boolean): PasskeyFailure {
  const actionKey = registering
    ? 'flowStep.passkeyActionRegister'
    : 'flowStep.passkeyActionSignIn'
  if (failure instanceof WebauthnError) {
    switch (failure.reason) {
      case 'unsupported':
        return { key: 'flowStep.passkeyUnsupported' }
      case 'cancelled':
        return { key: 'flowStep.passkeyCancelled' }
      case 'already-registered':
        return { key: 'flowStep.passkeyAlreadyRegistered' }
      default:
        return { key: 'flowStep.passkeyFailed', actionKey }
    }
  }
  return { key: 'flowStep.passkeyFailed', actionKey }
}

/** The `otpauth_uri` a step's options carry, or null. Only this one field of
 * the step's JSON is ever rendered. */
function otpauthUriOf(options: unknown): string | null {
  if (typeof options !== 'object' || options === null) {
    return null
  }
  const uri = (options as Record<string, unknown>).otpauth_uri
  return typeof uri === 'string' && uri !== '' ? uri : null
}
