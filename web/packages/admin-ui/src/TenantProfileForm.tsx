/**
 * The organization profile's edit form, shared by both surfaces. The page owns
 * the draft so a refused save keeps the typed values; the form owns only the
 * local field refusals.
 */

import { useState } from 'react'
import type { FormEvent, ReactElement } from 'react'
import Box from '@mui/material/Box'
import TextField from '@mui/material/TextField'
import { ActionButton, FORM_MAX_WIDTH, useActivationGuard } from '@speed/ui-kit'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  hasTenantProfileFieldErrors,
  validateTenantProfileFields,
  type TenantProfileFieldErrors,
  type TenantProfileFields,
} from './TenantProfileFields.js'

/** The props of the organization profile form. */
export interface TenantProfileFormProps {
  /** The draft the form renders; the page owns it. */
  readonly value: TenantProfileFields
  /** Reports each keystroke's result; the page keeps the draft. */
  readonly onChange: (fields: TenantProfileFields) => void
  /** Writes the validated fields; a server refusal is the page's to report. */
  readonly onSubmit: (fields: TenantProfileFields) => void
  /** Whether a write is in flight: the save control stays focusable and busy. */
  readonly saving: boolean
  /** The name shown as the display name's placeholder when that field is empty. */
  readonly name?: string
}

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
    // A field being edited drops its previous refusal; save can bring it back.
    if (errors[field] !== undefined) {
      setErrors((previous) => {
        const left = { ...previous }
        delete left[field]
        return left
      })
    }
  }

  const submit = useActivationGuard(
    (event: FormEvent<HTMLFormElement>): void => {
      event.preventDefault()
      const nextErrors = validateTenantProfileFields(value)
      setErrors(nextErrors)
      if (hasTenantProfileFieldErrors(nextErrors)) {
        return
      }
      onSubmit(value)
    },
    { busy: saving },
  )

  const helperOf = (field: keyof TenantProfileFieldErrors, hint?: string) => {
    const error = errors[field]
    return error === undefined ? hint : fieldErrorText(error, field, t)
  }

  return (
    <Box
      component="form"
      noValidate
      onSubmit={submit}
      sx={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'flex-start',
        gap: 2,
        maxWidth: FORM_MAX_WIDTH,
        '& .MuiOutlinedInput-root': { borderRadius: 1 },
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
      />
      <Box sx={{ display: 'flex', justifyContent: 'flex-start' }}>
        <ActionButton type="submit" emphasis="primary" busy={saving}>
          {t('tenantProfile.form.save')}
        </ActionButton>
      </Box>
    </Box>
  )
}
