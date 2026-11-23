/**
 * The shared organization-creation form.
 *
 * Both surfaces render this component: the platform console's registration
 * page (with the contact address and website in addition to the shared
 * fields) and the self-service page (name, display name and description
 * only). Neither surface asks for an internal identifier. It is
 * fully controlled -- the page owns the draft and the violations -- so a
 * refused write can keep every value, and it renders one column below
 * `sm` and two from `sm` up, with the long fields spanning the row.
 *
 * Each violation is tied to its field through MUI's own helper-text
 * wiring, so a field's error text is its accessible description and the
 * control carries `aria-invalid`. When a field has no violation its
 * helper says what the field expects; a field with neither renders no
 * helper.
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

  // The submit control and the form judge activation by the same state:
  // the form's own submit is in flight, or the form is not operable for a
  // reason of its own context. Both layers refuse a repeated submit, and
  // the browser's implicit submission from a field reaches the same guard.
  const guardedSubmit = useActivationGuard(
    (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault()
      onSubmit()
    },
    { busy: pending, unavailable: disabled && !pending },
  )

  /** A field's helper: its violation, else its standing hint, else none. */
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
        // Read-only, not disabled: while the form is not operable the
        // field stays focusable and readable, and a focused field is not
        // dropped to the document body.
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
