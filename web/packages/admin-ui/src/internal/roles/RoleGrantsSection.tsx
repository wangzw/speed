/**
 * RoleGrantsSection: the selected role's permissions.
 *
 * The grant table presents each permission by the catalog's readable
 * description with the code beneath it, and a visible high-risk badge when
 * the catalog marks it dangerous. The grant picker offers only the
 * permissions the caller may grant, grouped the way the catalog groups
 * them. A grant whose server-reported permission is empty is a pair this
 * process no longer translates: its row keeps the raw pair, and revoke stays
 * disabled with the reason beside it because the route names a permission by
 * its code. A readonly role (builtin, host-protected or `can_manage=false`)
 * lists its grants without the actions column at all.
 */

import { useMemo, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import ListSubheader from '@mui/material/ListSubheader'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { DataTable, ListSkeleton } from '@speed/ui-kit'
import type { DataTableColumn, WriteOutcome } from '@speed/ui-kit'
import type { RefObject } from 'react'
import type {
  AdminTenantGrant,
  AdminTenantPermission,
} from '../../organization-operations.js'
import { AdminAsyncSection } from '../async-section.js'
import { WriteFeedbackRegion } from '../write-feedback.js'
import {
  PERMISSION_GROUPS,
  PermissionName,
  permissionGroupKey,
  permissionGroupOf,
} from './PermissionName.js'
import type { PermissionGroup } from './PermissionName.js'
import { useAdminUiTranslation } from '../translation.js'

export interface RoleGrantsSectionProps {
  /** The selected role's grants read. */
  readonly grants: {
    readonly isPending: boolean
    readonly isFetching: boolean
    readonly isError: boolean
    readonly error: unknown
    readonly data: readonly AdminTenantGrant[] | undefined
    readonly refetch: () => unknown
  }
  /** The governed permission catalog, for readable names and badges. */
  readonly catalog: readonly AdminTenantPermission[]
  /** The selected role, or '' when none is chosen. */
  readonly selectedRole: string
  /** Why the selected role's grants are read-only, or null when writable. */
  readonly lockNote: string | null
  /** Whether a grant or revoke is in flight. */
  readonly busy: boolean
  /** The resolved text of the grants scope's failure, or null. */
  readonly failureText: string | null
  /** The grant row a failure names, or null. */
  readonly failedGrant: string | null
  /** The grants scope's slot. */
  readonly outcome: WriteOutcome | null
  /** The grants region's scroll handle. */
  readonly regionRef: RefObject<HTMLDivElement | null>
  /** Grants one permission; the page runs the write. */
  readonly onGrant: (entry: AdminTenantPermission) => void
  /** Revokes one grant; the page runs the write. */
  readonly onRevoke: (grant: AdminTenantGrant) => void
}

interface GroupedPermissions {
  readonly group: PermissionGroup
  readonly entries: readonly AdminTenantPermission[]
}

function groupGrantable(
  catalog: readonly AdminTenantPermission[],
): readonly GroupedPermissions[] {
  const grantable = catalog.filter((entry) => entry.grantable)
  return PERMISSION_GROUPS.flatMap((group) => {
    const entries = grantable.filter(
      (entry) => permissionGroupOf(entry.group) === group,
    )
    return entries.length === 0 ? [] : [{ group, entries }]
  })
}

export function RoleGrantsSection({
  grants,
  catalog,
  selectedRole,
  lockNote,
  busy,
  failureText,
  failedGrant,
  outcome,
  regionRef,
  onGrant,
  onRevoke,
}: RoleGrantsSectionProps) {
  const { t } = useAdminUiTranslation()
  const [permission, setPermission] = useState('')
  const grouped = useMemo(() => groupGrantable(catalog), [catalog])
  const byCode = useMemo(() => {
    const map = new Map<string, AdminTenantPermission>()
    for (const entry of catalog) {
      map.set(entry.permission, entry)
    }
    return map
  }, [catalog])
  const chosen = byCode.get(permission)

  const actionsColumn: DataTableColumn<AdminTenantGrant> = {
    id: 'actions',
    header: t('permissions.grants.column.actions'),
    cellWrap: 'nowrap',
    cell: (grant) => {
      const key = `${grant.resource}:${grant.action}`
      const failed = failedGrant === key
      const errorId = `permissions-grant-error-${encodeURIComponent(key)}`
      const unmappable = grant.permission === ''
      const label = unmappable
        ? `${grant.resource}/${grant.action}`
        : grant.permission
      return (
        <Box>
          <Button
            type="button"
            size="small"
            variant="outlined"
            color="error"
            aria-label={t('permissions.grants.revokeAria', {
              permission: label,
            })}
            aria-invalid={failed}
            aria-describedby={failed ? errorId : undefined}
            disabled={busy || unmappable}
            loading={busy}
            onClick={() => onRevoke(grant)}
          >
            {t('permissions.grants.revoke')}
          </Button>
          {unmappable && (
            <Typography
              variant="caption"
              color="text.secondary"
              sx={{ display: 'block', mt: 0.5 }}
            >
              {t('permissions.grants.revokeUnavailable')}
            </Typography>
          )}
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
  }

  // A readonly role lists its grants and the reason it is read-only; no
  // grant or revoke control renders, per the design's read-only tier.
  const columns: DataTableColumn<AdminTenantGrant>[] = [
    {
      id: 'permission',
      header: t('permissions.grants.column.permission'),
      cellWrap: 'wrap',
      cell: (grant) => {
        const entry = byCode.get(grant.permission)
        const code =
          grant.permission === ''
            ? `${grant.resource}/${grant.action}`
            : grant.permission
        return (
          <PermissionName
            code={code}
            {...(entry === undefined ? {} : { description: entry.description })}
            dangerous={entry?.dangerous ?? false}
          />
        )
      },
    },
    ...(lockNote === null ? [actionsColumn] : []),
  ]

  return (
    <Box>
      {selectedRole === '' ? (
        <Typography variant="body2" color="text.secondary">
          {t('permissions.grants.pickRole')}
        </Typography>
      ) : (
        <>
          <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
            {t('permissions.grants.forRole', { role: selectedRole })}
          </Typography>
          {lockNote === null ? (
            grouped.length === 0 ? (
              <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
                {t('permissions.grants.noneGrantable')}
              </Typography>
            ) : (
              <Box
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
                  select
                  size="small"
                  value={permission}
                  onChange={(event) => setPermission(event.target.value)}
                  label={t('permissions.grants.permissionLabel')}
                  sx={{ minWidth: 280 }}
                  slotProps={{
                    select: {
                      // The options carry the readable name above the code;
                      // the closed control keeps the single-line code so the
                      // field's height does not jump.
                      renderValue: (value) => value as string,
                    },
                  }}
                >
                  {grouped.flatMap((group) => [
                    <ListSubheader key={`group-${group.group}`}>
                      {t(permissionGroupKey(group.group))}
                    </ListSubheader>,
                    ...group.entries.map((entry) => (
                      <MenuItem key={entry.permission} value={entry.permission}>
                        <PermissionName
                          code={entry.permission}
                          description={entry.description}
                          dangerous={entry.dangerous}
                        />
                      </MenuItem>
                    )),
                  ])}
                </TextField>
                <Button
                  type="button"
                  variant="contained"
                  disabled={busy || chosen === undefined}
                  loading={busy}
                  onClick={() => {
                    if (chosen !== undefined) {
                      onGrant(chosen)
                      setPermission('')
                    }
                  }}
                >
                  {t('permissions.grants.add')}
                </Button>
              </Box>
            )
          ) : (
            <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
              {lockNote}
            </Typography>
          )}
          <AdminAsyncSection
            pending={grants.isPending && grants.data === undefined}
            payload={grants.data}
            error={grants.error}
            refreshing={grants.isFetching}
            empty={grants.data !== undefined && grants.data.length === 0}
            headingLevel="h3"
            loading={
              <ListSkeleton
                label={t('permissions.grants.loading')}
                rows={[<Skeleton key="a" variant="text" width="50%" />]}
              />
            }
            errorState={{
              title: t('permissions.grants.error.title'),
              description: t('permissions.grants.error.description'),
              retryLabel: t('permissions.grants.retry'),
              onRetry: () => void grants.refetch(),
            }}
            emptyState={{
              title: t('permissions.grants.empty.title'),
              description: t('permissions.grants.empty.description'),
            }}
          >
            {(settled) => (
              <DataTable
                rows={settled}
                columns={columns}
                rowKey={(grant) => `${grant.resource}:${grant.action}`}
              />
            )}
          </AdminAsyncSection>
          <Box ref={regionRef}>
            <WriteFeedbackRegion outcome={outcome} />
          </Box>
        </>
      )}
    </Box>
  )
}
