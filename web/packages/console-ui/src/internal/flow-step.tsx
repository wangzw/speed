/**
 * FlowStep: renders one step of an authn flow and reports the answer back
 * to the caller's driver.
 *
 * The step-up dialog advances a flow one answer at a time: it reads the
 * current `AuthnStep` (@speed/auth-core) and answers it through the
 * session's flow driver. This component is the whole of the rendering of
 * a step's inputs and the shape of the answer it builds, so the dialog
 * repeats no `next.kind` dispatch.
 *
 * What a step asks for, and under which answer step its input travels
 * (the authn login engine's vocabulary):
 *
 *  - `challenge`: the fields the flow asks for. Their input travels under
 *    the answer step the CALLER names -- `answer` in the flow's first
 *    factor, `factor` in its second.
 *  - `factor`: the second-factor options to choose from; the chosen
 *    identifier travels under `factor` as the field `factor`.
 *  - `enroll`: the factor kinds the assembly takes in; the chosen kind
 *    travels under `enroll` as the field `kind`.
 *  - `webauthn`: the factor's own JSON. TOTP's otpauth_uri is rendered as
 *    text beside a code field; a passkey's request options are driven by
 *    the WebAuthn ceremony (@speed/auth-core/webauthn), whose credential is
 *    reported under the answer step the CALLER names -- the record's state
 *    decides it, so the second factor of a step-up answers under `factor`.
 *  - `complete`: a single confirm that takes the flow's result with
 *    `step: 'complete'`.
 *  - `redirect`: an external URL; the component never navigates.
 *
 * Factor and enroll labels follow the family's guarded vocabulary rule: a
 * kind the bundle has wording for renders its translated name, an unknown
 * one renders the kind as given (the deployment's own vocabulary), and a
 * factor option's server-supplied label wins over both.
 */

import { useEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import type { AuthnStep } from '@speed/auth-core'
import type { AuthnFlowAnswer } from '@speed/auth-core'
import { WebauthnError, runWebauthn, webauthnStepOf } from '@speed/auth-core/webauthn'
import { useConsoleUiTranslation } from './translation.js'

/** The factor/enroll kinds the family has wording for. A known kind
 * renders its translated name; an unknown one renders the kind's own
 * name (the deployment's vocabulary, shown as given). A factor option's
 * server-supplied label wins over both. */
const FACTOR_KIND_KEYS: Readonly<Record<string, string>> = {
  totp: 'stepUp.factorKind.totp',
  webauthn: 'stepUp.factorKind.webauthn',
  sms: 'stepUp.factorKind.sms',
}

/** A factor kind's display label (see the vocabulary above). */
function factorKindLabel(kind: string, t: (key: string) => string): string {
  const key = FACTOR_KIND_KEYS[kind]
  return key === undefined ? kind : t(key)
}

export interface FlowStepProps {
  /** The step to render. */
  readonly step: AuthnStep
  /** The flow's step number (`seq`) the step arrived with. It advances with
   * every reply the flow parks; the assertion ceremony's arrival effect keys
   * on it together with the challenge, so a re-sent challenge starts a fresh
   * ceremony while an ordinary re-render does not. */
  readonly sequence: number
  /**
   * The answer step a step travels under: `answer` in the flow's first
   * factor, `factor` in its second, `enroll` in its registration. A
   * `challenge` step's input and a `webauthn` step's credential both use
   * it, because the record's state decides which steps are compatible.
   */
  readonly answerStep: string
  readonly submitLabel: string
  readonly onSubmit: (answer: AuthnFlowAnswer) => void
  /**
   * The passkey ceremony's failure, held by the caller so one failure
   * banner stands at a time in the surrounding region. Omit it and the
   * ceremony keeps its own local banner.
   */
  readonly ceremonyError?: string | null
  /** Reports the ceremony's failure key, or null when a new attempt
   * starts. Omit it with `ceremonyError` to stay uncontrolled. */
  readonly onCeremonyError?: (errorKey: string | null) => void
}

export function FlowStep({
  step,
  sequence,
  answerStep,
  submitLabel,
  onSubmit,
  ceremonyError,
  onCeremonyError,
}: FlowStepProps) {
  const { t } = useConsoleUiTranslation()
  // The free-text answer of a TOTP code field, and the typed values of a
  // `challenge` step's fields.
  const [values, setValues] = useState<Record<string, string>>({})

  if (step.kind === 'complete') {
    return (
      <Box sx={{ display: 'flex', gap: 1.5, mt: 2 }}>
        <Button type="button" variant="contained" onClick={() => onSubmit({ step: 'complete' })}>
          {submitLabel}
        </Button>
      </Box>
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
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1, mt: 2 }}>
        {step.factors.map((factor) => (
          <Button
            key={factor.id}
            type="button"
            variant="outlined"
            onClick={() => onSubmit({ step: 'factor', input: { factor: factor.id } })}
          >
            {factor.label ?? factorKindLabel(factor.kind, t)}
          </Button>
        ))}
      </Box>
    )
  }

  if (step.kind === 'enroll') {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1, mt: 2 }}>
        {step.kinds.map((kind) => (
          <Button
            key={kind}
            type="button"
            variant="outlined"
            onClick={() => onSubmit({ step: 'enroll', input: { kind } })}
          >
            {factorKindLabel(kind, t)}
          </Button>
        ))}
      </Box>
    )
  }

  if (step.kind === 'webauthn') {
    const options = step.options
    if (webauthnStepOf(options) === 'totp') {
      // TOTP's enrollment shows the otpauth_uri to add to an authenticator
      // app, then takes the six-digit code the app shows.
      return (
        <Box sx={{ mt: 2 }}>
          <Typography variant="body2" color="text.secondary">
            {t('stepUp.codeHelp')}
          </Typography>
          <Typography
            variant="body2"
            component="pre"
            sx={{
              fontFamily: 'monospace',
              wordBreak: 'break-all',
              whiteSpace: 'pre-wrap',
              mt: 1,
              mb: 1.5,
            }}
          >
            {safeJson(options)}
          </Typography>
          <TextField
            label={t('stepUp.codeLabel')}
            value={values.code ?? ''}
            onChange={(event) => setValues({ ...values, code: event.target.value })}
            fullWidth
            autoComplete="one-time-code"
          />
          <Box sx={{ display: 'flex', gap: 1.5, mt: 2 }}>
            <Button
              type="button"
              variant="contained"
              disabled={(values.code ?? '').trim() === ''}
              onClick={() => onSubmit({ step: answerStep, input: { code: (values.code ?? '').trim() } })}
            >
              {submitLabel}
            </Button>
          </Box>
        </Box>
      )
    }
    return (
      <PasskeyPrompt
        options={options}
        sequence={sequence}
        answerStep={answerStep}
        submitLabel={submitLabel}
        onSubmit={onSubmit}
        ceremonyError={ceremonyError}
        onCeremonyError={onCeremonyError}
      />
    )
  }

  // challenge: one text field per requested field, submitted together.
  const handleSubmit = (event: FormEvent): void => {
    event.preventDefault()
    onSubmit({ step: answerStep, input: { ...values } })
  }

  return (
    <Box component="form" onSubmit={handleSubmit} sx={{ mt: 2 }}>
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
      <Box sx={{ display: 'flex', gap: 1.5, mt: 2 }}>
        <Button type="submit" variant="contained">
          {submitLabel}
        </Button>
      </Box>
    </Box>
  )
}

interface PasskeyPromptProps {
  readonly options: unknown
  /** The flow's step number (`seq`), which advances with every reply. The
   * arrival effect keys on it together with the challenge, so a re-sent
   * challenge is a new ceremony while an ordinary re-render is not. */
  readonly sequence: number
  readonly answerStep: string
  readonly submitLabel: string
  readonly onSubmit: (answer: AuthnFlowAnswer) => void
  readonly ceremonyError?: string | null
  readonly onCeremonyError?: (errorKey: string | null) => void
}

/** The passkey ceremony step: drives the browser's WebAuthn ceremony and
 * reports the credential it returns.
 *
 * The two ceremonies start differently. An assertion step is the step-up
 * itself: the operator already chose the passkey factor, so the step starts
 * its ceremony on arrival and never offers a start control. A registration
 * step waits for a click, because a creation ceremony needs the user
 * activation some browsers require.
 *
 * The ceremony can sit pending for as long as the browser waits on the
 * device, so the step shows an explicit waiting state: the waiting message
 * and a cancel control instead of leaving a disabled button as the only
 * signal. The waiting notice mounts as an empty `role="status"` region and
 * fills a commit later, so a screen reader announces it; focus moves to the
 * cancel control, since the control that had it unmounted. Cancel fails at
 * once -- it shows the cancelled text and a retry without waiting for the
 * ceremony to settle -- and only best-effort asks the browser to give up
 * (the credential API does not guarantee it honours an abort). A late
 * answer from an abandoned ceremony is discarded by attempt, and unmount
 * abandons the ceremony the same way. A browser without WebAuthn, a
 * dismissed prompt and a failed ceremony each render their own message
 * rather than a silent no-op. The failure is reported to the caller when it
 * supplies `onCeremonyError`, so the caller's region can keep it and the
 * server-side banner mutually exclusive. */
function PasskeyPrompt({
  options,
  sequence,
  answerStep,
  submitLabel,
  onSubmit,
  ceremonyError,
  onCeremonyError,
}: PasskeyPromptProps) {
  const { t } = useConsoleUiTranslation()
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
  // The waiting notice's text, one commit behind the waiting phase: the
  // region must mount empty.
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

  // Abandon the ceremony with the surface: an unmounted step has no flow to
  // answer, so the attempt is invalidated and the browser is asked to give
  // the ceremony up.
  useEffect(
    () => () => {
      const pending = pendingRef.current
      pendingRef.current = null
      pending?.controller.abort()
    },
    [],
  )

  // The waiting notice's role="status" region must never mount together
  // with its text, and the cancel control takes the focus the control that
  // had it held.
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
    reportError(null)
    try {
      const credential = await runWebauthn(options, controller.signal)
      if (pendingRef.current?.attempt === attempt) {
        onSubmit({ step: answerStep, input: { credential } })
      }
    } catch (failure) {
      if (pendingRef.current?.attempt === attempt) {
        reportError(webauthnErrorKey(failure))
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

  // An assertion step is the step-up: its arrival starts the ceremony, so
  // choosing the passkey factor is the whole interaction. A registration
  // step stays parked until the viewer clicks. The effect keys on the
  // challenge and the step's sequence number rather than the options object,
  // so an ordinary re-render does not restart the ceremony, while a new
  // challenge -- or the same one re-sent in the flow's next step -- rebinds
  // it. Under StrictMode's double mount the first ceremony is abandoned by
  // this cleanup and the second starts cleanly; only the live attempt
  // answers.
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
    reportError('stepUp.passkeyCancelled')
  }

  return (
    <Box sx={{ mt: 2 }}>
      <Typography variant="body2" color="text.secondary">
        {webauthnStepOf(options) === 'register'
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
            role="status"
            sx={{ display: 'flex', alignItems: 'center', gap: 1.5, mt: 2 }}
          >
            <CircularProgress size={18} aria-hidden={true} />
            <Typography>
              {waitingCommitted ? t('stepUp.passkeyWaiting') : ''}
            </Typography>
          </Box>
          <Box sx={{ mt: 1.5 }}>
            <Button
              ref={cancelRef}
              type="button"
              variant="outlined"
              onClick={cancel}
            >
              {t('stepUp.passkeyCancel')}
            </Button>
          </Box>
        </>
      ) : (
        <Box sx={{ display: 'flex', gap: 1.5, mt: 2 }}>
          <Button
            type="button"
            variant="contained"
            onClick={() => void run()}
          >
            {errorKey === null ? submitLabel : t('stepUp.passkeyRetry')}
          </Button>
        </Box>
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
        return 'stepUp.passkeyUnsupported'
      case 'cancelled':
        return 'stepUp.passkeyCancelled'
      default:
        return 'stepUp.passkeyFailed'
    }
  }
  return 'stepUp.passkeyFailed'
}

/** The step's payload as readable text; a non-serializable value reads as
 * its string form rather than throwing in a render. */
function safeJson(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}
