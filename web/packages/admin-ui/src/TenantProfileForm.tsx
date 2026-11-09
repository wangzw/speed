/**
 * The organization profile's edit form, shared by the organization surface's
 * profile page and the platform console's tenant detail.
 *
 * It is a controlled form: the page owns the draft so a refused save keeps
 * exactly what the operator typed, and a settled re-read replaces it. The
 * form itself owns only the local field refusals, so an out-of-bounds value
 * is named on the field it belongs to and the request is not sent.
 *
 * The four fields may all be cleared. The display name is the one with a
 * fallback: leaving it empty makes every surface show the name, and the
 * field's placeholder is that name so the operator can see what will be
 * shown. Below `sm` the fields stack in one column; from `sm` the short ones
 * pair up and the description spans the width.
 */

import { useState } from 'react'
import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import TextField from '@mui/material/TextField'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  hasTenantProfileFieldErrors,
  validateTenantProfileFields,
  type TenantProfileFieldErrors,
  type TenantProfileFields,
} from './TenantProfileFields.js'

export interface TenantProfileFormProps {
  /** The draft the form renders; the page owns it. */
  readonly value: TenantProfileFields
  /** Reports each keystroke's result; the page keeps the draft. */
  readonly onChange: (fields: TenantProfileFields) => void
  /**
   * Writes the validated fields. Called only when every field passes the
   * local bounds; a server refusal is the page's feedback scope to report.
   */
  readonly onSubmit: (fields: TenantProfileFields) => void
  /** Whether a write is in flight: the save control stays disabled and busy. */
  readonly saving: boolean
  /**
   * The name the organization is addressed by, shown as the display name's
   * placeholder so an empty field says what will be shown instead.
   */
  readonly name?: string
}

/** The label a local field refusal renders. */
function fieldErrorText(
  error: NonNullable<TenantProfileFieldErrors['displayName']>,
  field: keyof TenantProfileFieldErrors,
  t: (key: string) => string,
): string {
  switch (error) {
    case 'tooLong':
      return field === 'displayName'
        ? t('tenantProfile.form.invalid.displayName')
        : t('tenantProfile.form.invalid.description')
    case 'invalidEmail':
      return t('tenantProfile.form.invalid.contactEmail')
    case 'invalidWebsite':
      return t('tenantProfile.form.invalid.website')
  }
}

/** The organization profile's four-field form. */
export function TenantProfileForm({
  value,
  onChange,
  onSubmit,
  saving,
  name,
}: TenantProfileFormProps): ReactElement {
  const { t } = useAdminUiTranslation()
  const [errors, setErrors] = useState<TenantProfileFieldErrors>({})

  const update = (field: keyof TenantProfileFields, next: string): void => {
    onChange({ ...value, [field]: next })
    // A field the operator is editing no longer carries the previous
    // refusal; re-validating on save can bring it back if it still holds.
    if (errors[field] !== undefined) {
      setErrors((previous) => {
        const left = { ...previous }
        delete left[field]
        return left
      })
    }
  }

  const submit = (): void => {
    const nextErrors = validateTenantProfileFields(value)
    setErrors(nextErrors)
    if (hasTenantProfileFieldErrors(nextErrors)) {
      return
    }
    onSubmit(value)
  }

  const helperOf = (field: keyof TenantProfileFieldErrors, hint?: string) => {
    const error = errors[field]
    return error === undefined ? hint : fieldErrorText(error, field, t)
  }

  return (
    <Box
      component="form"
      noValidate
      onSubmit={(event) => {
        event.preventDefault()
        submit()
      }}
      sx={{
        display: 'grid',
        gridTemplateColumns: { xs: '1fr', sm: '1fr 1fr' },
        columnGap: 2,
        rowGap: 2,
        alignItems: 'start',
      }}
    >
      <TextField
        size="small"
        fullWidth
        value={value.displayName}
        onChange={(event) => update('displayName', event.target.value)}
        label={t('tenantProfile.field.displayName')}
        placeholder={name}
        error={errors.displayName !== undefined}
        helperText={helperOf(
          'displayName',
          t('tenantProfile.form.displayNameHint'),
        )}
      />
      <TextField
        size="small"
        fullWidth
        value={value.contactEmail}
        onChange={(event) => update('contactEmail', event.target.value)}
        label={t('tenantProfile.field.contactEmail')}
        type="email"
        autoComplete="email"
        error={errors.contactEmail !== undefined}
        helperText={helperOf('contactEmail')}
      />
      <TextField
        size="small"
        fullWidth
        multiline
        minRows={3}
        value={value.description}
        onChange={(event) => update('description', event.target.value)}
        label={t('tenantProfile.field.description')}
        error={errors.description !== undefined}
        helperText={helperOf('description')}
        sx={{ gridColumn: { sm: '1 / -1' } }}
      />
      <TextField
        size="small"
        fullWidth
        value={value.website}
        onChange={(event) => update('website', event.target.value)}
        label={t('tenantProfile.field.website')}
        type="url"
        inputMode="url"
        error={errors.website !== undefined}
        helperText={helperOf('website')}
        sx={{ gridColumn: { sm: '1 / -1' } }}
      />
      <Box sx={{ gridColumn: '1 / -1', display: 'flex', justifyContent: 'flex-end' }}>
        <Button
          type="submit"
          variant="contained"
          loading={saving}
          disabled={saving}
        >
          {t('tenantProfile.form.save')}
        </Button>
      </Box>
    </Box>
  )
}
