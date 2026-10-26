/**
 * FlowStepFields: the step renderer of the sign-in family.
 *
 * After a channel form's own first fields were submitted, a login flow
 * can answer with another step instead of a completion -- the second
 * factor, a forced password change, a factor to register, a passkey
 * ceremony. This component renders that step into branch fields,
 * dispatching on the step's `next.kind`, and reports the answer upward;
 * it drives nothing itself and holds no session.
 *
 *  - `challenge`  -> one text input per field the server asks for, plus
 *    the continue action. The label of a field the family knows by name
 *    (identifier, password, new_password, phone, code) comes from the
 *    bundle (see ./labels); an unknown name renders verbatim, so a
 *    server-side field the family has no wording for shows its own name
 *    rather than a translated guess.
 *  - `factor`     -> one button per offered second factor; choosing one
 *    sends its id and opens the factor's own challenge.
 *  - `enroll`     -> one button per registerable factor kind.
 *  - `webauthn`   -> the factor's own JSON. TOTP's otpauth_uri is
 *    rendered as text beside a code field. A passkey's public-key options
 *    are driven by the WebAuthn ceremony (@speed/auth-core/webauthn) --
 *    the browser's own API, no dependency -- whose credential is reported
 *    under the answer step the record's state names. The same surface
 *    renders a passkey login's first step and a passkey enrollment.
 *  - `complete`   -> nothing: the driver takes a ready step's result.
 *  - `redirect`   -> the host owns navigation; a redirect step that
 *    reaches this renderer (a callback route opened before the provider
 *    answered) renders the unsupported notice with the exit the channel
 *    supplies, so every rendered step carries at least one control.
 *
 * The factor and enroll checkboxes are the step's own: the challenge a
 * chosen factor opens carries no factor id, so the driver remembers the
 * selection and merges it back into the factor's answer (see
 * use-flow-driver). Factor and enroll labels follow the same guarded
 * vocabulary rule as the field names: a known kind renders its
 * translated name, an unknown one shows the kind as given (the
 * deployment's own vocabulary), and a server-supplied label wins over
 * both.
 *
 * HumanGatedFlowStep wraps this renderer for a step that may send a
 * verification message. It renders the deployment's proof block above the
 * step, presents the proof the answer needs, and consumes it once
 * presented. Which answers send is ./flow's answerSends -- choosing an
 * SMS second factor, or supplying a phone a challenge declared -- so a
 * factor the deployment verifies another way answers without a proof and
 * leaves the current one unspent.
 */

import { useEffect, useId, useRef, useState } from 'react'
import Button from '@mui/material/Button'
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
  FormField,
  FormLayout,
  HumanVerification as HumanVerificationBlock,
} from '@speed/ui-kit'
import { useAuthUiTranslation } from './translation.js'
import { fieldLabel } from './labels.js'
import { WebauthnError, runWebauthn, webauthnStepOf } from '@speed/auth-core/webauthn'
import type { OpenFlowState } from './flow.js'
import { answerSends, stepMaySend } from './flow.js'

/** The factor/enroll kinds the family has wording for. */
const FACTOR_KINDS = ['totp', 'sms', 'webauthn'] as const

export interface FlowStepFieldsProps {
  /** The open step to render. */
  readonly step: OpenFlowState
  /** A request is in flight; the step's own action disables. */
  readonly busy: boolean
  /** Report the answered fields upward (the driver sends them). */
  readonly onAnswer: (input: Record<string, string>) => void
  /**
   * The exit attached to the unsupported-step notice. A channel form
   * returns to its own first-step fields; the callback route re-reads the
   * flow. Required because every rendered step carries at least one
   * control -- an unsupported step has no field of its own to offer.
   */
  readonly onUnsupported: () => void
  /** The exit's label; omitted uses the family's own "back to the first
   * step" wording (the callback route passes its retry wording). */
  readonly unsupportedLabel?: string
  /**
   * The passkey ceremony failure the channel's feedback scope holds, as a
   * bundle key. The ceremony is a client-side source that does not enter
   * the slot (see ./write-feedback); the step reports it upward so the
   * channel can keep it mutually exclusive with the slot's failure.
   */
  readonly clientFailure?: string | null
  /** Report a ceremony failure upward, or `null` when a retry begins. */
  readonly onClientFailure?: (key: string | null) => void
}

export interface HumanGatedFlowStepProps {
  /** The open step to render. */
  readonly step: OpenFlowState
  /** A request is in flight; the step's own action disables. */
  readonly busy: boolean
  /** The session whose human-verification gate and proof the step uses. */
  readonly session: AuthSession
  /** The gate a host already read, or omitted to read it here. */
  readonly humanGate?: AuthnHumanGate
  /** Report the answered fields upward (the driver sends them), with the
   * proof an answer that may send presents. */
  readonly onAnswer: (
    input: Record<string, string>,
    humanProof?: string,
  ) => void
  /** See FlowStepFieldsProps.onUnsupported. */
  readonly onUnsupported: () => void
  /** See FlowStepFieldsProps.unsupportedLabel. */
  readonly unsupportedLabel?: string
  /** See FlowStepFieldsProps.clientFailure. */
  readonly clientFailure?: string | null
  /** See FlowStepFieldsProps.onClientFailure. */
  readonly onClientFailure?: (key: string | null) => void
}

/**
 * A step renderer with the deployment's human-verification block above it.
 * The block renders when the step may send a verification message, and an
 * answer that does send presents the proof the surface holds. The proof is
 * single-use, so presenting it consumes it and the block obtains the next
 * one; an answer that sends nothing leaves it unspent.
 *
 * The hook lives here rather than in each channel form, so a form whose
 * own first step sends nothing reads no discovery document until a parked
 * step actually may send.
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
}: HumanGatedFlowStepProps) {
  const human = useHumanVerification(
    session,
    humanGate === undefined ? {} : { gate: humanGate },
  )
  const needsProof = humanGateNeedsProof(human.gate)
  const answer = (input: Record<string, string>): void => {
    const presented =
      needsProof && answerSends(step, input) ? human.proof : null
    onAnswer(input, presented ?? undefined)
    if (presented !== null) {
      human.consume()
    }
  }
  return (
    <>
      {needsProof && stepMaySend(step) ? (
        <HumanVerificationBlock
          gate={human.gate ?? undefined}
          proof={human.proof}
          status={human.status}
          onToken={human.adoptToken}
          onRetry={human.retry}
          disabled={busy}
        />
      ) : null}
      <FlowStepFields
        step={step}
        busy={busy}
        onAnswer={answer}
        onUnsupported={onUnsupported}
        unsupportedLabel={unsupportedLabel}
        clientFailure={clientFailure}
        onClientFailure={onClientFailure}
      />
    </>
  )
}

export function FlowStepFields({
  step,
  busy,
  onAnswer,
  onUnsupported,
  unsupportedLabel,
  clientFailure,
  onClientFailure,
}: FlowStepFieldsProps) {
  const { t } = useAuthUiTranslation()
  const labelId = useId()

  const labelOf = (field: AuthnField): string => fieldLabel(field, t)

  const kindLabel = (kind: string): string =>
    (FACTOR_KINDS as readonly string[]).includes(kind)
      ? t(`signInFlow.factorKind.${kind}`)
      : kind

  switch (step.next.kind) {
    case 'challenge':
      return (
        <ChallengeFields
          fields={step.next.fields}
          fieldLabel={labelOf}
          submit={t('signInFlow.submit')}
          busy={busy}
          onAnswer={onAnswer}
        />
      )
    case 'factor':
      return (
        <Choices
          labelId={labelId}
          title={t('signInFlow.factorTitle')}
          busy={busy}
          choices={step.next.factors.map((factor) => ({
            key: factor.id,
            label: factor.label ?? kindLabel(factor.kind),
          }))}
          onChoose={(id) => onAnswer({ factor: id })}
        />
      )
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
          busy={busy}
          submit={t('signInFlow.submit')}
          onAnswer={onAnswer}
          clientFailure={clientFailure ?? null}
          onClientFailure={onClientFailure}
        />
      )
    case 'complete':
      // The driver takes a ready step's result; nothing renders here.
      return null
    default:
      // redirect: the host owns navigation; a redirect step reaching this
      // renderer (a callback route opened before the provider answered)
      // renders the unsupported notice with the exit the channel supplies.
      // The exit is not disabled by `busy`: every rendered step must offer
      // a control focus can land on, and this is the only one here.
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
          <Button type="button" variant="outlined" onClick={onUnsupported}>
            {unsupportedLabel ?? t('signInFlow.unsupportedStepAction')}
          </Button>
        </Box>
      )
  }
}

/** The TOTP enrollment step: the otpauth_uri to add to an authenticator
 * app, rendered as text, plus a code field for the six-digit answer. */
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
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5, width: '100%' }}>
      <Typography variant="body2" color="text.secondary">
        {t('signInFlow.enrollTotpHelp')}
      </Typography>
      <Typography
        variant="body2"
        component="pre"
        sx={(theme) => ({
          fontFamily: theme.typography.fontFamilyMono,
          wordBreak: 'break-all',
          whiteSpace: 'pre-wrap',
          m: 0,
        })}
      >
        {safeJson(options)}
      </Typography>
      <TextField
        label={t('signInFlow.codeLabel')}
        value={value}
        onChange={(event) => setValue(event.target.value)}
        disabled={busy}
        fullWidth
        autoComplete="off"
      />
      <Button
        type="button"
        variant="contained"
        disabled={busy || value.trim() === ''}
        onClick={() => onAnswer({ code: value.trim() })}
      >
        {submit}
      </Button>
    </Box>
  )
}

/** The passkey ceremony step: one button that drives the browser's WebAuthn
 * ceremony against the step's options and reports the credential it returns.
 *
 * The ceremony can sit pending for as long as the browser waits on the
 * device (a page with no authenticator at all keeps it pending until it is
 * dismissed), so a click moves the step into an explicit waiting state: the
 * waiting message and a cancel control replace the button instead of
 * leaving a disabled button as the only signal. The waiting notice mounts
 * as an empty `role="status"` region and fills a commit later, so a screen
 * reader announces it; focus moves to the cancel control, since the button
 * that had it unmounted. Cancel is its own state
 * transition, taken at once: it shows the failure and retry without waiting
 * for the ceremony to settle, and only best-effort asks the browser to give
 * up the ceremony (the credential API does not guarantee it honours an
 * abort). A late answer from an abandoned ceremony is discarded by attempt.
 * Unmount abandons the ceremony the same way: the attempt is invalidated
 * and the browser is asked to give it up, so a ceremony that settles after
 * the surface is gone answers nothing and completes nothing.
 * A browser without WebAuthn, a cancelled or dismissed ceremony and a
 * failed ceremony each report their own bundle key to the channel's
 * feedback scope, which renders it as the step's failure banner; the retry
 * action runs the ceremony again against the same step and clears it. The
 * scope keeps this client-side message mutually exclusive with the
 * channel's server failure banner (see ./write-feedback). The prompt names
 * the ceremony the step asks for: a login assertion or a passkey to
 * register. */
function PasskeyFields({
  options,
  busy,
  submit,
  onAnswer,
  clientFailure,
  onClientFailure,
}: {
  readonly options: unknown
  readonly busy: boolean
  readonly submit: string
  readonly onAnswer: (input: Record<string, string>) => void
  /** The ceremony failure the channel's feedback scope holds. */
  readonly clientFailure: string | null
  /** Report a ceremony failure to the scope, or `null` as a retry begins. */
  readonly onClientFailure?: (key: string | null) => void
}) {
  const { t } = useAuthUiTranslation()
  const [working, setWorking] = useState(false)
  // The scope owns the failure: it renders the banner and keeps it mutually
  // exclusive with the channel's server failure. This step only reports it.
  const errorKey = clientFailure
  // The waiting notice's text, one commit behind the waiting phase (see
  // the effect below): the region must mount empty.
  const [waitingCommitted, setWaitingCommitted] = useState(false)
  const cancelRef = useRef<HTMLButtonElement>(null)
  // The ceremony currently being waited on. Cancel clears it before the
  // failure renders, so the ceremony's late settlement and a retry's own
  // attempt stay apart from this one.
  const pendingRef = useRef<{
    attempt: number
    controller: AbortController
  } | null>(null)
  const attemptRef = useRef(0)

  // Abandon the ceremony with the surface. An unmounted step has no flow to
  // answer and no host to complete for, so the attempt is invalidated (a
  // late success or failure no longer matches it) and the browser is asked
  // to give the ceremony up. Without this, a ceremony that settles after
  // the surface is gone would still answer the parked step and fire the
  // host's completion for a screen the viewer already left.
  useEffect(
    () => () => {
      const pending = pendingRef.current
      pendingRef.current = null
      pending?.controller.abort()
    },
    [],
  )

  // The waiting notice's role="status" region must never mount together
  // with its text: a live region announces only content changes that
  // follow its own insertion, so a notice born in the same commit as the
  // region would be silent. The text is therefore gated on
  // `waitingCommitted`, which lags the waiting phase by one commit: the
  // region is born empty and the text fills it a commit later. The same
  // effect moves focus to the cancel control, because the Continue button
  // that had it unmounted with the phase change.
  useEffect(() => {
    setWaitingCommitted(working)
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
        onClientFailure?.(webauthnErrorKey(failure))
      }
    } finally {
      if (pendingRef.current?.attempt === attempt) {
        pendingRef.current = null
        setWorking(false)
      }
    }
  }

  function cancel(): void {
    const pending = pendingRef.current
    pendingRef.current = null
    pending?.controller.abort()
    // Fail now. The ceremony's own rejection (if the abort reaches it) and
    // its eventual settlement are both ignored: its attempt no longer
    // matches pendingRef.
    setWorking(false)
    onClientFailure?.('signInFlow.passkeyCancelled')
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5, width: '100%' }}>
      <Typography variant="body2" color="text.secondary">
        {webauthnStepOf(options) === 'register'
          ? t('signInFlow.passkeyRegisterPrompt')
          : t('signInFlow.passkeyPrompt')}
      </Typography>
      {working ? (
        <>
          <Box
            role="status"
            sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}
          >
            <CircularProgress size={18} aria-hidden={true} />
            <Typography>
              {waitingCommitted ? t('signInFlow.passkeyWaiting') : ''}
            </Typography>
          </Box>
          <Button
            ref={cancelRef}
            type="button"
            variant="outlined"
            disabled={busy}
            onClick={cancel}
          >
            {t('signInFlow.passkeyCancel')}
          </Button>
        </>
      ) : (
        <Button
          type="button"
          variant="contained"
          disabled={busy}
          onClick={() => void run()}
        >
          {errorKey === null ? submit : t('signInFlow.passkeyRetry')}
        </Button>
      )}
    </Box>
  )
}

/** The bundle key of the message a failed ceremony renders. */
function webauthnErrorKey(failure: unknown): string {
  if (failure instanceof WebauthnError) {
    switch (failure.reason) {
      case 'unsupported':
        return 'signInFlow.passkeyUnsupported'
      case 'cancelled':
        return 'signInFlow.passkeyCancelled'
      default:
        return 'signInFlow.passkeyFailed'
    }
  }
  return 'signInFlow.passkeyFailed'
}

/** JSON for display; a value JSON cannot carry renders as its string form
 * rather than throwing out of the render. */
function safeJson(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2) ?? String(value)
  } catch {
    return String(value)
  }
}

interface ChallengeFieldsProps {
  readonly fields: readonly AuthnField[]
  readonly fieldLabel: (field: AuthnField) => string
  readonly submit: string
  readonly busy: boolean
  readonly onAnswer: (input: Record<string, string>) => void
}

/** One text input per challenged field, submitted as the answer. */
function ChallengeFields({
  fields,
  fieldLabel,
  submit,
  busy,
  onAnswer,
}: ChallengeFieldsProps) {
  const defaults: Record<string, string> = {}
  for (const field of fields) {
    defaults[field.name] = ''
  }
  const form = useForm<Record<string, string>>({ defaultValues: defaults })
  const onSubmit: SubmitHandler<Record<string, string>> = (values) => {
    onAnswer(values)
  }
  return (
    <FormLayout
      form={form}
      onSubmit={onSubmit}
      actions={
        <Button type="submit" variant="contained" disabled={busy}>
          {submit}
        </Button>
      }
    >
      {fields.map((field) => (
        <FormField
          key={field.name}
          name={field.name}
          required={field.required === true}
          render={({ field: bound, invalid, errorText }) => (
            <TextField
              {...bound}
              label={fieldLabel(field)}
              type={field.kind === 'password' || field.secret === true ? 'password' : 'text'}
              inputMode={field.kind === 'code' ? 'numeric' : undefined}
              autoComplete={field.kind === 'code' ? 'one-time-code' : 'off'}
              fullWidth
              error={invalid}
              helperText={errorText ?? undefined}
            />
          )}
        />
      ))}
    </FormLayout>
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

/** A titled stack of buttons, one per choice. */
function Choices({ labelId, title, busy, choices, onChoose }: ChoicesProps) {
  return (
    <Box
      sx={{ display: 'flex', flexDirection: 'column', gap: 1, width: '100%' }}
    >
      <Alert severity="info" role="status" id={labelId} sx={{ width: '100%' }}>
        {title}
      </Alert>
      {choices.map((choice) => (
        <Button
          key={choice.key}
          variant="outlined"
          fullWidth
          disabled={busy}
          onClick={() => onChoose(choice.key)}
        >
          {choice.label}
        </Button>
      ))}
    </Box>
  )
}
