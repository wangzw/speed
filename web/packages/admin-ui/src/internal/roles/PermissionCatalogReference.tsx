/**
 * PermissionCatalogReference: the page's bottom reference area.
 *
 * The section is collapsed by default but its titled section and its
 * content region stay mounted either way, so the page never loses a
 * discoverable section and an end-to-end scan can always find it. Expanded,
 * the catalog is searchable by permission code or readable description and
 * grouped the way the declaring modules group it; each entry leads with the
 * readable name, keeps the code as secondary information, marks a
 * dangerous permission visibly and says when the caller may not grant it.
 */

import { useId, useMemo, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { CopyButton, FORM_MAX_WIDTH, ListSkeleton } from '@speed/ui-kit'
import { SectionCard } from '@speed/ui-kit'
import type { AdminTenantPermission } from '../../organization-operations.js'
import { AdminAsyncSection } from '../async-section.js'
import { PermissionsIcon } from '../icons.js'
import {
  PERMISSION_GROUPS,
  PermissionName,
  permissionGroupKey,
  permissionGroupOf,
} from './PermissionName.js'
import type { PermissionGroup } from './PermissionName.js'
import { useAdminUiTranslation } from '../translation.js'

export interface PermissionCatalogReferenceProps {
  /** The governed permission catalog read. */
  readonly catalog: {
    readonly isPending: boolean
    readonly isFetching: boolean
    readonly isError: boolean
    readonly error: unknown
    readonly data: readonly AdminTenantPermission[] | undefined
    readonly refetch: () => unknown
  }
  /** Whether the reference area is expanded. */
  readonly expanded: boolean
  /** Flips the expanded state. */
  readonly onToggle: () => void
}

interface CatalogGroup {
  readonly group: PermissionGroup
  readonly entries: readonly AdminTenantPermission[]
}

function groupCatalog(
  entries: readonly AdminTenantPermission[],
  query: string,
): readonly CatalogGroup[] {
  const needle = query.trim().toLowerCase()
  const matched =
    needle === ''
      ? entries
      : entries.filter(
          (entry) =>
            entry.permission.toLowerCase().includes(needle) ||
            entry.description.toLowerCase().includes(needle),
        )
  return PERMISSION_GROUPS.flatMap((group) => {
    const rows = matched.filter(
      (entry) => permissionGroupOf(entry.group) === group,
    )
    return rows.length === 0 ? [] : [{ group, entries: rows }]
  })
}

export function PermissionCatalogReference({
  catalog,
  expanded,
  onToggle,
}: PermissionCatalogReferenceProps) {
  const { t } = useAdminUiTranslation()
  const [search, setSearch] = useState('')
  const regionId = useId()
  const groups = useMemo(
    () => groupCatalog(catalog.data ?? [], search),
    [catalog.data, search],
  )
  const noMatches = (catalog.data?.length ?? 0) > 0 && groups.length === 0

  return (
    <SectionCard
      title={t('permissions.catalog.title')}
      icon={<PermissionsIcon size={24} />}
      actions={
        <Button
          type="button"
          variant="outlined"
          size="small"
          aria-expanded={expanded}
          aria-controls={regionId}
          onClick={onToggle}
        >
          {expanded
            ? t('permissions.catalog.collapse')
            : t('permissions.catalog.expand')}
        </Button>
      }
    >
      <Box id={regionId} hidden={!expanded}>
        <TextField
          size="small"
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          label={t('permissions.catalog.search.label')}
          placeholder={t('permissions.catalog.search.placeholder')}
          sx={{ width: '100%', maxWidth: FORM_MAX_WIDTH, mb: 2 }}
        />
        <AdminAsyncSection
          pending={catalog.isPending && catalog.data === undefined}
          payload={catalog.data}
          error={catalog.error}
          refreshing={catalog.isFetching}
          empty={catalog.data !== undefined && catalog.data.length === 0}
          headingLevel="h3"
          loading={
            <ListSkeleton
              label={t('permissions.catalog.loading')}
              rows={[<Skeleton key="a" variant="text" width="45%" />]}
            />
          }
          errorState={{
            title: t('permissions.catalog.error.title'),
            description: t('permissions.catalog.error.description'),
            retryLabel: t('permissions.catalog.retry'),
            onRetry: () => void catalog.refetch(),
          }}
          emptyState={{
            title: t('permissions.catalog.empty.title'),
            description: t('permissions.catalog.empty.description'),
          }}
        >
          {() =>
            noMatches ? (
              <Typography variant="body2" color="text.secondary">
                {t('permissions.catalog.noMatches')}
              </Typography>
            ) : (
              groups.map((group) => (
                <Box key={group.group} sx={{ '&:not(:last-of-type)': { mb: 3 } }}>
                  <Typography component="h3" variant="subtitle1" sx={{ mb: 1 }}>
                    {t(permissionGroupKey(group.group))}
                  </Typography>
                  <Box component="ul" sx={{ listStyle: 'none', m: 0, p: 0 }}>
                    {group.entries.map((entry, index) => (
                      <Box
                        component="li"
                        key={entry.permission}
                        sx={{
                          display: 'flex',
                          alignItems: 'flex-start',
                          gap: 2,
                          py: 1.5,
                          ...(index > 0
                            ? { borderTop: 1, borderColor: 'divider' }
                            : {}),
                        }}
                      >
                        <Box sx={{ flexGrow: 1, minWidth: 0 }}>
                          <PermissionName
                            code={entry.permission}
                            description={entry.description}
                            dangerous={entry.dangerous}
                          />
                          {!entry.grantable && (
                            <Typography
                              variant="caption"
                              color="text.secondary"
                              sx={{ display: 'block' }}
                            >
                              {t('permissions.catalog.notGrantable')}
                            </Typography>
                          )}
                        </Box>
                        <CopyButton value={entry.permission} />
                      </Box>
                    ))}
                  </Box>
                </Box>
              ))
            )
          }
        </AdminAsyncSection>
      </Box>
    </SectionCard>
  )
}
