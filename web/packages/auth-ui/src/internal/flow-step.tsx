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
 *    (identifier, password, new_password, phone, code, username) comes
 *    from the bundle (see ./labels), along with any explanatory text it
 *    carries; an unknown name renders verbatim and carries no hint, so a
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
 * The step a first login parks at before it opens an account is the same
 * challenge renderer with its own faces, chosen by what the step asks for.
 * A step that asks for a username alone (SMS and enterprise just-in-time
 * signup) renders a title, a sentence saying what the login name is for,
 * the field and its rule help. A step that also asks for a password
 * (social first signup) is the whole account-opening face and carries no
 * title of its own -- the card's heading already says this is a sign-in --
 * just a centred narrative in two sentences: who the account being opened
 * belongs to (the method and the provider's name for the person, the name
 * emphasised, each part omitted when the step carries none) and what is
 * still missing, then a divider, then the form. The provider's shared
 * address is a read-only block under the username: wrapped text, not a
 * one-line input, so a long address stays whole; the username, the
 * read-only address, the password and its confirmation follow in that
 * order. The identity facts are display-only and never submitted; the
 * confirmation is compared locally and never travels.
 *
 * A refusal of a still-parked signup step is the field's, never the
 * whole-attempt banner: the three name refusals (taken, reserved, a shape
 * the server cannot normalize) render under the username field, and a bad
 * request on the face that asked for a password is the password policy's
 * message under the password field. `aria-invalid`, the message as the
 * field's description, and focus moved to it; the step keeps the typed
 * values and stays parked for another try.
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
import { fieldHelp, fieldLabel, methodLabel } from './labels.js'
import { WebauthnError, runWebauthn, webauthnStepOf } from '@speed/auth-core/webauthn'
import type { OpenFlowState } from './flow.js'
import { answerSends, stepMaySend } from './flow.js'

/** The factor/enroll kinds the family has wording for. */
const FACTOR_KINDS = ['totp', 'sms', 'webauthn'] as const

/** The field a username step asks for. */
/** Where the identity sentence splits around the provider's name. */
const IDENTITY_MARKER = '\u0000'

const USERNAME_FIELD = 'username'

/** The field a social signup step asks for beside the username. */
const PASSWORD_FIELD = 'password'

/** Whether the step asks for a password, which is what makes it the full
 * account-opening face (social first signup) rather than the
 * username-only one (SMS and enterprise just-in-time signup). */
function asksForPassword(step: OpenFlowState): boolean {
  return (
    step.next.kind === 'challenge' &&
    step.next.fields.some((field) => field.name === PASSWORD_FIELD)
  )
}

/** The bundle key the username step renders a refusal under, or null for a
 * code that is not a username refusal. */
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

/**
 * How a still-parked signup step carries a refusal: the field it belongs to
 * and the bundle key of its text. The three username refusals are the
 * username field's; a bad request on the face that asked for a password is
 * the password policy's, on the password field. Every other code, and every
 * other step, is the whole-attempt banner's -- a surface asks this to decide
 * which of the two renders.
 */
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
  /**
   * The code of an answer that failed while this step stayed parked. A
   * signup step renders its refusals under the field they name (the
   * username's three, or a password policy on the face that asked for a
   * password); no other step has a field-level use for it.
   */
  readonly failure?: string | null
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
  /** See FlowStepFieldsProps.failure. */
  readonly failure?: string | null
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
  failure,
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
        failure={failure}
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
      // The face that asks for a password is the whole account-opening
      // surface: the read-only provider address, the username with its rule
      // help, the password and its confirmation, and the submit row.
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
          sequence={step.seq}
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

/** The passkey ceremony step: drives the browser's WebAuthn ceremony against
 * the step's options and reports the credential it returns.
 *
 * The two ceremonies start differently. An assertion step is the login
 * itself: the viewer already chose the passkey entry, so the step starts
 * its ceremony on arrival and never offers a start control -- one click,
 * like a social provider. A registration step waits for a click, because
 * a creation ceremony needs the user activation some browsers require.
 *
 * The ceremony can sit pending for as long as the browser waits on the
 * device (a page with no authenticator at all keeps it pending until it is
 * dismissed), so the step shows an explicit waiting state: the waiting
 * message and a cancel control instead of leaving a disabled button as the
 * only signal. The waiting notice mounts as an empty `role="status"` region
 * and fills a commit later, so a screen reader announces it; focus moves to
 * the cancel control, since the control that had it unmounted. Cancel is
 * its own state
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
  sequence,
  busy,
  submit,
  onAnswer,
  clientFailure,
  onClientFailure,
}: {
  readonly options: unknown
  /** The flow's step number (`seq`), which advances with every reply. The
   * arrival effect keys on it together with the challenge, so a re-sent
   * challenge is a new ceremony while an ordinary re-render is not. */
  readonly sequence: number
  readonly busy: boolean
  readonly submit: string
  readonly onAnswer: (input: Record<string, string>) => void
  /** The ceremony failure the channel's feedback scope holds. */
  readonly clientFailure: string | null
  /** Report a ceremony failure to the scope, or `null` as a retry begins. */
  readonly onClientFailure?: (key: string | null) => void
}) {
  const { t } = useAuthUiTranslation()
  // Assertion steps start on arrival, so the first render is already the
  // waiting state: the start control never flashes before the effect below
  // begins the ceremony.
  const arrivalKey = assertionKey(options, sequence)
  const [working, setWorking] = useState(arrivalKey !== null)
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
  // The latest `run`, read by the arrival effect. The effect keys on the
  // step's challenge and sequence number rather than on this function or the
  // options object, so a re-render leaves a running ceremony alone.
  const runRef = useRef<() => Promise<void>>(async () => {})

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
  // effect moves focus to the cancel control, because the control that had
  // it (the Continue button, or the entry that opened the step) unmounted
  // with the phase change.
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
  // Publish the latest `run` for the arrival effect. This effect is
  // declared before the arrival effect, so the assignment lands before the
  // arrival effect reads it; a re-render refreshes it without restarting
  // the ceremony.
  useEffect(() => {
    runRef.current = run
  })

  // An assertion step is the login: its arrival starts the ceremony, so the
  // click that chose the passkey entry is the whole interaction. A
  // registration step stays parked until the viewer clicks. The effect keys
  // on the challenge and the step's sequence number rather than the options
  // object, so an ordinary re-render does not restart the ceremony, while a
  // new challenge -- or the same one re-sent in the flow's next step -- rebinds
  // it. Under StrictMode's double mount the first ceremony is abandoned by
  // this cleanup and the second starts cleanly; only the live attempt
  // answers (see `run`).
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

/** The identity of an assertion step's ceremony: the flow's step number plus
 * the server's challenge. A registration step returns `null` -- it starts on
 * a click -- and the arrival effect watches this value, so a re-render does
 * not restart the ceremony while a new challenge, or the same challenge in a
 * re-sent step, rebinds it. The sequence alone would also distinguish a
 * re-sent step, but the challenge keeps a reply that reuses a number
 * distinguishable. Options without a challenge still key on the step number,
 * so a mounted step runs once and a re-sent one runs again. */
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
  readonly fieldHelp: (field: AuthnField) => string | null
  readonly submit: string
  readonly busy: boolean
  readonly onAnswer: (input: Record<string, string>) => void
  /** The refusal to render under one of the fields (the username step's
   * three username refusals); the field carries `aria-invalid` and the
   * message as its description, and takes focus when the message changes. */
  readonly fieldError?: { readonly name: string; readonly text: string } | null
}

/** One text input per challenged field, submitted as the answer. */
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
  // A refusal under the field is the attempt's outcome: move focus there so
  // a viewer who submitted from the button lands on the value to change.
  useEffect(() => {
    if (errorText !== null) {
      errorInputRef.current?.focus()
    }
  }, [errorText])
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
                autoComplete={field.kind === 'code' ? 'one-time-code' : 'off'}
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
  /** The sign-in method name that opened this login (the discovery
   * vocabulary, such as github or wechat.mp), resolved through the family's
   * method vocabulary for the identity sentence; absent when the step
   * carries none, and then the sentence is not rendered at all. */
  readonly method?: string
  /** The provider's name for the person, emphasised inside the identity
   * sentence; absent when the provider gave none, and then the sentence
   * falls back to naming the method alone. */
  readonly displayName?: string
  /** The provider-shared address, shown as the read-only block under the
   * username; absent when the provider shared none, and then the block is
   * not rendered. */
  readonly email?: string
  /** The server refusal to render under its field, if any. */
  readonly refusal: { readonly name: string; readonly text: string } | null
  readonly fieldLabel: (field: AuthnField) => string
  readonly fieldHelp: (field: AuthnField) => string | null
  readonly submit: string
  readonly busy: boolean
  readonly onAnswer: (input: Record<string, string>) => void
}

interface SignupAccountValues {
  username: string
  password: string
  confirmPassword: string
}

/**
 * The social first-signup face: a centred narrative in two sentences (whose
 * account this is, then what is still missing), a divider, then the form --
 * the username with its rule help, the provider's shared address as a
 * read-only block, the password and its confirmation, and the submit row.
 *
 * The face carries no title of its own: the host renders it inside the
 * sign-in card, whose heading already says this is a sign-in.
 *
 * The identity sentence is composed from two bundle entries so the
 * provider's name can be an element of its own instead of a substring of
 * interpolated text: a lead naming the method (`signupIdentityLead`, or
 * `signupIdentityLeadWithoutName` when the provider gave no name), the name
 * element, and a tail with what the language puts after the name
 * (`signupIdentityTail` -- the verb in zh-CN, the separating space in
 * en-US, where the name ends the sentence). No method name leaves the
 * whole sentence out; no shared address leaves the block out; every other
 * part keeps its place. The confirmation is compared here and never
 * travels; every other rule -- the name's shape, uniqueness and reserved
 * names, and the password policy -- is the server's, and a refusal it
 * answers lands under the field it names.
 */
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
    defaultValues: { username: '', password: '', confirmPassword: '' },
  })
  const usernameField = fields.find((field) => field.name === USERNAME_FIELD) ?? {
    name: USERNAME_FIELD,
    kind: 'text',
  }
  const passwordField = fields.find((field) => field.name === PASSWORD_FIELD) ?? {
    name: PASSWORD_FIELD,
    kind: 'password',
  }
  const usernameInput = useRef<HTMLInputElement>(null)
  const passwordInput = useRef<HTMLInputElement>(null)
  const confirmInput = useRef<HTMLInputElement>(null)
  const refusedName = refusal?.name ?? null
  const refusedText = refusal?.text ?? null
  // The face has no discovery document, so the method's own name is resolved
  // through the same vocabulary the sign-in surface uses minus the
  // descriptor's label: a mode-specific word where the family has one, then
  // its brand word, then the name as the server wrote it.
  const methodName =
    method === undefined || method === ''
      ? null
      : methodLabel({ name: method }, t)
  const accountName =
    displayName === undefined || displayName === '' ? null : displayName
  const providedEmail =
    email === undefined || email === '' ? null : email
  const emailLabel = fieldLabel({ name: 'email', kind: 'text' })
  // The sentence is one bundle entry per case, with a marker where the name
  // goes: the entry then carries the language's own spacing around the name,
  // and the name itself is an element rather than a substring.
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
  // A refusal under a field is the attempt's outcome: move focus there so a
  // viewer who submitted from the button lands on the value to change.
  useEffect(() => {
    if (refusedName === USERNAME_FIELD) {
      usernameInput.current?.focus()
    } else if (refusedName === PASSWORD_FIELD) {
      passwordInput.current?.focus()
    }
  }, [refusedName, refusedText])
  const onSubmit: SubmitHandler<SignupAccountValues> = (values) => {
    onAnswer({ username: values.username, password: values.password })
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
        onSubmit={onSubmit}
        actions={
          <Button type="submit" variant="contained" disabled={busy}>
            {submit}
          </Button>
        }
      >
        <FormField
          name="username"
          required
          render={({ field, invalid, errorText }) => (
            <TextField
              {...field}
              inputRef={refusedName === USERNAME_FIELD ? usernameInput : undefined}
              label={fieldLabel(usernameField)}
              autoComplete="username"
              fullWidth
              error={invalid || refusedName === USERNAME_FIELD}
              helperText={
                errorText ??
                (refusedName === USERNAME_FIELD ? refusedText : null) ??
                fieldHelp(usernameField) ??
                undefined
              }
            />
          )}
        />
        {providedEmail === null ? null : (
          <SignupProvidedEmail
            label={emailLabel}
            value={providedEmail}
            helper={
              methodName === null
                ? t('signInFlow.signupEmailImmutable')
                : t('signInFlow.signupEmailSource', { provider: methodName })
            }
          />
        )}
        <FormField
          name="password"
          required
          render={({ field, invalid, errorText }) => (
            <TextField
              {...field}
              inputRef={refusedName === PASSWORD_FIELD ? passwordInput : undefined}
              label={fieldLabel(passwordField)}
              type="password"
              autoComplete="new-password"
              fullWidth
              error={invalid || refusedName === PASSWORD_FIELD}
              helperText={
                errorText ??
                (refusedName === PASSWORD_FIELD ? refusedText : null) ??
                fieldHelp(passwordField) ??
                undefined
              }
            />
          )}
        />
        <FormField
          name="confirmPassword"
          required
          rules={{
            validate: (value) =>
              value === form.getValues('password') ||
              t('signInFlow.signupPasswordMismatch'),
          }}
          render={({ field, invalid, errorText }) => (
            <TextField
              {...field}
              inputRef={confirmInput}
              label={t('register.confirmPasswordLabel')}
              type="password"
              autoComplete="new-password"
              fullWidth
              error={invalid}
              helperText={errorText ?? undefined}
            />
          )}
        />
      </FormLayout>
    </Box>
  )
}

interface SignupProvidedEmailProps {
  /** The family's label for the address, used as the block's name. */
  readonly label: string
  /** The address the provider shared. */
  readonly value: string
  /** The line under the block: where the address came from and that it
   * cannot be changed. */
  readonly helper: string
}

/**
 * The provider-shared address as a read-only block, not an input: a
 * one-line input hard-truncates a long address with no ellipsis, so the
 * value renders as wrapped text (`overflowWrap: 'anywhere'`) inside a
 * surface that reads as distinct from the editable fields below it. It
 * carries a read-only textbox's semantics -- focusable by keyboard,
 * selectable and copyable, `role="textbox"` with `aria-readonly` -- and
 * its floating label and source line, so the address is announced and
 * explained like the fields around it.
 */
function SignupProvidedEmail({
  label,
  value,
  helper,
}: SignupProvidedEmailProps) {
  const labelId = useId()
  const helperId = useId()
  return (
    <Box sx={{ position: 'relative' }}>
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
          // The relationship, not the sample's literal colours: the block sits
          // one shade off the card, and its rule is quieter than an editable
          // field's -- lighter than it in the light theme, darker in the dark
          // one. That is what keeps the two apart without the read-only block
          // reading as an input.
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
