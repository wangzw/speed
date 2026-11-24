/**
 * RoleTable: the role catalog. A builtin, host-protected or no-manage role is
 * read-only but keeps view-grants and says why, since disabled controls are unreachable.
 */

import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Typography from '@mui/material/Typography'
import {
  DataTable,
  LONG_TEXT_COLUMN_MAX_WIDTH,
  PRIMARY_COLUMN_MAX_WIDTH,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import type { AdminTenantRole } from '../../organization-operations.js'
import { RowActionMenu } from '../row-action-menu.js'
import { useAdminUiTranslation } from '../translation.js'

export interface RoleTableProps {
  readonly roles: readonly AdminTenantRole[]
  /** The role whose grants and usage the lower sections show. */
  readonly selectedRole: string
  /** The role names the host protects from writes. */
  readonly protectedRoles: readonly string[]
  /** The role a delete failure names, or null. */
  readonly failedRole: string | null
  /** The resolved text of the role scope's failure, or null. */
  readonly failureText: string | null
  /** Whether a role write is in flight. */
  readonly busy: boolean
  /** Opens one role's grants and usage. */
  readonly onSelect: (role: string) => void
  /** Opens the description editor for one role. */
  readonly onEditDescription: (role: AdminTenantRole) => void
  /** Asks to remove one role. */
  readonly onRemove: (role: AdminTenantRole) => void
}

function lockedReason(
  role: AdminTenantRole,
  protectedRoles: readonly string[],
): 'builtin' | 'protected' | 'noManage' | null {
  if (role.builtin) {
    return 'builtin'
  }
  if (protectedRoles.includes(role.role)) {
    return 'protected'
  }
  if (!role.can_manage) {
    return 'noManage'
  }
  return null
}

export function RoleTable({
  roles,
  selectedRole,
  protectedRoles,
  failedRole,
  failureText,
  busy,
  onSelect,
  onEditDescription,
  onRemove,
}: RoleTableProps) {
  const { t } = useAdminUiTranslation()

  const columns: DataTableColumn<AdminTenantRole>[] = [
    {
      id: 'role',
      header: t('permissions.roles.column.role'),
      cellWrap: 'nowrap',
      cellFullValue: (role) => role.role,
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (role) => {
        const locked = lockedReason(role, protectedRoles)
        return (
          <Box>
            <Typography variant="body2">{role.role}</Typography>
            {locked !== null && (
              <Typography
                variant="caption"
                color="text.secondary"
                sx={{ display: 'block' }}
              >
                {locked === 'builtin'
                  ? t('permissions.roles.builtinLocked')
                  : locked === 'protected'
                    ? t('permissions.roles.protectedReadOnly')
                    : t('permissions.roles.noManageReadOnly')}
              </Typography>
            )}
          </Box>
        )
      },
    },
    {
      id: 'description',
      header: t('permissions.roles.column.description'),
      priority: 'medium',
      cellWrap: 'wrap',
      cellFullValue: (role) =>
        role.description === '' ? undefined : role.description,
      contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
      cell: (role) => role.description,
    },
    {
      id: 'permissions',
      header: t('permissions.roles.column.permissions'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (role) => role.permission_count,
    },
    {
      id: 'members',
      header: t('permissions.roles.column.members'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (role) =>
        role.member_count === undefined || role.member_count === null
          ? t('common.emptyValue')
          : role.member_count,
    },
    {
      id: 'builtin',
      header: t('permissions.roles.column.builtin'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (role) =>
        role.builtin
          ? t('permissions.roles.builtinYes')
          : t('permissions.roles.builtinNo'),
    },
    {
      id: 'actions',
      align: 'right',
      header: t('permissions.roles.column.actions'),
      cellWrap: 'nowrap',
      cell: (role) => {
        const locked = lockedReason(role, protectedRoles)
        const failed = failedRole === role.role
        const errorId = `permissions-role-error-${encodeURIComponent(role.role)}`
        const selected = selectedRole === role.role
        return (
          <Box>
            <Box
              sx={{
                display: 'flex',
                flexWrap: 'wrap',
                alignItems: 'center',
                columnGap: 1,
                rowGap: 1,
              }}
            >
              {locked === null ? (
                <RowActionMenu
                  label={t('permissions.roles.actionsAria', {
                    role: role.role,
                  })}
                  disabled={busy}
                  invalid={failed}
                  describedBy={failed ? errorId : undefined}
                  actions={[
                    {
                      key: 'manage',
                      label: t('permissions.roles.manageGrants'),
                      onSelect: () => onSelect(role.role),
                    },
                    {
                      key: 'description',
                      label: t('permissions.roles.editDescription'),
                      onSelect: () => onEditDescription(role),
                    },
                    {
                      key: 'remove',
                      label: t('permissions.roles.remove'),
                      onSelect: () => onRemove(role),
                    },
                  ]}
                />
              ) : (
                <Button
                  type="button"
                  size="small"
                  variant="outlined"
                  aria-label={t('permissions.roles.viewGrantsAria', {
                    role: role.role,
                  })}
                  aria-pressed={selected}
                  onClick={() => onSelect(role.role)}
                >
                  {t('permissions.roles.viewGrants')}
                </Button>
              )}
            </Box>
            {failed && failureText !== null && (
              <Typography
                id={errorId}
                variant="caption"
                color="error"
                sx={{ display: 'block', mt: 0.5 }}
              >
                {failureText}
              </Typography>
            )}
          </Box>
        )
      },
    },
  ]

  return (
    <DataTable
      rows={roles}
      columns={columns}
      rowKey={(role) => role.role}
      pinLastColumn
    />
  )
}
