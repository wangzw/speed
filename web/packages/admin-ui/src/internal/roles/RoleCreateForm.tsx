/**
 * RoleCreateForm: the role catalog's create control.
 *
 * The role name is the key bindings hold, so a name the catalog already
 * carries cannot be created again: the submit control stays disabled and
 * the form says the role exists and points at the description editor. The
 * role is created with its free-text description in the same request.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import type { AdminTenantRole } from '../../organization-operations.js'
import { ROLE_DESCRIPTION_MAX } from './RoleDescriptionPanel.js'
import { useAdminUiTranslation } from '../translation.js'

export interface RoleCreateDraft {
  readonly role: string
  readonly description: string
}

export interface RoleCreateFormProps {
  /** The roles the catalog already carries, for the duplicate check. */
  readonly roles: readonly AdminTenantRole[]
  /** Whether a role write is in flight. */
  readonly busy: boolean
  /** Creates the role; resolves true when the write landed, so the form
   * keeps a refused draft for the operator to correct. */
  readonly onCreate: (draft: RoleCreateDraft) => Promise<boolean>
}

export function RoleCreateForm({
  roles,
  busy,
  onCreate,
}: RoleCreateFormProps) {
  const { t } = useAdminUiTranslation()
  const [role, setRole] = useState('')
  const [description, setDescription] = useState('')
  const trimmed = role.trim()
  const duplicate = roles.some((existing) => existing.role === trimmed)
  const tooLong = [...description].length > ROLE_DESCRIPTION_MAX
  const disabled = busy || trimmed === '' || duplicate || tooLong

  return (
    <Box
      component="form"
      onSubmit={(event) => {
        event.preventDefault()
        if (disabled) {
          return
        }
        void onCreate({ role: trimmed, description }).then((landed) => {
          if (landed) {
            setRole('')
            setDescription('')
          }
        })
      }}
      sx={{
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'flex-end',
        columnGap: 2,
        rowGap: 1,
        mb: 2,
      }}
    >
      <TextField
        size="small"
        value={role}
        onChange={(event) => setRole(event.target.value)}
        label={t('permissions.roles.create.roleLabel')}
        error={duplicate}
      />
      <TextField
        size="small"
        value={description}
        onChange={(event) => setDescription(event.target.value)}
        label={t('permissions.roles.create.descriptionLabel')}
        sx={{ minWidth: 260 }}
      />
      <Button type="submit" variant="contained" disabled={disabled} loading={busy}>
        {t('permissions.roles.create.submit')}
      </Button>
      {duplicate && (
        <Typography variant="body2" color="error" sx={{ flexBasis: '100%' }}>
          {t('permissions.roles.create.duplicate')}
        </Typography>
      )}
    </Box>
  )
}
