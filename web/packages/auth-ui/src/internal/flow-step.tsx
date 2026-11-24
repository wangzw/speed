/** The step renderer of the sign-in family: a parked flow step's fields, and
 * the human-verification wrapper around a step that may send. */

import { Fragment, useEffect, useId, useMemo, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Alert from '@mui/material/Alert'
import CircularProgress from '@mui/material/CircularProgress'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useForm } from 'react-hook-form'
import type { SubmitHandler } from 'react-hook-form'
import type { AuthnField } from '@speed/auth-core'
import type { AuthSession, AuthnHumanGate } from '@speed/auth-core'
import { humanGateNeedsProof, useHumanVerification } from '@speed/auth-core'
import {
  ActionButton,
  ActionRow,
  AnnouncementRegion,
  FormField,
  FormLayout,
  HumanVerification as HumanVerificationBlock,
  TotpEnrollmentPanel,
  useActivationGuard,
} from '@speed/ui-kit'
import { useAuthUiTranslation } from './translation.js'
import { fieldHelp, fieldLabel, methodLabel } from './labels.js'
import {
  formatSecretForDisplay,
  parseOtpauth,
} from '@speed/auth-core/otpauth'
import { WebauthnError, runWebauthn, webauthnStepOf } from '@speed/auth-core/webauthn'
import type { OpenFlowState } from './flow.js'
import { answerSends, stepMaySend } from './flow.js'
import { firstControl } from './step-focus.js'

const FACTOR_KINDS = ['totp', 'sms', 'webauthn'] as const

interface FactorChoice {
  readonly kind: string
  readonly label?: string
}

/** One label per offered factor: the kind alone unless one kind repeats, then
 * "Kind · Name" or an ordinal ("Kind (2)") so two entries never read the same. */
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

/** Where the identity sentence splits around the provider's name. */
const IDENTITY_MARKER = '\u0000'

const USERNAME_FIELD = 'username'

const PASSWORD_FIELD = 'password'

const DISPLAY_NAME_FIELD = 'display_name'

/** The server's cap on a display name's length. */
const DISPLAY_NAME_MAX_LENGTH = 100

function fieldAutoComplete(field: AuthnField): string {
  if (field.name === DISPLAY_NAME_FIELD) {
    return 'name'
  }
  return field.kind === 'code' ? 'one-time-code' : 'off'
}

function fieldMaxLength(field: AuthnField): number | undefined {
  return field.name === DISPLAY_NAME_FIELD ? DISPLAY_NAME_MAX_LENGTH : undefined
}

function asksForPassword(step: OpenFlowState): boolean {
  return (
    step.next.kind === 'challenge' &&
    step.next.fields.some((field) => field.name === PASSWORD_FIELD)
  )
}

function usernameRefusalKey(code: string | null): string | null {
  switch (code) {
    case 'authn.username_taken':
      return 'signInFlow.usernameTaken'
    case 'authn.username_reserved':
      return 'signInFlow.usernameReserved'
    case 'authn.username_invalid':
      return 'signInFlow.usernameInvalid'
    default:
      return null
  }
}

/** How a still-parked signup step carries a refusal: the field it belongs to
 * and its text key, or null for the whole-attempt banner. */
export function signupFieldRefusal(
  step: OpenFlowState,
  code: string | null,
): { readonly name: string; readonly key: string } | null {
  if (step.state !== 'signup' || code === null) {
    return null
  }
  const key = usernameRefusalKey(code)
  if (key !== null) {
    return { name: USERNAME_FIELD, key }
  }
  if (code === 'authn.bad_request' && asksForPassword(step)) {
    return { name: PASSWORD_FIELD, key: 'signInFlow.signupPasswordPolicy' }
  }
  return null
}

export interface FlowStepFieldsProps {
  /** The open step to render. */
  readonly step: OpenFlowState
  /** A request is in flight; the step's controls stay focusable but refuse
   * further activation. */
  readonly busy: boolean
  /** Report the answered fields upward (the driver sends them). */
  readonly onAnswer: (input: Record<string, string>) => void
  /** The exit attached to the unsupported-step notice. Required because every
   * rendered step carries at least one control. */
  readonly onUnsupported: () => void
  /** The exit's label; omitted uses the family's own wording. */
  readonly unsupportedLabel?: string
  /** The client-side ceremony failure the channel's feedback scope holds, as a
   * bundle key. */
  readonly clientFailure?: string | null
  /** Report a ceremony failure upward, or `null` when a retry begins. */
  readonly onClientFailure?: (key: string | null) => void
  /** The code of an answer refused while this step stayed parked; a signup
   * step renders it under the field it names. */
  readonly failure?: string | null
}

export interface HumanGatedFlowStepProps {
  /** The open step to render. */
  readonly step: OpenFlowState
  /** A request is in flight; the step's controls stay focusable but refuse
   * further activation. */
  readonly busy: boolean
  /** The session whose human-verification gate and proof the step uses. */
  readonly session: AuthSession
  /** The gate a host already read, or omitted to read it here. */
  readonly humanGate?: AuthnHumanGate
  /** Report the answered fields upward, with the proof an answer that may send
   * presents. */
  readonly onAnswer: (
    input: Record<string, string>,
    humanProof?: string,
  ) => void
  readonly onUnsupported: () => void
  readonly unsupportedLabel?: string
  readonly clientFailure?: string | null
  readonly onClientFailure?: (key: string | null) => void
  readonly failure?: string | null
  /** An attempt is beginning: the caller clears whatever result the previous
   * attempt left beside this step. */
  readonly onAttemptStart?: () => void
}

/**
 * Wraps the step renderer with the human-verification block: only an answer
 * that may send waits for a proof, and the held answer's cancel control is the
 * way back when none can be obtained.
 */
export function HumanGatedFlowStep({
  session,
  humanGate,
  step,
  busy,
  onAnswer,
  onUnsupported,
  unsupportedLabel,
  clientFailure,
  onClientFailure,
  failure,
  onAttemptStart,
}: HumanGatedFlowStepProps) {
  const { t } = useAuthUiTranslation()
  const humanScope = stepMaySend(step) ? 'phone' : 'other'
  // The chosen answer, held while the gate is read and its proof obtained.
  const [pending, setPending] = useState<Record<string, string> | null>(null)
  const human = useHumanVerification(session, {
    scope: humanScope,
    enabled: pending !== null,
    ...(humanGate === undefined ? {} : { gate: humanGate }),
  })
  const gateDocument = humanGate ?? human.gate
  const needsProof = humanGateNeedsProof(gateDocument, humanScope)
  // Behind a ref so the deferred path calls the latest render's callback.
  const onAnswerRef = useRef(onAnswer)
  onAnswerRef.current = onAnswer
  // The control that started a held answer; the cancel control unmounts with
  // the wait, so focus returns to this or the step's first control.
  const answerOriginRef = useRef<HTMLElement | null>(null)
  const focusAfterCancel = useRef(false)
  const stepContainerRef = useRef<HTMLDivElement | null>(null)

  const answer = (input: Record<string, string>): void => {
    if (pending !== null) {
      return
    }
    onAttemptStart?.()
    const sending = answerSends(step, input)
    const gateKnown = !human.pending && gateDocument !== null
    if (sending && (!gateKnown || (needsProof && human.proof === null))) {
      // Only a sending answer waits; an answer that sends nothing leaves at once.
      const active = document.activeElement
      answerOriginRef.current = active instanceof HTMLElement ? active : null
      setPending(input)
      return
    }
    const presented = sending && needsProof ? human.proof : null
    onAnswerRef.current(input, presented ?? undefined)
    if (presented !== null) {
      human.consume()
    }
  }

  // A held answer leaves by itself once the gate is settled and its proof is in
  // hand. The `busy` guard is defensive: while an answer is held, this step's
  // controls are held too, so no write can start beside it.
  useEffect(() => {
    if (pending === null || human.pending || gateDocument === null) {
      return
    }
    if (needsProof && human.proof === null) {
      return
    }
    if (busy) {
      return
    }
    const input = pending
    setPending(null)
    const presented = answerSends(step, input) && needsProof ? human.proof : null
    onAnswerRef.current(input, presented ?? undefined)
    if (presented !== null) {
      human.consume()
    }
  }, [
    pending,
    human.pending,
    human.proof,
    human.consume,
    gateDocument,
    needsProof,
    step,
    busy,
  ])

  // A cancelled answer hands focus back to the control that asked, or to the
  // step's first control when that one left with the step.
  useEffect(() => {
    if (pending !== null || !focusAfterCancel.current) {
      return
    }
    focusAfterCancel.current = false
    const origin = answerOriginRef.current
    answerOriginRef.current = null
    const originFocusable =
      origin !== null &&
      origin.isConnected &&
      !origin.hasAttribute('disabled') &&
      origin.getAttribute('aria-hidden') !== 'true' &&
      origin.closest('[inert]') === null
    const target =
      originFocusable && origin !== null
        ? origin
        : stepContainerRef.current === null
          ? null
          : firstControl(stepContainerRef.current)
    target?.focus()
  }, [pending])

  const cancelHeld = (): void => {
    focusAfterCancel.current = true
    setPending(null)
  }

  return (
    <Box ref={stepContainerRef} sx={{ display: 'contents' }}>
      {pending !== null ? (
        <>
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
          {/* The way back when no proof can be obtained: dropping the held
              answer re-enables the step. */}
          <ActionRow>
            <ActionButton type="button" onClick={cancelHeld}>
              {t('signInFlow.passkeyCancel')}
            </ActionButton>
          </ActionRow>
        </>
      ) : null}
      <FlowStepFields
        step={step}
        busy={busy || pending !== null}
        onAnswer={answer}
        onUnsupported={onUnsupported}
        unsupportedLabel={unsupportedLabel}
        clientFailure={clientFailure}
        onClientFailure={onClientFailure}
        failure={failure}
      />
    </Box>
  )
}

/**
 * Renders a parked flow step by its `next.kind`: challenge fields, factor and
 * enroll choices, a TOTP or passkey ceremony, or the unsupported-step notice.
 * It drives nothing and holds no session.
 */
export function FlowStepFields({
  step,
  busy,
  onAnswer,
  onUnsupported,
  unsupportedLabel,
  clientFailure,
  onClientFailure,
  failure,
}: FlowStepFieldsProps) {
  const { t } = useAuthUiTranslation()
  const labelId = useId()

  const labelOf = (field: AuthnField): string => fieldLabel(field, t)
  const helpOf = (field: AuthnField): string | null => fieldHelp(field, t)

  const kindLabel = (kind: string): string =>
    (FACTOR_KINDS as readonly string[]).includes(kind)
      ? t(`signInFlow.factorKind.${kind}`)
      : kind

  const refusal = signupFieldRefusal(step, failure ?? null)
  const fieldRefusal =
    refusal === null ? null : { name: refusal.name, text: t(refusal.key) }

  switch (step.next.kind) {
    case 'challenge': {
      if (step.state === 'signup' && asksForPassword(step)) {
        return (
          <SignupAccountFields
            fields={step.next.fields}
            method={step.next.provided_method}
            displayName={step.next.provided_display_name}
            email={step.next.provided_email}
            refusal={fieldRefusal}
            fieldLabel={labelOf}
            fieldHelp={helpOf}
            submit={t('signInFlow.signupAccountSubmit')}
            busy={busy}
            onAnswer={onAnswer}
          />
        )
      }
      const fields = (
        <ChallengeFields
          fields={step.next.fields}
          fieldLabel={labelOf}
          fieldHelp={helpOf}
          submit={t('signInFlow.submit')}
          busy={busy}
          onAnswer={onAnswer}
          fieldError={
            fieldRefusal !== null &&
            step.next.fields.some((field) => field.name === fieldRefusal.name)
              ? fieldRefusal
              : null
          }
        />
      )
      if (step.state !== 'signup') {
        return fields
      }
      return (
        <Box
          sx={{
            display: 'flex',
            flexDirection: 'column',
            gap: 1.5,
            width: '100%',
          }}
        >
          <Box>
            <Typography component="h2" variant="h6">
              {t('signInFlow.signupTitle')}
            </Typography>
            <Typography variant="body2" color="text.secondary">
              {t('signInFlow.signupDescription')}
            </Typography>
          </Box>
          {fields}
        </Box>
      )
    }
    case 'factor': {
      const factorLabels = factorChoiceLabels(
        step.next.factors,
        kindLabel,
        (kind, name) => t('signInFlow.factorOptionNamed', { kind, name }),
        (kind, position) =>
          t('signInFlow.factorOptionOrdinal', { kind, position }),
      )
      return (
        <Box>
          <Choices
            labelId={labelId}
            title={t('signInFlow.factorTitle')}
            busy={busy}
            choices={step.next.factors.map((factor, index) => ({
              key: factor.id,
              label: factorLabels[index] ?? kindLabel(factor.kind),
            }))}
            onChoose={(id) => onAnswer({ factor: id })}
          />
          {/* No self-service recovery: an operator reset. Not shown when the
              step offers nothing to choose. */}
          {step.next.factors.length > 0 ? (
            <Typography
              variant="body2"
              color="text.secondary"
              sx={{ mt: 1.5 }}
            >
              {t('signInFlow.lostFactorHint')}
            </Typography>
          ) : null}
        </Box>
      )
    }
    case 'enroll':
      return (
        <Choices
          labelId={labelId}
          title={t('signInFlow.enrollTitle')}
          busy={busy}
          choices={step.next.kinds.map((kind) => ({
            key: kind,
            label: kindLabel(kind),
          }))}
          onChoose={(kind) => onAnswer({ kind })}
        />
      )
    case 'webauthn':
      return webauthnStepOf(step.next.options) === 'totp' ? (
        <TotpFields
          options={step.next.options}
          busy={busy}
          submit={t('signInFlow.submit')}
          onAnswer={onAnswer}
        />
      ) : (
        <PasskeyFields
          options={step.next.options}
          sequence={step.seq}
          busy={busy}
          submit={t('signInFlow.submit')}
          onAnswer={onAnswer}
          clientFailure={clientFailure ?? null}
          onClientFailure={onClientFailure}
        />
      )
    case 'complete':
      return null
    default:
      // A redirect step belongs to the host; the exit is the only control here,
      // so it is not disabled by `busy`.
      return (
        <Box
          sx={{
            display: 'flex',
            flexDirection: 'column',
            gap: 1.5,
            width: '100%',
          }}
        >
          <Alert severity="info" role="status" sx={{ width: '100%' }}>
            {t('signInFlow.unsupportedStep')}
          </Alert>
          <ActionRow>
            <ActionButton type="button" onClick={onUnsupported}>
              {unsupportedLabel ?? t('signInFlow.unsupportedStepAction')}
            </ActionButton>
          </ActionRow>
        </Box>
      )
  }
}

/** The TOTP enrollment step: parses `otpauth_uri` and renders it through
 * ui-kit's `TotpEnrollmentPanel`. A URI that does not parse renders neither the
 * symbol nor the manual block, and the URI is never shown as text. */
function TotpFields({
  options,
  busy,
  submit,
  onAnswer,
}: {
  readonly options: unknown
  readonly busy: boolean
  readonly submit: string
  readonly onAnswer: (input: Record<string, string>) => void
}) {
  const { t } = useAuthUiTranslation()
  const [value, setValue] = useState('')
  const uri = otpauthUriOf(options)
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
          { label: t('signInFlow.totpAlgorithm'), value: otpauth.algorithm },
          { label: t('signInFlow.totpDigits'), value: String(otpauth.digits) },
          { label: t('signInFlow.totpPeriod'), value: String(otpauth.period) },
        ]

  return (
    <TotpEnrollmentPanel
      otpauthUri={otpauth === null ? undefined : (uri ?? undefined)}
      qrLabel={t('signInFlow.totpQrLabel')}
      guidance={otpauth === null ? undefined : t('signInFlow.enrollTotpHelp')}
      manualToggleLabel={t('signInFlow.totpManualToggle')}
      secret={
        otpauth === null
          ? undefined
          : {
              value: otpauth.secret,
              display: formatSecretForDisplay(otpauth.secret),
            }
      }
      secretLabel={t('signInFlow.totpSecretLabel')}
      copyLabel={t('signInFlow.totpCopyLabel')}
      copiedLabel={t('signInFlow.totpCopiedLabel')}
      copyVisibleLabel={t('signInFlow.totpCopyVisibleLabel')}
      parameters={parameters}
      codeLabel={t('signInFlow.codeLabel')}
      code={value}
      onCodeChange={setValue}
      busy={busy}
      primaryAction={
        <ActionButton
          type="button"
          emphasis="primary"
          busy={busy}
          unavailable={value.trim() === ''}
          unavailableReason={t('signInFlow.totpCodeRequired')}
          onClick={() => onAnswer({ code: value.trim() })}
        >
          {submit}
        </ActionButton>
      }
    />
  )
}

/** The passkey ceremony step: an assertion starts on arrival, a registration
 * on a click. Cancel, unmount and a late settlement all invalidate the attempt,
 * so only the live ceremony answers. */
function PasskeyFields({
  options,
  sequence,
  busy,
  submit,
  onAnswer,
  clientFailure,
  onClientFailure,
}: {
  readonly options: unknown
  /** The flow's step number; the arrival effect keys on it with the challenge,
   * so a re-sent challenge is a new ceremony while a re-render is not. */
  readonly sequence: number
  readonly busy: boolean
  readonly submit: string
  readonly onAnswer: (input: Record<string, string>) => void
  readonly clientFailure: string | null
  readonly onClientFailure?: (key: string | null) => void
}) {
  const { t } = useAuthUiTranslation()
  // The ceremony: a passkey login's assertion, or one to register. The prompt
  // and the generic failure sentence both name it.
  const registering = webauthnStepOf(options) === 'register'
  // Assertion steps start on arrival, so the first render is already the
  // waiting state.
  const arrivalKey = assertionKey(options, sequence)
  const [working, setWorking] = useState(arrivalKey !== null)
  // The scope owns the failure and its banner; this step only reports it.
  const errorKey = clientFailure
  const cancelRef = useRef<HTMLButtonElement>(null)
  // The ceremony being waited on. Cancel clears it before the failure renders,
  // so its late settlement and a retry's own attempt stay apart.
  const pendingRef = useRef<{
    attempt: number
    controller: AbortController
  } | null>(null)
  const attemptRef = useRef(0)
  // Read by the arrival effect, which keys on the challenge and sequence rather
  // than this function, so a re-render leaves a running ceremony alone.
  const runRef = useRef<() => Promise<void>>(async () => {})

  // Abandon the ceremony with the surface: invalidate the attempt and ask the
  // browser to give it up, so a late settlement answers nothing.
  useEffect(
    () => () => {
      const pending = pendingRef.current
      pendingRef.current = null
      pending?.controller.abort()
    },
    [],
  )

  // Take the focus the unmounted control held; the live region handles its own
  // empty birth frame.
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
    onClientFailure?.(null)
    try {
      const credential = await runWebauthn(options, controller.signal)
      if (pendingRef.current?.attempt === attempt) {
        onAnswer({ credential })
      }
    } catch (failure) {
      if (pendingRef.current?.attempt === attempt) {
        onClientFailure?.(webauthnErrorKey(failure, registering))
      }
    } finally {
      if (pendingRef.current?.attempt === attempt) {
        pendingRef.current = null
        setWorking(false)
      }
    }
  }
  // Publish the latest `run`; declared before the arrival effect so the
  // assignment lands first.
  useEffect(() => {
    runRef.current = run
  })

  // An assertion starts on arrival, a registration on a click; the key is the
  // challenge and sequence, so a re-render keeps the ceremony while a re-sent
  // step restarts it. Under StrictMode only the live attempt answers.
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
    // Fail now; the ceremony's own late settlement no longer matches pendingRef.
    setWorking(false)
    onClientFailure?.('signInFlow.passkeyCancelled')
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5, width: '100%' }}>
      <Typography variant="body2" color="text.secondary">
        {registering
          ? t('signInFlow.passkeyRegisterPrompt')
          : t('signInFlow.passkeyPrompt')}
      </Typography>
      {working ? (
        <>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
            <CircularProgress size={18} aria-hidden={true} />
            <AnnouncementRegion
              message={t('signInFlow.passkeyWaiting')}
              sx={{ typography: 'body1' }}
            />
          </Box>
          <ActionRow>
            <ActionButton
              ref={cancelRef}
              type="button"
              unavailable={busy}
              onClick={cancel}
            >
              {t('signInFlow.passkeyCancel')}
            </ActionButton>
          </ActionRow>
        </>
      ) : (
        <ActionRow>
          <ActionButton
            type="button"
            emphasis="primary"
            busy={busy}
            onClick={() => void run()}
          >
            {errorKey === null ? submit : t('signInFlow.passkeyRetry')}
          </ActionButton>
        </ActionRow>
      )}
    </Box>
  )
}

/** An assertion ceremony's arrival key: step number plus challenge. A
 * registration returns null (it starts on a click). */
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

/** The bundle key of a failed ceremony, by its reason and the ceremony ran. */
function webauthnErrorKey(failure: unknown, registering: boolean): string {
  if (failure instanceof WebauthnError) {
    switch (failure.reason) {
      case 'unsupported':
        return 'signInFlow.passkeyUnsupported'
      case 'cancelled':
        return 'signInFlow.passkeyCancelled'
      case 'already-registered':
        return 'signInFlow.passkeyAlreadyRegistered'
      default:
        return registering
          ? 'signInFlow.passkeyFailedRegister'
          : 'signInFlow.passkeyFailedSignIn'
    }
  }
  return registering
    ? 'signInFlow.passkeyFailedRegister'
    : 'signInFlow.passkeyFailedSignIn'
}

function otpauthUriOf(options: unknown): string | null {
  if (typeof options !== 'object' || options === null) {
    return null
  }
  const uri = (options as Record<string, unknown>).otpauth_uri
  return typeof uri === 'string' && uri !== '' ? uri : null
}

interface ChallengeFieldsProps {
  readonly fields: readonly AuthnField[]
  readonly fieldLabel: (field: AuthnField) => string
  readonly fieldHelp: (field: AuthnField) => string | null
  readonly submit: string
  readonly busy: boolean
  readonly onAnswer: (input: Record<string, string>) => void
  /** The refusal to render under one of the fields; that field carries
   * `aria-invalid`, the message as its description, and takes focus. */
  readonly fieldError?: { readonly name: string; readonly text: string } | null
}

function ChallengeFields({
  fields,
  fieldLabel,
  fieldHelp,
  submit,
  busy,
  onAnswer,
  fieldError = null,
}: ChallengeFieldsProps) {
  const defaults: Record<string, string> = {}
  for (const field of fields) {
    defaults[field.name] = ''
  }
  const form = useForm<Record<string, string>>({ defaultValues: defaults })
  const errorInputRef = useRef<HTMLInputElement | null>(null)
  const errorText = fieldError?.text ?? null
  // Move focus to a refused field so a viewer who submitted from the button
  // lands on the value to change.
  useEffect(() => {
    if (errorText !== null) {
      errorInputRef.current?.focus()
    }
  }, [errorText])
  const onSubmit: SubmitHandler<Record<string, string>> = (values) => {
    // An optional field left blank is omitted, which the server reads as no value.
    const answer: Record<string, string> = {}
    for (const field of fields) {
      const value = values[field.name] ?? ''
      if (field.required === true || value.trim() !== '') {
        answer[field.name] = value
      }
    }
    onAnswer(answer)
  }
  const guardedSubmit = useActivationGuard(onSubmit, { busy })
  return (
    <FormLayout
      form={form}
      onSubmit={guardedSubmit}
      actions={
        <ActionButton type="submit" emphasis="primary" busy={busy}>
          {submit}
        </ActionButton>
      }
    >
      {fields.map((field) => {
        const refused = fieldError !== null && fieldError.name === field.name
        const refusal = refused && fieldError !== null ? fieldError.text : null
        return (
          <FormField
            key={field.name}
            name={field.name}
            required={field.required === true}
            render={({ field: bound, invalid, errorText: formError }) => (
              <TextField
                {...bound}
                inputRef={refused ? errorInputRef : undefined}
                label={fieldLabel(field)}
                type={field.kind === 'password' || field.secret === true ? 'password' : 'text'}
                inputMode={field.kind === 'code' ? 'numeric' : undefined}
                autoComplete={fieldAutoComplete(field)}
                slotProps={{ htmlInput: { maxLength: fieldMaxLength(field) } }}
                fullWidth
                error={invalid || refused}
                helperText={formError ?? refusal ?? fieldHelp(field) ?? undefined}
              />
            )}
          />
        )
      })}
    </FormLayout>
  )
}

interface SignupAccountFieldsProps {
  readonly fields: readonly AuthnField[]
  /** The sign-in method name that opened this login, resolved for the identity
   * sentence; absent leaves the sentence out. */
  readonly method?: string
  readonly displayName?: string
  /** The provider-shared address, shown as the read-only block under the
   * username when present. */
  readonly email?: string
  readonly refusal: { readonly name: string; readonly text: string } | null
  readonly fieldLabel: (field: AuthnField) => string
  readonly fieldHelp: (field: AuthnField) => string | null
  readonly submit: string
  readonly busy: boolean
  readonly onAnswer: (input: Record<string, string>) => void
}

/** The confirmation input, compared here and never sent. */
const CONFIRM_PASSWORD_FIELD = 'confirmPassword'

type SignupAccountValues = Record<string, string>

/** The password account-opening face: the step's fields in the server's order,
 * a password's confirmation after it, and the provider-shared address as a
 * read-only block under the username. It carries no title of its own. */
function SignupAccountFields({
  fields,
  method,
  displayName,
  email,
  refusal,
  fieldLabel,
  fieldHelp,
  submit,
  busy,
  onAnswer,
}: SignupAccountFieldsProps) {
  const { t } = useAuthUiTranslation()
  const form = useForm<SignupAccountValues>({
    defaultValues: Object.fromEntries([
      ...fields.map((field) => [field.name, '']),
      [CONFIRM_PASSWORD_FIELD, ''],
    ]),
  })
  const inputRefs = useRef<Record<string, HTMLInputElement | null>>({})
  const refusedName = refusal?.name ?? null
  const refusedText = refusal?.text ?? null
  const methodName =
    method === undefined || method === ''
      ? null
      : methodLabel({ name: method }, t)
  const accountName =
    displayName === undefined || displayName === '' ? null : displayName
  const providedEmail =
    email === undefined || email === '' ? null : email
  const emailLabel = fieldLabel({ name: 'email', kind: 'text' })
  // One bundle entry per case, with a marker where the name goes so the name
  // is an element and the entry carries the language's own spacing.
  const identity = (() => {
    if (methodName === null) {
      return null
    }
    if (accountName === null) {
      return {
        before: t('signInFlow.signupIdentityWithoutName', {
          provider: methodName,
        }),
        after: '',
      }
    }
    const [before, after = ''] = t('signInFlow.signupIdentity', {
      provider: methodName,
      name: IDENTITY_MARKER,
    }).split(IDENTITY_MARKER)
    return { before, after }
  })()
  // Move focus to a refused field so the viewer lands on the value to change.
  useEffect(() => {
    if (refusedName !== null) {
      inputRefs.current[refusedName]?.focus()
    }
  }, [refusedName, refusedText])
  const onSubmit: SubmitHandler<SignupAccountValues> = (values) => {
    const answer: Record<string, string> = {}
    for (const field of fields) {
      const value = values[field.name] ?? ''
      if (field.required === true || value.trim() !== '') {
        answer[field.name] = value
      }
    }
    onAnswer(answer)
  }
  const guardedSubmit = useActivationGuard(onSubmit, { busy })
  // A new account, so the username and password carry the new-account hints.
  const autoCompleteOf = (field: AuthnField): string => {
    if (field.name === USERNAME_FIELD) {
      return 'username'
    }
    if (field.name === PASSWORD_FIELD) {
      return 'new-password'
    }
    return fieldAutoComplete(field)
  }
  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: 1.5,
        width: '100%',
      }}
    >
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          gap: 0.5,
          textAlign: 'center',
        }}
      >
        {identity === null ? null : (
          <Typography variant="body2" color="text.secondary">
            {identity.before}
            {accountName === null ? null : (
              <Box
                component="span"
                sx={{ color: 'text.primary', fontWeight: 600 }}
              >
                {accountName}
              </Box>
            )}
            {identity.after}
          </Typography>
        )}
        <Typography variant="body1" color="text.primary">
          {t('signInFlow.signupAccountInstruction')}
        </Typography>
      </Box>
      <Box
        component="hr"
        sx={{ border: 0, borderTop: 1, borderColor: 'divider', m: 0 }}
      />
      <FormLayout
        form={form}
        onSubmit={guardedSubmit}
        actions={
          <ActionButton type="submit" emphasis="primary" busy={busy}>
            {submit}
          </ActionButton>
        }
      >
        {fields.map((field) => {
          const refused = refusedName === field.name
          return (
            <Fragment key={field.name}>
              <FormField
                name={field.name}
                required={field.required === true}
                render={({ field: bound, invalid, errorText }) => (
                  <TextField
                    {...bound}
                    inputRef={(element) => {
                      inputRefs.current[field.name] = element
                    }}
                    label={fieldLabel(field)}
                    type={
                      field.kind === 'password' || field.secret === true
                        ? 'password'
                        : 'text'
                    }
                    inputMode={
                      field.kind === 'code' ? 'numeric' : undefined
                    }
                    autoComplete={autoCompleteOf(field)}
                    slotProps={{ htmlInput: { maxLength: fieldMaxLength(field) } }}
                    fullWidth
                    error={invalid || refused}
                    helperText={
                      errorText ??
                      (refused ? refusedText : null) ??
                      fieldHelp(field) ??
                      undefined
                    }
                  />
                )}
              />
              {field.name === USERNAME_FIELD && providedEmail !== null ? (
                <SignupProvidedEmail
                  label={emailLabel}
                  value={providedEmail}
                  helper={
                    methodName === null
                      ? t('signInFlow.signupEmailImmutable')
                      : t('signInFlow.signupEmailSource', {
                          provider: methodName,
                        })
                  }
                />
              ) : null}
              {field.name === PASSWORD_FIELD ? (
                <FormField
                  name={CONFIRM_PASSWORD_FIELD}
                  required
                  rules={{
                    validate: (value) =>
                      value === form.getValues(PASSWORD_FIELD) ||
                      t('signInFlow.signupPasswordMismatch'),
                  }}
                  render={({ field: bound, invalid, errorText }) => (
                    <TextField
                      {...bound}
                      label={t('register.confirmPasswordLabel')}
                      type="password"
                      autoComplete="new-password"
                      fullWidth
                      error={invalid}
                      helperText={errorText ?? undefined}
                    />
                  )}
                />
              ) : null}
            </Fragment>
          )
        })}
      </FormLayout>
    </Box>
  )
}

interface SignupProvidedEmailProps {
  readonly label: string
  readonly value: string
  readonly helper: string
}

/** The provider-shared address as a read-only textbox with wrapped text (not a
 * one-line input, which hard-truncates a long address), carrying its label and
 * source line. */
function SignupProvidedEmail({
  label,
  value,
  helper,
}: SignupProvidedEmailProps) {
  const labelId = useId()
  const helperId = useId()
  return (
    // The block keeps a line above it so a wrapped helper under the previous
    // field does not run into its floating label.
    <Box sx={{ position: 'relative', mt: 1 }}>
      <Box
        role="textbox"
        aria-readonly="true"
        aria-labelledby={labelId}
        aria-describedby={helperId}
        tabIndex={0}
        sx={{
          display: 'flex',
          alignItems: 'center',
          minHeight: 56,
          px: 1.75,
          py: 1.5,
          border: 1,
          borderColor: 'divider',
          borderRadius: '8px',
          backgroundColor: 'action.hover',
          overflowWrap: 'anywhere',
          userSelect: 'text',
        }}
      >
        <Typography
          variant="body1"
          sx={{ color: 'text.primary', lineHeight: '22px', minWidth: 0 }}
        >
          {value}
        </Typography>
      </Box>
      <Box
        component="span"
        id={labelId}
        sx={{
          position: 'absolute',
          top: '-8px',
          left: '12px',
          px: 0.5,
          backgroundColor: 'background.paper',
          color: 'text.secondary',
          fontSize: 12,
          lineHeight: '16px',
        }}
      >
        {label}
      </Box>
      <Typography
        id={helperId}
        component="p"
        sx={{
          mx: 1.75,
          mt: 0.5,
          color: 'text.secondary',
          fontSize: 12,
          lineHeight: '16px',
        }}
      >
        {helper}
      </Typography>
    </Box>
  )
}

interface Choice {
  readonly key: string
  readonly label: string
}

interface ChoicesProps {
  readonly labelId: string
  readonly title: string
  readonly busy: boolean
  readonly choices: readonly Choice[]
  readonly onChoose: (key: string) => void
}

/** A titled stack of buttons, one per choice: the chosen one reports busy, its
 * siblings unavailable. */
function Choices({ labelId, title, busy, choices, onChoose }: ChoicesProps) {
  const [chosen, setChosen] = useState<string | null>(null)
  return (
    <Box
      sx={{ display: 'flex', flexDirection: 'column', gap: 1, width: '100%' }}
    >
      <Alert severity="info" role="status" id={labelId} sx={{ width: '100%' }}>
        {title}
      </Alert>
      {choices.map((choice) => (
        <ActionButton
          key={choice.key}
          fullWidth
          busy={busy && chosen === choice.key}
          unavailable={busy && chosen !== choice.key}
          onClick={() => {
            setChosen(choice.key)
            onChoose(choice.key)
          }}
        >
          {choice.label}
        </ActionButton>
      ))}
    </Box>
  )
}
