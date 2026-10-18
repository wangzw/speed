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
 *    are driven by the WebAuthn ceremony (./webauthn) -- the browser's own
 *    API, no dependency -- whose credential is reported under the answer
 *    step the record's state names. The same surface renders a passkey
 *    login's first step and a passkey enrollment.
 *  - `complete`   -> nothing: the driver takes a ready step's result.
 *  - `redirect`   -> the host owns navigation; a redirect step that
 *    reaches this renderer (a callback route opened before the provider
 *    answered) renders the unsupported notice.
 *
 * The factor and enroll checkboxes are the step's own: the challenge a
 * chosen factor opens carries no factor id, so the driver remembers the
 * selection and merges it back into the factor's answer (see
 * use-flow-driver). Factor and enroll labels follow the same guarded
 * vocabulary rule as the field names: a known kind renders its
 * translated name, an unknown one renders the generic "other" label,
 * and a server-supplied label wins over both.
 */

import { useId, useRef, useState } from 'react'
import Button from '@mui/material/Button'
import Box from '@mui/material/Box'
import Alert from '@mui/material/Alert'
import CircularProgress from '@mui/material/CircularProgress'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useForm } from 'react-hook-form'
import type { SubmitHandler } from 'react-hook-form'
import type { AuthnField } from '@speed/auth-core'
import { FormField, FormLayout } from '@speed/ui-kit'
import { useAuthUiTranslation } from './translation.js'
import { fieldLabel } from './labels.js'
import { WebauthnError, runWebauthn, webauthnStepOf } from './webauthn.js'
import type { OpenFlowState } from './flow.js'

/** The factor/enroll kinds the family has wording for. */
const FACTOR_KINDS = ['totp', 'sms', 'webauthn'] as const

export interface FlowStepFieldsProps {
  /** The open step to render. */
  readonly step: OpenFlowState
  /** A request is in flight; the step's own action disables. */
  readonly busy: boolean
  /** Report the answered fields upward (the driver sends them). */
  readonly onAnswer: (input: Record<string, string>) => void
}

export function FlowStepFields({
  step,
  busy,
  onAnswer,
}: FlowStepFieldsProps) {
  const { t } = useAuthUiTranslation()
  const labelId = useId()

  const labelOf = (field: AuthnField): string => fieldLabel(field, t)

  const kindLabel = (kind: string): string =>
    (FACTOR_KINDS as readonly string[]).includes(kind)
      ? t(`signInFlow.factorKind.${kind}`)
      : t('signInFlow.factorKind.other')

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
        />
      )
    case 'complete':
      // The driver takes a ready step's result; nothing renders here.
      return null
    default:
      // redirect: the host owns navigation; a redirect step reaching this
      // renderer (a callback route opened before the provider answered)
      // renders the unsupported notice.
      return (
        <Alert severity="info" role="status" sx={{ width: '100%' }}>
          {t('signInFlow.unsupportedStep')}
        </Alert>
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
        sx={{
          fontFamily: 'monospace',
          wordBreak: 'break-all',
          whiteSpace: 'pre-wrap',
          m: 0,
        }}
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
 * leaving a disabled button as the only signal. The credential API offers
 * no portable way to abort a running ceremony, so cancel stops this step
 * from waiting on it -- the call is left to settle on its own and its late
 * answer, success or failure, is discarded. A browser without WebAuthn, a
 * cancelled or dismissed ceremony and a failed ceremony each render their
 * own message, and the retry action runs the ceremony again against the
 * same step. */
function PasskeyFields({
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
  const [working, setWorking] = useState(false)
  const [errorKey, setErrorKey] = useState<string | null>(null)
  // The ceremony the view is currently waiting on. Cancel flips the token
  // so the promise's late settlement (and a retry's own token) stay apart.
  const pendingRef = useRef<{ cancelled: boolean } | null>(null)

  async function run(): Promise<void> {
    const token = { cancelled: false }
    pendingRef.current = token
    setWorking(true)
    setErrorKey(null)
    try {
      const credential = await runWebauthn(options)
      if (!token.cancelled) {
        onAnswer({ credential })
      }
    } catch (failure) {
      if (!token.cancelled) {
        setErrorKey(webauthnErrorKey(failure))
      }
    } finally {
      if (pendingRef.current === token) {
        pendingRef.current = null
        setWorking(false)
      }
    }
  }

  function cancel(): void {
    const pending = pendingRef.current
    if (pending !== null) {
      pending.cancelled = true
      pendingRef.current = null
    }
    setWorking(false)
    setErrorKey('signInFlow.passkeyCancelled')
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5, width: '100%' }}>
      <Typography variant="body2" color="text.secondary">
        {t('signInFlow.passkeyPrompt')}
      </Typography>
      {errorKey !== null && (
        <Alert severity="error" role="alert">
          {t(errorKey)}
        </Alert>
      )}
      {working ? (
        <>
          <Box
            role="status"
            sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}
          >
            <CircularProgress size={18} aria-hidden={true} />
            <Typography>{t('signInFlow.passkeyWaiting')}</Typography>
          </Box>
          <Button
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
