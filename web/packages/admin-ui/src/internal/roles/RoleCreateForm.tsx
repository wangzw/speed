/**
 * RoleCreateForm: the role catalog's create control.
 *
 * The role name is the key bindings hold, so a name the catalog already
 * carries cannot be created again: the submit control stays focusable but
 * unavailable and the form says the role exists and points at the
 * description editor. The role is created with its free-text description in
 * the same request.
 */

import { useId, useState } from 'react'
import type { FormEvent } from 'react'
import Box from '@mui/material/Box'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { ActionButton, useActivationGuard } from '@speed/ui-kit'
import type { ActionButtonUnavailableExplanation } from '@speed/ui-kit'
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
  /** Whether this form's create write is in flight. */
  readonly busy: boolean
  /** Whether another write in the role scope is in flight. */
  readonly scopePending?: boolean
  /** Creates the role; resolves true when the write landed, so the form
   * keeps a refused draft for the operator to correct. */
  readonly onCreate: (draft: RoleCreateDraft) => Promise<boolean>
}

export function RoleCreateForm({
  roles,
  busy,
  scopePending = false,
  onCreate,
}: RoleCreateFormProps) {
  const { t } = useAdminUiTranslation()
  const [role, setRole] = useState('')
  const [description, setDescription] = useState('')
  const duplicateHintId = useId()
  const trimmed = role.trim()
  const roleMissing = trimmed === ''
  const duplicate = roles.some((existing) => existing.role === trimmed)
  const tooLong = [...description].length > ROLE_DESCRIPTION_MAX
  const precondition = roleMissing || duplicate || tooLong
  // A write elsewhere in the role scope keeps this control out of reach
  // without naming a reason of its own; an unmet precondition does name one.
  const unavailable = precondition || (scopePending && !busy)

  const submit = useActivationGuard(
    (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault()
      void onCreate({ role: trimmed, description }).then((landed) => {
        if (landed) {
          setRole('')
          setDescription('')
        }
      })
    },
    { busy, unavailable },
  )

  // The duplicate hint is already on the page and the button points at it.
  // An empty or over-long draft has no standing text, so the button carries
  // its own reason instead.
  const explanation: ActionButtonUnavailableExplanation = duplicate
    ? { unavailableDescribedBy: duplicateHintId }
    : tooLong
      ? {
          unavailableReason: t('permissions.roles.create.tooLong', {
            max: ROLE_DESCRIPTION_MAX,
          }),
        }
      : roleMissing
        ? {
            unavailableReason: t('permissions.roles.create.roleRequired'),
          }
        : {}

  return (
    <Box
      component="form"
      onSubmit={submit}
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
        error={tooLong}
        sx={{ minWidth: 260 }}
      />
      <ActionButton
        type="submit"
        emphasis="primary"
        busy={busy}
        unavailable={unavailable}
        {...explanation}
      >
        {t('permissions.roles.create.submit')}
      </ActionButton>
      {duplicate && (
        <Typography
          id={duplicateHintId}
          variant="body2"
          color="error"
          sx={{ flexBasis: '100%' }}
        >
          {t('permissions.roles.create.duplicate')}
        </Typography>
      )}
    </Box>
  )
}
