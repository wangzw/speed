/**
 * FlowStep: renders one step of an authn flow and reports the answer
 * back to the caller's driver.
 *
 * The step-up dialog and the enrollment wizard both advance a flow one
 * answer at a time: each reads the current `AuthnStep` and answers it
 * with `session.answerFlow`. This component is the whole of what the two
 * share -- the rendering of a step's inputs and the shape of the answer
 * it builds -- so neither repeats the `next.kind` dispatch.
 *
 * What a step asks for, and under which answer step its input travels
 * (from the authn login engine's own vocabulary):
 *
 *  - `challenge`: the fields the flow asks for. Their input travels
 *    under the answer step the CALLER names -- `answer` in the flow's
 *    first factor, `factor` in its second -- because the record's state
 *    decides which steps are compatible, not the field list.
 *  - `factor`: the second-factor options to choose from; the chosen
 *    identifier travels under `factor` as the field `factor`.
 *  - `enroll`: the factor kinds the assembly takes in; the chosen kind
 *    travels under `enroll` as the field `kind`.
 *  - `webauthn`: the factor's own JSON. TOTP's `otpauth_uri` is extracted
 *    from it and rendered as its own read-only field -- the step's JSON is
 *    never shown as a blob -- beside a code field; a passkey's creation or
 *    request options are driven by the WebAuthn ceremony
 *    (@speed/auth-core/webauthn), whose credential is reported under the
 *    answer step the CALLER names.
 *    Both answer under the same step a `challenge` step would, because the
 *    record's state decides it: `enroll` in a registration, `factor` in
 *    the second factor of a step-up.
 *  - `complete`: a single confirm that takes the flow's result with
 *    `step: 'complete'`.
 *  - `redirect`: an external URL, reported upward; the component never
 *    navigates.
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
import { WebauthnError, runWebauthn, webauthnStepOf } from '@speed/auth-core/webauthn'
import { useAccountUiTranslation } from './translation.js'

/** The bundle keys of the field names the family has wording for. A name
 * outside this map renders verbatim, so a server-side field the family
 * has no wording for shows its own name rather than a translated guess. */
const FIELD_LABEL_KEYS: Readonly<Record<string, string>> = {
  identifier: 'flowStep.field.identifier',
  password: 'flowStep.field.password',
  new_password: 'flowStep.field.newPassword',
  phone: 'flowStep.field.phone',
  code: 'flowStep.codeLabel',
}

/** The factor/enroll kinds the family has wording for. A known kind
 * renders its translated name; an unknown one renders the kind's own
 * name (the deployment's vocabulary, shown as given). A factor option's
 * server-supplied label wins over both. */
const FACTOR_KIND_KEYS: Readonly<Record<string, string>> = {
  totp: 'mfa.factor.kind.totp',
  webauthn: 'mfa.factor.kind.webauthn',
  sms: 'mfa.factor.kind.sms',
}

/** A field name's display label (see the vocabulary above). */
function fieldLabel(name: string, t: (key: string) => string): string {
  const key = FIELD_LABEL_KEYS[name]
  return key === undefined ? name : t(key)
}

/** A factor kind's display label (see the vocabulary above). */
function factorKindLabel(kind: string, t: (key: string) => string): string {
  const key = FACTOR_KIND_KEYS[kind]
  return key === undefined ? kind : t(key)
}

/** One answer to a flow step: the answer step name and its input. */
export interface FlowStepAnswer {
  readonly step: string
  readonly input?: Readonly<Record<string, string>>
}

export interface FlowStepProps {
  /** The step to render. */
  readonly step: AuthnStep
  /** The flow's step number (`seq`) the step arrived with. It advances with
   * every reply the flow parks; the assertion ceremony's arrival effect keys
   * on it together with the challenge, so a re-sent challenge starts a fresh
   * ceremony while an ordinary re-render does not. */
  readonly sequence: number
  /** True while the answer is in flight; every control is disabled. */
  readonly busy: boolean
  /**
   * The answer step a step travels under: `answer` in the flow's first
   * factor, `factor` in its second, `enroll` in its registration. A
   * `challenge` step's input and a `webauthn` step's credential both use
   * it, because the record's state decides which steps are compatible.
   */
  readonly answerStep: string
  readonly submitLabel: string
  readonly onSubmit: (answer: FlowStepAnswer) => void
  /** Reports a `redirect` step's URL upward; the host navigates. */
  readonly onRedirect?: (url: string) => void
}

export function FlowStep({
  step,
  sequence,
  busy,
  answerStep,
  submitLabel,
  onSubmit,
  onRedirect,
}: FlowStepProps) {
  const { t } = useAccountUiTranslation()
  // The free-text answer of a TOTP code field, and the typed values of a
  // `challenge` step's fields.
  const [values, setValues] = useState<Record<string, string>>({})

  if (step.kind === 'complete') {
    return (
      <Box sx={{ display: 'flex', gap: 1.5, mt: 2 }}>
        <Button
          type="button"
          variant="contained"
          disabled={busy}
          onClick={() => onSubmit({ step: 'complete' })}
        >
          {submitLabel}
        </Button>
      </Box>
    )
  }

  if (step.kind === 'redirect') {
    return (
      <Box sx={{ mt: 2 }}>
        <Alert severity="info">
          {t('flowStep.redirectNotice')}
        </Alert>
        <Box sx={{ mt: 1.5 }}>
          <Button
            type="button"
            variant="contained"
            disabled={busy || onRedirect === undefined}
            onClick={() => onRedirect?.(step.url)}
          >
            {t('flowStep.continueLabel')}
          </Button>
        </Box>
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
            disabled={busy}
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
            disabled={busy}
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
      // app, then takes the six-digit code the app shows. Only the URI is
      // rendered, in a read-only field the reader can select and copy; the
      // step's remaining options are the server's answer to the flow and
      // are not a document to show.
      const uri = otpauthUriOf(options)
      return (
        <Box sx={{ mt: 2 }}>
          <Typography variant="body2" color="text.secondary">
            {t('flowStep.enrollTotpHelp')}
          </Typography>
          {uri !== null && (
            <TextField
              label={t('flowStep.enrollTotpUriLabel')}
              value={uri}
              multiline
              minRows={2}
              fullWidth
              slotProps={{ input: { readOnly: true } }}
              sx={{
                mt: 1,
                mb: 1.5,
                '& textarea': {
                  fontFamily: (theme) => theme.typography.fontFamilyMono,
                  wordBreak: 'break-all',
                },
              }}
            />
          )}
          <TextField
            label={t('flowStep.codeLabel')}
            value={values.code ?? ''}
            onChange={(event) => setValues({ ...values, code: event.target.value })}
            disabled={busy}
            fullWidth
            autoComplete="one-time-code"
          />
          <Box sx={{ display: 'flex', gap: 1.5, mt: 2 }}>
            <Button
              type="button"
              variant="contained"
              disabled={busy || (values.code ?? '').trim() === ''}
              onClick={() =>
                onSubmit({ step: answerStep, input: { code: (values.code ?? '').trim() } })
              }
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
        busy={busy}
        answerStep={answerStep}
        submitLabel={submitLabel}
        onSubmit={onSubmit}
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
            label={fieldLabel(field.name, t)}
            type={field.secret === true ? 'password' : 'text'}
            value={values[field.name] ?? ''}
            onChange={(event) =>
              setValues({ ...values, [field.name]: event.target.value })
            }
            disabled={busy}
            required={field.required === true}
            fullWidth
            autoComplete={field.secret === true ? 'one-time-code' : 'off'}
          />
        ))}
      </Box>
      <Box sx={{ display: 'flex', gap: 1.5, mt: 2 }}>
        <Button type="submit" variant="contained" disabled={busy}>
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
  readonly busy: boolean
  readonly answerStep: string
  readonly submitLabel: string
  readonly onSubmit: (answer: FlowStepAnswer) => void
}

/** The passkey ceremony step: drives the browser's WebAuthn ceremony and
 * reports the credential it returns.
 *
 * The two ceremonies start differently. An assertion step is the login
 * itself: the viewer already chose the passkey entry, so the step starts
 * its ceremony on arrival and never offers a start control. A registration
 * step waits for a click, because a creation ceremony needs the user
 * activation some browsers require.
 *
 * The ceremony can sit pending for as long as the browser waits on the
 * device, so the step shows an explicit waiting state: the waiting message
 * and a cancel control instead of leaving a disabled button as the only
 * signal. The waiting notice mounts as an empty `role="status"` region and
 * fills a commit later, so a
 * screen reader announces it; focus moves to the cancel control, since
 * the control that had it unmounted. Cancel is its own state transition,
 * taken at once: it shows the failure and retry without waiting for the
 * ceremony to settle, and only best-effort asks the browser to give up
 * (the credential API does not guarantee it honours an abort). A late
 * answer from an abandoned ceremony is discarded by attempt. Unmount
 * abandons the ceremony the same way. A browser without WebAuthn, a
 * dismissed prompt and a failed ceremony each render their own message,
 * and the retry action runs the ceremony again against the same step. */
function PasskeyPrompt({
  options,
  sequence,
  busy,
  answerStep,
  submitLabel,
  onSubmit,
}: PasskeyPromptProps) {
  const { t } = useAccountUiTranslation()
  // An assertion step starts on arrival, so its first render is already the
  // waiting state and the start control never flashes.
  const arrivalKey = assertionKey(options, sequence)
  const [working, setWorking] = useState(arrivalKey !== null)
  const [errorKey, setErrorKey] = useState<string | null>(null)
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

  // Abandon the ceremony with the surface. An unmounted step has no flow
  // to answer, so the attempt is invalidated and the browser is asked to
  // give the ceremony up.
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
  // had it (the Continue button, or the entry that opened the step) held.
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
    setErrorKey(null)
    try {
      const credential = await runWebauthn(options, controller.signal)
      if (pendingRef.current?.attempt === attempt) {
        onSubmit({ step: answerStep, input: { credential } })
      }
    } catch (failure) {
      if (pendingRef.current?.attempt === attempt) {
        setErrorKey(webauthnErrorKey(failure))
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
  // click that opened the step is the whole interaction. A registration
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
    setErrorKey('flowStep.passkeyCancelled')
  }

  return (
    <Box sx={{ mt: 2 }}>
      <Typography variant="body2" color="text.secondary">
        {webauthnStepOf(options) === 'register'
          ? t('flowStep.passkeyRegisterPrompt')
          : t('flowStep.passkeyPrompt')}
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
              {waitingCommitted ? t('flowStep.passkeyWaiting') : ''}
            </Typography>
          </Box>
          <Box sx={{ mt: 1.5 }}>
            <Button
              ref={cancelRef}
              type="button"
              variant="outlined"
              disabled={busy}
              onClick={cancel}
            >
              {t('flowStep.passkeyCancel')}
            </Button>
          </Box>
        </>
      ) : (
        <Box sx={{ display: 'flex', gap: 1.5, mt: 2 }}>
          <Button
            type="button"
            variant="contained"
            disabled={busy}
            onClick={() => void run()}
          >
            {errorKey === null ? submitLabel : t('flowStep.passkeyRetry')}
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
        return 'flowStep.passkeyUnsupported'
      case 'cancelled':
        return 'flowStep.passkeyCancelled'
      default:
        return 'flowStep.passkeyFailed'
    }
  }
  return 'flowStep.passkeyFailed'
}

/** The TOTP setup URI a step's options carry, or null when they carry none.
 * Only this one field of the step's JSON is rendered: the options are the
 * flow engine's answer, not a document to show, so a step without a usable
 * URI renders no payload at all. */
function otpauthUriOf(options: unknown): string | null {
  if (typeof options !== 'object' || options === null) {
    return null
  }
  const uri = (options as Record<string, unknown>).otpauth_uri
  return typeof uri === 'string' && uri !== '' ? uri : null
}
