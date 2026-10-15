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
 *  - `webauthn`: the factor's own JSON (TOTP's `otpauth_uri`, a
 *    passkey's creation options), which the package renders as text --
 *    it ships no QR or WebAuthn dependency -- plus one free-text field
 *    for the answer. The field's key is the factor's: an `otpauth_uri`
 *    option means a code (`code`), anything else a credential
 *    (`credential`).
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
   * The answer step a `challenge` step's input travels under: `answer`
   * in the flow's first factor, `factor` in its second. Ignored by every
   * other step kind.
   */
  readonly challengeStep: string
  readonly submitLabel: string
  readonly onSubmit: (answer: FlowStepAnswer) => void
  /** Reports a `redirect` step's URL upward; the host navigates. */
  readonly onRedirect?: (url: string) => void
}

export function FlowStep({
  step,
  busy,
  challengeStep,
  submitLabel,
  onSubmit,
  onRedirect,
}: FlowStepProps) {
  const { t } = useAccountUiTranslation()
  // The free-text answer of a `webauthn` step, and the typed values of a
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
    // The factor's own JSON: TOTP's otpauth_uri, or a passkey's creation
    // options. The key of the answer follows it (see the file header).
    const options = step.options
    const isTotp =
      typeof options === 'object' &&
      options !== null &&
      'otpauth_uri' in options
    const payloadKey = isTotp ? 'code' : 'credential'
    const text = safeJson(options)
    return (
      <Box sx={{ mt: 2 }}>
        <Typography variant="body2" color="text.secondary">
          {isTotp
            ? t('flowStep.enrollTotpHelp')
            : t('flowStep.enrollPasskeyHelp')}
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
          {text}
        </Typography>
        <TextField
          label={isTotp ? t('flowStep.codeLabel') : t('flowStep.credentialLabel')}
          value={values[payloadKey] ?? ''}
          onChange={(event) =>
            setValues({ ...values, [payloadKey]: event.target.value })
          }
          disabled={busy}
          fullWidth
          autoComplete="one-time-code"
        />
        <Box sx={{ display: 'flex', gap: 1.5, mt: 2 }}>
          <Button
            type="button"
            variant="contained"
            disabled={busy || (values[payloadKey] ?? '').trim() === ''}
            onClick={() =>
              onSubmit({
                step: 'enroll',
                input: { [payloadKey]: (values[payloadKey] ?? '').trim() },
              })
            }
          >
            {submitLabel}
          </Button>
        </Box>
      </Box>
    )
  }

  // challenge: one text field per requested field, submitted together.
  const handleSubmit = (event: FormEvent): void => {
    event.preventDefault()
    onSubmit({ step: challengeStep, input: { ...values } })
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

/** The step's payload as readable text; a non-serializable value reads
 * as its string form rather than throwing in a render. */
function safeJson(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}
