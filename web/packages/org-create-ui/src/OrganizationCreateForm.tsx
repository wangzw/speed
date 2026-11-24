/**
 * The shared organization-creation form, controlled by its host. Each
 * violation is tied to its field as the field's accessible description.
 */

import type { FormEvent } from 'react'
import Box from '@mui/material/Box'
import TextField from '@mui/material/TextField'
import { useTranslation } from '@speed/i18n'
import {
  ActionButton,
  FORM_MAX_WIDTH,
  useActivationGuard,
} from '@speed/ui-kit'
import { ORG_CREATE_UI_NAMESPACE } from './resources.js'
import type {
  OrganizationCreateField,
  OrganizationCreateMode,
  OrganizationCreateValues,
  OrganizationCreateViolations,
} from './validation.js'

/** The controlled form's props: the surface mode, the host-owned draft and
 * violations, and the submit handler. */
export interface OrganizationCreateFormProps {
  /** Which surface renders the form; decides the visible field set. */
  readonly mode: OrganizationCreateMode
  /** The controlled draft. */
  readonly values: OrganizationCreateValues
  /** Reports one field's new value. */
  readonly onChange: (field: OrganizationCreateField, value: string) => void
  /** The violations to show, keyed by field. */
  readonly violations?: OrganizationCreateViolations
  /**
   * Makes the form not operable (a write or an identity switch is in
   * flight): the fields are read-only and the submit control is
   * unavailable.
   */
  readonly disabled?: boolean
  /** The submit is in flight. */
  readonly pending?: boolean
  /** The submit control's text, translated by the page's namespace. */
  readonly submitLabel: string
  /** The page's submit action. */
  readonly onSubmit: () => void
}

/** Renders the controlled creation form for the platform or the
 * self-service surface. */
export function OrganizationCreateForm({
  mode,
  values,
  onChange,
  violations = {},
  disabled = false,
  pending = false,
  submitLabel,
  onSubmit,
}: OrganizationCreateFormProps) {
  const { t } = useTranslation(ORG_CREATE_UI_NAMESPACE)

  const guardedSubmit = useActivationGuard(
    (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault()
      onSubmit()
    },
    { busy: pending, unavailable: disabled && !pending },
  )

  const helperOf = (field: OrganizationCreateField, hint?: string) => {
    const violation = violations[field]
    return violation === undefined
      ? hint
      : t(`validation.${violation}`)
  }

  const field = (
    name: OrganizationCreateField,
    labelKey: string,
    options: {
      readonly hintKey?: string
      readonly multiline?: boolean
    } = {},
  ) => {
    const violation = violations[name]
    return (
      <TextField
        key={name}
        size="small"
        fullWidth
        multiline={options.multiline}
        minRows={options.multiline === true ? 3 : undefined}
        value={values[name]}
        onChange={(event) => onChange(name, event.target.value)}
        label={t(labelKey)}
        error={violation !== undefined}
        helperText={helperOf(
          name,
          options.hintKey === undefined ? undefined : t(options.hintKey),
        )}
        // readOnly, not disabled: a disabled field is unfocusable and would
        // drop focus to the body.
        slotProps={{ input: { readOnly: disabled } }}
        autoComplete="off"
      />
    )
  }

  const platform = mode === 'platform'

  return (
    <Box
      component="form"
      noValidate
      onSubmit={guardedSubmit}
    >
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'flex-start',
          gap: 2,
          maxWidth: FORM_MAX_WIDTH,
          '& .MuiOutlinedInput-root': { borderRadius: 1 },
        }}
      >
        {field('name', 'fields.name', { hintKey: 'helpers.name' })}
        {field('displayName', 'fields.displayName')}
        {field('description', 'fields.description', {
          hintKey: 'helpers.description',
          multiline: true,
        })}
        {platform && field('contactEmail', 'fields.contactEmail', {
          hintKey: 'helpers.contactEmail',
        })}
        {platform && field('website', 'fields.website', {
          hintKey: 'helpers.website',
        })}
      </Box>
      <Box sx={{ mt: 3 }}>
        <ActionButton
          type="submit"
          emphasis="primary"
          busy={pending}
          unavailable={disabled && !pending}
        >
          {submitLabel}
        </ActionButton>
      </Box>
    </Box>
  )
}
