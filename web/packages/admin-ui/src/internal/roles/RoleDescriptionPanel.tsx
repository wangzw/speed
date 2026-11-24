/**
 * RoleDescriptionPanel: edits one role's description. A role is never renamed,
 * so the name is read-only; the field enforces the server's limit and an
 * over-long draft cannot be submitted.
 */

import { useEffect, useId, useState } from 'react'
import Box from '@mui/material/Box'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { ActionButton, SidePanel } from '@speed/ui-kit'
import type { ActionButtonUnavailableExplanation } from '@speed/ui-kit'
import type { AdminTenantRole } from '../../organization-operations.js'
import { useAdminUiTranslation } from '../translation.js'

/** The server's free-text description limit. */
export const ROLE_DESCRIPTION_MAX = 500

export interface RoleDescriptionPanelProps {
  /** Whether the panel is shown. */
  readonly open: boolean
  /** The role being edited; undefined while the panel is closing. */
  readonly role: AdminTenantRole | undefined
  /** Whether the page's description write is in flight. */
  readonly saving: boolean
  /** The resolved failure of the last save attempt, or null. */
  readonly failureText: string | null
  /** Saves the draft; the page runs the write and closes the panel. */
  readonly onSave: (description: string) => void
  /** Reports a close request; the page decides. */
  readonly onClose: () => void
  /** Reports the exit transition's end; the page drops the role. */
  readonly onExited: () => void
}

export function RoleDescriptionPanel({
  open,
  role,
  saving,
  failureText,
  onSave,
  onClose,
  onExited,
}: RoleDescriptionPanelProps) {
  const { t } = useAdminUiTranslation()
  const [draft, setDraft] = useState(role?.description ?? '')
  const lengthHintId = useId()

  // A new role or a re-opened panel starts from that role's description.
  useEffect(() => {
    if (open) {
      setDraft(role?.description ?? '')
    }
  }, [open, role])

  const length = [...draft].length
  const tooLong = length > ROLE_DESCRIPTION_MAX

  // An over-long draft cannot be saved; the button points at the field's
  // standing length text.
  const saveExplanation: ActionButtonUnavailableExplanation = tooLong
    ? { unavailableDescribedBy: lengthHintId }
    : {}

  return (
    <SidePanel
      open={open}
      title={t('permissions.roles.description.title')}
      description={role?.role}
      busy={saving}
      closeLabel={t('permissions.roles.description.close')}
      onClose={onClose}
      onExited={onExited}
      actions={
        <>
          <ActionButton type="button" unavailable={saving} onClick={onClose}>
            {t('permissions.roles.description.cancel')}
          </ActionButton>
          <ActionButton
            type="button"
            emphasis="primary"
            busy={saving}
            unavailable={tooLong}
            {...saveExplanation}
            onClick={() => onSave(draft)}
          >
            {t('permissions.roles.description.save')}
          </ActionButton>
        </>
      }
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', rowGap: 2 }}>
        <TextField
          size="small"
          value={role?.role ?? ''}
          label={t('permissions.roles.description.roleLabel')}
          slotProps={{ input: { readOnly: true } }}
        />
        <TextField
          multiline
          minRows={6}
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          label={t('permissions.roles.description.descriptionLabel')}
          error={tooLong}
          helperText={
            <span id={lengthHintId}>
              {tooLong
                ? t('permissions.roles.description.tooLong', {
                    max: ROLE_DESCRIPTION_MAX,
                  })
                : t('permissions.roles.description.counter', {
                    count: length,
                    max: ROLE_DESCRIPTION_MAX,
                  })}
            </span>
          }
        />
        {failureText !== null && (
          <Typography variant="body2" color="error">
            {failureText}
          </Typography>
        )}
      </Box>
    </SidePanel>
  )
}
