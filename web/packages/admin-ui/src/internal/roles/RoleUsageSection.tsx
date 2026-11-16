/**
 * RoleUsageSection: who holds the selected role, read-only.
 *
 * The summary reports the role's active-member count; an assembly with no
 * account-identity reader answers no count at all, and the section says
 * the member state is unavailable rather than presenting the binding
 * count as a stand-in. The holder table pages by the server's account
 * cursor and shows the display name, the recorded account, the membership
 * state and when the binding was written.
 *
 * The optional "open in the members page" entry appears only when the
 * caller may manage memberships; it is an addition to the read-only list,
 * never a replacement for it.
 */

import Box from '@mui/material/Box'
import Chip from '@mui/material/Chip'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import {
  DataTable,
  ListSkeleton,
  PRIMARY_COLUMN_MAX_WIDTH,
  useFormatInstant,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import type {
  AdminTenantRole,
  AdminTenantRoleBinding,
  AdminTenantRoleBindingPage,
} from '../../organization-operations.js'
import { identityLabel } from '../account-identity.js'
import { AdminAsyncSection } from '../async-section.js'
import { CursorPager } from '../cursor-pager.js'
import { AdminLink } from '../navigation.js'
import { useAdminUiTranslation } from '../translation.js'

export interface RoleUsageSectionProps {
  /** The selected role, or undefined when none is chosen. */
  readonly role: AdminTenantRole | undefined
  /** The role's holder read. */
  readonly bindings: {
    readonly isPending: boolean
    readonly isFetching: boolean
    readonly isError: boolean
    readonly error: unknown
    readonly data: AdminTenantRoleBindingPage | undefined
    readonly refetch: () => unknown
  }
  /** The zero-based page the cursor stack is on. */
  readonly page: number
  /** Whether a pager move is unavailable while the page is loading. */
  readonly busy: boolean
  /** The members page's address for this role, when the caller may use it. */
  readonly memberHref: string | undefined
  /** Moves to the previous page. */
  readonly onPrevious: () => void
  /** Moves to the server's next page. */
  readonly onNext: () => void
}

export function RoleUsageSection({
  role,
  bindings,
  page,
  busy,
  memberHref,
  onPrevious,
  onNext,
}: RoleUsageSectionProps) {
  const { t } = useAdminUiTranslation()
  const formatTime = useFormatInstant()
  const rows = bindings.data?.bindings
  const nextCursor = bindings.data?.next_cursor ?? ''

  const columns: DataTableColumn<AdminTenantRoleBinding>[] = [
    {
      id: 'member',
      header: t('permissions.usage.column.name'),
      cellWrap: 'wrap',
      // The member is named by display name and login username, either
      // alone, or the neutral copy when neither resolves. The account id is
      // the row key, never visible text or a copy value.
      cellFullValue: (binding) =>
        identityLabel(binding.display_name, binding.username),
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (binding) =>
        identityLabel(binding.display_name, binding.username) ??
        t('common.accountUnavailable'),
    },
    {
      id: 'status',
      header: t('permissions.usage.column.status'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (binding) => {
        const active = binding.active
        const label =
          active === true
            ? t('permissions.usage.status.active')
            : active === false
              ? t('permissions.usage.status.suspended')
              : t('permissions.usage.status.unavailable')
        return (
          <Chip
            size="small"
            variant="outlined"
            color={active === true ? 'success' : 'default'}
            label={label}
          />
        )
      },
    },
    {
      id: 'grantedAt',
      header: t('permissions.usage.column.grantedAt'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (binding) => formatTime(binding.granted_at),
    },
  ]

  const memberCount = role?.member_count

  return (
    <Box>
      {role === undefined ? (
        <Typography variant="body2" color="text.secondary">
          {t('permissions.usage.pickRole')}
        </Typography>
      ) : (
        <>
          <Box
            sx={{
              display: 'flex',
              flexWrap: 'wrap',
              alignItems: 'center',
              columnGap: 2,
              rowGap: 1,
              mb: 2,
            }}
          >
            <Typography variant="body2">
              {memberCount === undefined || memberCount === null
                ? t('permissions.usage.memberCountUnavailable')
                : t('permissions.usage.memberCount', { count: memberCount })}
            </Typography>
            {memberHref !== undefined && (
              <AdminLink href={memberHref}>
                {t('permissions.usage.viewInMembers')}
              </AdminLink>
            )}
          </Box>
          <AdminAsyncSection
            pending={bindings.isPending && rows === undefined}
            payload={rows}
            error={bindings.error}
            refreshing={bindings.isFetching}
            empty={rows !== undefined && rows.length === 0}
            headingLevel="h3"
            loading={
              <ListSkeleton
                label={t('permissions.usage.loading')}
                rows={[<Skeleton key="a" variant="text" width="52%" />]}
              />
            }
            errorState={{
              title: t('permissions.usage.error.title'),
              description: t('permissions.usage.error.description'),
              retryLabel: t('permissions.usage.retry'),
              onRetry: () => void bindings.refetch(),
            }}
            emptyState={{
              title: t('permissions.usage.empty.title'),
              description: t('permissions.usage.empty.description'),
            }}
          >
            {(settled) => (
              <DataTable
                rows={settled}
                columns={columns}
                rowKey={(binding) => binding.account}
              />
            )}
          </AdminAsyncSection>
          <CursorPager
            page={page}
            hasNext={nextCursor !== ''}
            busy={busy || bindings.isFetching}
            onPrevious={onPrevious}
            onNext={onNext}
          />
        </>
      )}
    </Box>
  )
}
