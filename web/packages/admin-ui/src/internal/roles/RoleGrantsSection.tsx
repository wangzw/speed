/**
 * RoleGrantsSection: the selected role's permissions and grant picker; a grant
 * with no reported code keeps its raw pair and disables revoke.
 */

import { useMemo, useState } from 'react'
import Box from '@mui/material/Box'
import ListSubheader from '@mui/material/ListSubheader'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { ActionButton, DataTable, ListSkeleton } from '@speed/ui-kit'
import type {
  ActionButtonUnavailableExplanation,
  DataTableColumn,
  WriteOutcome,
} from '@speed/ui-kit'
import type { Ref, RefObject } from 'react'
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
  /** The grant control the in-flight write belongs to. */
  readonly pendingWrite:
    | { readonly kind: 'grant' }
    | { readonly kind: 'revoke'; readonly key: string }
    | null
  /** The resolved text of the grants scope's failure, or null. */
  readonly failureText: string | null
  /** The grant row a failure names, or null. */
  readonly failedGrant: string | null
  /** The grants scope's slot. */
  readonly outcome: WriteOutcome | null
  /** The grants region's scroll handle. */
  readonly regionRef: RefObject<HTMLDivElement | null>
  /** The ref of one grant's revoke control, for the removal focus record. */
  readonly revokeControlRef: (key: string) => Ref<HTMLButtonElement>
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
  pendingWrite,
  failureText,
  failedGrant,
  outcome,
  regionRef,
  revokeControlRef,
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
  // With no permission picked the grant button is unavailable and says why.
  const pickExplanation: ActionButtonUnavailableExplanation =
    chosen === undefined
      ? { unavailableReason: t('permissions.grants.pickPermission') }
      : {}

  const actionsColumn: DataTableColumn<AdminTenantGrant> = {
    id: 'actions',
    align: 'right',
    header: t('permissions.grants.column.actions'),
    cellWrap: 'nowrap',
    cell: (grant) => {
      const key = `${grant.resource}:${grant.action}`
      const failed = failedGrant === key
      const errorId = `permissions-grant-error-${encodeURIComponent(key)}`
      const unmappableId = `permissions-grant-unavailable-${encodeURIComponent(key)}`
      const unmappable = grant.permission === ''
      const label = unmappable
        ? `${grant.resource}/${grant.action}`
        : grant.permission
      // A pair the catalog cannot map has its reason beside the row.
      const explanation: ActionButtonUnavailableExplanation = unmappable
        ? { unavailableDescribedBy: unmappableId }
        : {}
      const revokeBusy =
        pendingWrite?.kind === 'revoke' && pendingWrite.key === key
      return (
        <Box>
          <ActionButton
            ref={revokeControlRef(key)}
            type="button"
            size="small"
            tone="danger"
            aria-label={t('permissions.grants.revokeAria', {
              permission: label,
            })}
            aria-invalid={failed}
            aria-describedby={failed ? errorId : undefined}
            busy={revokeBusy}
            unavailable={unmappable || (busy && !revokeBusy)}
            {...explanation}
            data-grant-revoke={key}
            onClick={() => onRevoke(grant)}
          >
            {t('permissions.grants.revoke')}
          </ActionButton>
          {unmappable && (
            <Typography
              id={unmappableId}
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

  // A readonly role lists its grants and the reason; no grant or revoke control.
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
                      // The options carry the readable name above the code; the
                      // closed control keeps the single-line code to avoid a height jump.
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
                <ActionButton
                  type="button"
                  emphasis="primary"
                  busy={pendingWrite?.kind === 'grant'}
                  unavailable={
                    chosen === undefined ||
                    (busy && pendingWrite?.kind !== 'grant')
                  }
                  {...pickExplanation}
                  onClick={() => {
                    if (chosen !== undefined) {
                      onGrant(chosen)
                      setPermission('')
                    }
                  }}
                >
                  {t('permissions.grants.add')}
                </ActionButton>
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
