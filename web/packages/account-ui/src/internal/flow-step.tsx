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
 *  - `webauthn`: the factor's own JSON. TOTP's otpauth_uri is rendered as
 *    text beside a code field; a passkey's creation or request options
 *    are driven by the WebAuthn ceremony (./webauthn), whose credential
 *    is reported under the answer step the CALLER names. Both answer
 *    under the same step a `challenge` step would, because the record's
 *    state decides it: `enroll` in a registration, `factor` in the second
 *    factor of a step-up.
 *  - `complete`: a single confirm that takes the flow's result with
 *    `step: 'complete'`.
 *  - `redirect`: an external URL, reported upward; the component never
 *    navigates.
 */

import { useState } from 'react'
import type { FormEvent } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import type { AuthnStep } from '@speed/auth-core'
import { useAccountUiTranslation } from './translation.js'
import { WebauthnError, runWebauthn, webauthnStepOf } from './webauthn.js'

/** One answer to a flow step: the answer step name and its input. */
export interface FlowStepAnswer {
  readonly step: string
  readonly input?: Readonly<Record<string, string>>
}

export interface FlowStepProps {
  /** The step to render. */
  readonly step: AuthnStep
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
            {factor.label ?? factor.kind}
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
            {kind}
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
            {t('flowStep.enrollTotpHelp')}
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
            label={field.name}
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
  readonly busy: boolean
  readonly answerStep: string
  readonly submitLabel: string
  readonly onSubmit: (answer: FlowStepAnswer) => void
}

/** The passkey ceremony step: one button that drives the browser's
 * WebAuthn ceremony and reports the credential it returns. A browser
 * without WebAuthn, a dismissed prompt and a failed ceremony each render
 * their own message rather than a silent no-op. */
function PasskeyPrompt({
  options,
  busy,
  answerStep,
  submitLabel,
  onSubmit,
}: PasskeyPromptProps) {
  const { t } = useAccountUiTranslation()
  const [working, setWorking] = useState(false)
  const [errorKey, setErrorKey] = useState<string | null>(null)

  async function run(): Promise<void> {
    setWorking(true)
    setErrorKey(null)
    try {
      const credential = await runWebauthn(options)
      onSubmit({ step: answerStep, input: { credential } })
    } catch (failure) {
      setErrorKey(webauthnErrorKey(failure))
    } finally {
      setWorking(false)
    }
  }

  return (
    <Box sx={{ mt: 2 }}>
      <Typography variant="body2" color="text.secondary">
        {t('flowStep.passkeyPrompt')}
      </Typography>
      {errorKey !== null && (
        <Box sx={{ mt: 1 }}>
          <Alert severity="error" role="alert">
            {t(errorKey)}
          </Alert>
        </Box>
      )}
      <Box sx={{ display: 'flex', gap: 1.5, mt: 2 }}>
        <Button
          type="button"
          variant="contained"
          disabled={busy || working}
          onClick={() => void run()}
        >
          {submitLabel}
        </Button>
      </Box>
    </Box>
  )
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

/** The step's payload as readable text; a non-serializable value reads
 * as its string form rather than throwing in a render. */
function safeJson(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}
