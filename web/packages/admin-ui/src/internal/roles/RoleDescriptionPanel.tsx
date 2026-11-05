/**
 * RoleDescriptionPanel: the side panel that edits one role's free-text
 * description.
 *
 * A role is never renamed -- its name is the key bindings hold -- so the
 * panel shows the name read-only and edits the description alone. The
 * field carries the 500-character contract the server enforces and shows
 * the remaining length as a character count; an over-long draft cannot be
 * submitted. The panel stays open while the page's write is in flight and
 * closes only when the page reports the save landed.
 */

import { useEffect, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { SidePanel } from '@speed/ui-kit'
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

  // A new role or a re-opened panel starts from the role's current
  // description, never from the previous role's draft.
  useEffect(() => {
    if (open) {
      setDraft(role?.description ?? '')
    }
  }, [open, role])

  const length = [...draft].length
  const tooLong = length > ROLE_DESCRIPTION_MAX

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
          <Button type="button" variant="outlined" disabled={saving} onClick={onClose}>
            {t('permissions.roles.description.cancel')}
          </Button>
          <Button
            type="button"
            variant="contained"
            disabled={saving || tooLong}
            loading={saving}
            onClick={() => onSave(draft)}
          >
            {t('permissions.roles.description.save')}
          </Button>
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
            tooLong
              ? t('permissions.roles.description.tooLong', {
                  max: ROLE_DESCRIPTION_MAX,
                })
              : t('permissions.roles.description.counter', {
                  count: length,
                  max: ROLE_DESCRIPTION_MAX,
                })
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
