/**
 * The shared organization-creation form.
 *
 * Both surfaces render this component: the platform console's registration
 * page (with the identifier, contact address and website) and the
 * self-service page (alias, display name and description only). It is
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

import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import TextField from '@mui/material/TextField'
import { useTranslation } from '@speed/i18n'
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
  /** Disables every control (a write or an identity switch is in flight). */
  readonly disabled?: boolean
  /** Renders the submit control busy. */
  readonly pending?: boolean
  /** The submit control's text, translated by the page's namespace. */
  readonly submitLabel: string
  /** The page's submit action. */
  readonly onSubmit: () => void
}

/** The grid cell a field occupies: full width below `sm`, one track above. */
const FULL_WIDTH_SX = { gridColumn: { xs: 'auto', sm: '1 / -1' } } as const

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
      readonly fullWidth?: boolean
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
        disabled={disabled}
        autoComplete="off"
        sx={options.fullWidth === true ? FULL_WIDTH_SX : undefined}
      />
    )
  }

  const platform = mode === 'platform'

  return (
    <Box
      component="form"
      noValidate
      onSubmit={(event) => {
        event.preventDefault()
        onSubmit()
      }}
    >
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: { xs: '1fr', sm: 'repeat(2, minmax(0, 1fr))' },
          columnGap: 2,
          rowGap: 2,
        }}
      >
        {platform && field('id', 'fields.id', { hintKey: 'helpers.id' })}
        {field('alias', 'fields.alias', { hintKey: 'helpers.alias' })}
        {field('displayName', 'fields.displayName', { fullWidth: true })}
        {field('description', 'fields.description', {
          hintKey: 'helpers.description',
          fullWidth: true,
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
        <Button
          type="submit"
          variant="contained"
          disabled={disabled || pending}
          loading={pending}
        >
          {submitLabel}
        </Button>
      </Box>
    </Box>
  )
}
