/**
 * FlowStepFields: the step renderer of the sign-in family.
 *
 * After a channel form's own first fields were submitted, a login flow
 * can answer with another step instead of a completion -- the second
 * factor, a forced password change, a factor to register. This component
 * renders that step into branch fields, dispatching on the step's
 * `next.kind`, and reports the answer upward; it drives nothing itself
 * and holds no session.
 *
 *  - `challenge`  -> one text input per field the server asks for, plus
 *    the continue action. The label of a field the family knows by name
 *    (identifier, password, new_password, phone, code) comes from the
 *    bundle; an unknown name renders verbatim, so a server-side field
 *    the family has no wording for shows its own name rather than a
 *    translated guess.
 *  - `factor`     -> one button per offered second factor; choosing one
 *    sends its id and opens the factor's own challenge.
 *  - `enroll`     -> one button per registerable factor kind.
 *  - `complete`   -> nothing: the driver takes a ready step's result.
 *  - `webauthn`   -> a ceremony this surface cannot run; it renders an
 *    unavailable notice rather than a dead continue button (passkey
 *    ceremonies are the account surface's own, not the sign-in page's).
 *  - `redirect`   -> the host owns navigation; a redirect step that
 *    reaches this renderer (a callback route opened before the provider
 *    answered) renders the same notice.
 *
 * The factor and enroll checkboxes are the step's own: the challenge a
 * chosen factor opens carries no factor id, so the driver remembers the
 * selection and merges it back into the factor's answer (see
 * use-flow-driver). Factor and enroll labels follow the same guarded
 * vocabulary rule as the field names: a known kind renders its
 * translated name, an unknown one renders the generic "other" label,
 * and a server-supplied label wins over both.
 */

import { useId } from 'react'
import Button from '@mui/material/Button'
import Box from '@mui/material/Box'
import Alert from '@mui/material/Alert'
import TextField from '@mui/material/TextField'
import { useForm } from 'react-hook-form'
import type { SubmitHandler } from 'react-hook-form'
import type { AuthnField } from '@speed/auth-core'
import { FormField, FormLayout } from '@speed/ui-kit'
import { useAuthUiTranslation } from './translation.js'
import type { OpenFlowState } from './flow.js'

/** The bundle keys of the field names the family knows. */
const FIELD_LABEL_KEYS: Readonly<Record<string, string>> = {
  identifier: 'passwordSignIn.identifierLabel',
  password: 'passwordSignIn.passwordLabel',
  new_password: 'passwordSignIn.newPasswordLabel',
  phone: 'smsSignIn.phoneLabel',
  code: 'smsSignIn.codeLabel',
}

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

  const fieldLabel = (field: AuthnField): string => {
    const key = FIELD_LABEL_KEYS[field.name]
    return key === undefined ? field.name : t(key)
  }

  const kindLabel = (kind: string): string =>
    (FACTOR_KINDS as readonly string[]).includes(kind)
      ? t(`signInFlow.factorKind.${kind}`)
      : t('signInFlow.factorKind.other')

  switch (step.next.kind) {
    case 'challenge':
      return (
        <ChallengeFields
          fields={step.next.fields}
          fieldLabel={fieldLabel}
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
    case 'complete':
      // The driver takes a ready step's result; nothing renders here.
      return null
    default:
      // webauthn and redirect: a step this surface cannot finish.
      return (
        <Alert severity="info" role="status" sx={{ width: '100%' }}>
          {t('signInFlow.unsupportedStep')}
        </Alert>
      )
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
