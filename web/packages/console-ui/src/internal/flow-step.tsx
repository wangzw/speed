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
 *    the WebAuthn ceremony (./webauthn), whose credential is reported
 *    under the answer step the CALLER names -- the record's state decides
 *    it, so the second factor of a step-up answers under `factor`.
 *  - `complete`: a single confirm that takes the flow's result with
 *    `step: 'complete'`.
 *  - `redirect`: an external URL; the component never navigates.
 */

import { useState } from 'react'
import type { FormEvent } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import type { AuthnStep } from '@speed/auth-core'
import type { AuthnFlowAnswer } from '@speed/auth-core'
import { useConsoleUiTranslation } from './translation.js'
import { WebauthnError, runWebauthn, webauthnStepOf } from './webauthn.js'

export interface FlowStepProps {
  /** The step to render. */
  readonly step: AuthnStep
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
  readonly answerStep: string
  readonly submitLabel: string
  readonly onSubmit: (answer: AuthnFlowAnswer) => void
  readonly ceremonyError?: string | null
  readonly onCeremonyError?: (errorKey: string | null) => void
}

/** The passkey ceremony step: one button that drives the browser's
 * WebAuthn ceremony and reports the credential it returns. A browser
 * without WebAuthn, a dismissed prompt and a failed ceremony each render
 * their own message rather than a silent no-op. The failure is reported
 * to the caller when it supplies `onCeremonyError`, so the caller's region
 * can keep it and the server-side banner mutually exclusive. */
function PasskeyPrompt({
  options,
  answerStep,
  submitLabel,
  onSubmit,
  ceremonyError,
  onCeremonyError,
}: PasskeyPromptProps) {
  const { t } = useConsoleUiTranslation()
  const [working, setWorking] = useState(false)
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

  async function run(): Promise<void> {
    setWorking(true)
    reportError(null)
    try {
      const credential = await runWebauthn(options)
      onSubmit({ step: answerStep, input: { credential } })
    } catch (failure) {
      reportError(webauthnErrorKey(failure))
    } finally {
      setWorking(false)
    }
  }

  return (
    <Box sx={{ mt: 2 }}>
      <Typography variant="body2" color="text.secondary">
        {t('stepUp.passkeyPrompt')}
      </Typography>
      {errorKey !== null && (
        <Box sx={{ mt: 1 }}>
          <Alert severity="error" role="alert">
            {t(errorKey)}
          </Alert>
        </Box>
      )}
      <Box sx={{ display: 'flex', gap: 1.5, mt: 2 }}>
        <Button type="button" variant="contained" disabled={working} onClick={() => void run()}>
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
