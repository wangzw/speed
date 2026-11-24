/**
 * TenantRosterPage lists the platform roster and enables or disables a
 * tenant. A deleted tenant is a tombstone and carries no write control; the
 * tenant name is the row's identity, never an internal identifier.
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Skeleton from '@mui/material/Skeleton'
import Switch from '@mui/material/Switch'
import Typography from '@mui/material/Typography'
import {
  DataTable,
  ListSkeleton,
  PRIMARY_COLUMN_MAX_WIDTH,
  SectionCard,
  primaryColumnMaxWidthSx,
  useActivationGuard,
  useWriteFeedback,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { AdminAsyncSection } from './internal/async-section.js'
import { useAdminUiErrorText } from './internal/error-text.js'
import { AdminLink } from './internal/navigation.js'
import { RefreshButton } from '@speed/ui-kit'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  pageRefreshRead,
  usePageRefresh,
} from '@speed/ui-kit'
import {
  failureTextOf,
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { TenantLifecycleActions } from './internal/tenant-lifecycle.js'
import { removedControlHeldFocus } from './internal/section-heading-focus.js'
import { useAdminOperations } from './operations.js'
import type { AdminTenant } from './operations.js'
import { AdminPageHeader } from './internal/page-header.js'
import { displayNameOf } from './TenantProfileFields.js'

const KNOWN_STATUSES = new Set<string>(['active', 'disabled', 'deleted'])

function statusLabel(
  status: string,
  t: (key: string) => string,
): string {
  return KNOWN_STATUSES.has(status)
    ? t(`tenants.status.${status}`)
    : t('tenants.status.other')
}

function disabledByLabel(
  source: string | undefined,
  t: (key: string) => string,
): string {
  return source === 'account'
    ? t('tenants.disabledBy.account')
    : t('tenants.disabledBy.operator')
}

// The toggle saves on change and stays focusable while its write is in flight:
// a natively disabled control would drop the focus the activation gave it.
function TenantRowActions({
  tenant,
  refreshGeneration,
  onWritePendingChange,
}: {
  readonly tenant: AdminTenant
  /** The page's refresh counter; a change resets this row's scope. */
  readonly refreshGeneration: number
  readonly onWritePendingChange: (name: string, pending: boolean) => void
}) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const setStatus = operations.useSetTenantStatus()
  const feedback = useWriteFeedback()
  const regionRef = useFailureScroll(feedback.outcome)
  const [failed, setFailed] = useState(false)
  const resolveErrorText = useAdminUiErrorText()
  const failureText = failureTextOf(feedback.outcome, resolveErrorText)
  const statusErrorId = `tenant-status-error-${encodeURIComponent(tenant.name)}`

  const generationRef = useRef(refreshGeneration)
  useEffect(() => {
    if (generationRef.current === refreshGeneration) {
      return
    }
    generationRef.current = refreshGeneration
    feedback.reset()
    setFailed(false)
  }, [refreshGeneration, feedback.reset])

  useEffect(() => {
    onWritePendingChange(tenant.name, feedback.pending)
    return () => onWritePendingChange(tenant.name, false)
  }, [feedback.pending, onWritePendingChange, tenant.name])

  const disabled = tenant.status === 'disabled'

  const toggleStatus = useActivationGuard(
    () => {
      setFailed(false)
      void feedback.run(async () => {
        try {
          await setStatus.mutateAsync({
            name: tenant.name,
            status: disabled ? 'active' : 'disabled',
          })
          return t(
            disabled ? 'tenants.enabledToast' : 'tenants.disabledToast',
            { name: tenant.name },
          )
        } catch (error) {
          setFailed(true)
          throw error
        }
      })
    },
    { busy: feedback.pending },
  )

  return (
    <Box
      sx={{
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'flex-start',
        columnGap: 2,
        rowGap: 1,
      }}
    >
      <Switch
        checked={!disabled}
        slotProps={{
          input: {
            'aria-label': disabled
              ? t('tenants.enableAria', { name: tenant.name })
              : t('tenants.disableAria', { name: tenant.name }),
            'aria-invalid': failed,
            'aria-describedby': failed ? statusErrorId : undefined,
            'aria-busy': feedback.pending ? 'true' : undefined,
            'aria-disabled': feedback.pending ? 'true' : undefined,
          },
        }}
        onChange={toggleStatus}
      />
      {failed && failureText !== null && (
        <Typography
          id={statusErrorId}
          variant="caption"
          color="error"
          sx={{ flexBasis: '100%' }}
        >
          {failureText}
        </Typography>
      )}
      <Box ref={regionRef} sx={{ flexBasis: '100%' }}>
        <WriteFeedbackRegion outcome={feedback.outcome} />
      </Box>
    </Box>
  )
}

/** Props of TenantRosterPage: the roster path and optional host columns. */
export interface TenantRosterPageProps {
  /**
   * The roster's matched path, for the rows' detail links; absent leaves
   * the display name as plain text.
   */
  readonly basePath?: string
  /**
   * Host columns inserted after the roster's own fields and before the
   * row-actions column. Each host column takes its own ui-kit priority; the
   * page does not choose for it.
   */
  readonly extraColumns?: readonly DataTableColumn<AdminTenant>[]
}

/**
 * TenantRosterPage lists the platform roster and enables or disables a tenant.
 */
export function TenantRosterPage({
  basePath,
  extraColumns,
}: TenantRosterPageProps) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const tenants = operations.useTenants()
  const [pendingRowWrites, setPendingRowWrites] = useState<ReadonlySet<string>>(
    () => new Set<string>(),
  )
  const reportRowWritePending = useCallback(
    (name: string, pending: boolean) => {
      setPendingRowWrites((current) => {
        if (current.has(name) === pending) {
          return current
        }
        const next = new Set(current)
        if (pending) {
          next.add(name)
        } else {
          next.delete(name)
        }
        return next
      })
    },
    [],
  )

  const lifecycleFeedback = useWriteFeedback()
  const rosterListRef = useRef<HTMLDivElement | null>(null)
  const deletionFocus = useRef<{
    readonly name: string
    readonly index: number
  } | null>(null)
  const recordDeletion = useCallback((name: string) => {
    const list = rosterListRef.current
    if (list === null) {
      return
    }
    const controls = Array.from(
      list.querySelectorAll<HTMLElement>('[data-tenant-delete]'),
    )
    const index = controls.findIndex(
      (control) => control.dataset.tenantDelete === name,
    )
    if (index >= 0) {
      deletionFocus.current = { name, index }
    }
  }, [])
  const [refreshGeneration, setRefreshGeneration] = useState(0)
  const pageRefresh = usePageRefresh(
    [pageRefreshRead('tenants', tenants)],
    () => {
      lifecycleFeedback.reset()
      setRefreshGeneration((generation) => generation + 1)
    },
  )

  const columns: DataTableColumn<AdminTenant>[] = [
    {
      id: 'displayName',
      header: t('tenantProfile.field.displayName'),
      cellWrap: 'nowrap',
      cellFullValue: (tenant) =>
        displayNameOf(tenant.display_name, tenant.name),
      // The shared cap bounds the content and its copy control together.
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (tenant) => {
        const name = displayNameOf(tenant.display_name, tenant.name)
        return basePath === undefined ? (
          <Typography variant="body2">{name}</Typography>
        ) : (
          <AdminLink href={`${basePath}/${encodeURIComponent(tenant.name)}`}>
            {name}
          </AdminLink>
        )
      },
    },
    {
      id: 'name',
      header: t('tenants.column.name'),
      cellWrap: 'nowrap',
      cell: (tenant) => (
        <Typography
          variant="body2"
          noWrap
          sx={{ ...primaryColumnMaxWidthSx, fontWeight: 500 }}
          title={tenant.name}
        >
          {tenant.name}
        </Typography>
      ),
    },
    {
      id: 'status',
      header: t('tenants.column.status'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (tenant) => (
        <Box>
          <Typography variant="body2" color="text.secondary">
            {statusLabel(tenant.status, t)}
          </Typography>
          {tenant.status === 'disabled' && (
            <Typography
              variant="caption"
              color="text.secondary"
              sx={{ display: 'block' }}
            >
              {disabledByLabel(tenant.disabled_by, t)}
            </Typography>
          )}
        </Box>
      ),
    },
    ...(extraColumns ?? []),
    {
      id: 'actions',
      align: 'right',
      header: t('tenants.column.actions'),
      // A tombstone cannot change state, so the row's write scope is not mounted.
      cell: (tenant) =>
        tenant.status === 'deleted' ? null : (
          <Box sx={{ display: 'flex', flexDirection: 'column', rowGap: 1 }}>
            <TenantRowActions
              tenant={tenant}
              refreshGeneration={refreshGeneration}
              onWritePendingChange={reportRowWritePending}
            />
            <TenantLifecycleActions
              tenant={tenant}
              onSuccess={lifecycleFeedback.succeed}
              onDeleted={recordDeletion}
            />
          </Box>
        ),
    },
  ]

  const rows = tenants.data
  // A landed deletion removes the delete control that started the write: the
  // next row's control takes focus, else the previous row's, else the list.
  useEffect(() => {
    const removed = deletionFocus.current
    if (removed === null) {
      return
    }
    // A failed re-read never confirms the row left.
    if (tenants.error !== null) {
      deletionFocus.current = null
      return
    }
    if (rows === undefined) {
      return
    }
    const list = rosterListRef.current
    if (list === null) {
      return
    }
    const controls = Array.from(
      list.querySelectorAll<HTMLElement>('[data-tenant-delete]'),
    )
    if (controls.some((control) => control.dataset.tenantDelete === removed.name)) {
      return
    }
    deletionFocus.current = null
    if (!removedControlHeldFocus()) {
      return
    }
    const target = controls[removed.index] ?? controls[removed.index - 1]
    if (target !== undefined) {
      target.focus()
      return
    }
    list.focus()
  }, [rows, tenants.error])
  // isPending covers the first fetch and one parked offline; both keep loading.
  const pending = tenants.isPending && rows === undefined

  return (
    <Box>
      <AdminPageHeader
        title={t('tenants.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('tenants.title') },
        ]}
        actions={
          <Box
            sx={{
              display: 'flex',
              flexWrap: 'wrap',
              alignItems: 'center',
              columnGap: 2,
              rowGap: 1,
            }}
          >
            {basePath === undefined ? null : (
              <AdminLink href={`${basePath}/new`}>
                {t('tenants.register')}
              </AdminLink>
            )}
            <RefreshButton
              busy={pageRefresh.busy}
              writePending={pendingRowWrites.size > 0}
              label={t('common.refresh')}
              onClick={pageRefresh.refresh}
            />
          </Box>
        }
        sx={{ mb: 2 }}
      />

      {/* A delete removes the row, so the lifecycle result cannot live in it. */}
      <WriteFeedbackRegion outcome={lifecycleFeedback.outcome} />

      <SectionCard flushContent>
        <Box
          ref={rosterListRef}
          role="group"
          aria-label={t('tenants.title')}
          tabIndex={-1}
        >
          <AdminAsyncSection
            pending={pending}
            payload={rows}
            error={tenants.error}
            refreshing={tenants.isFetching}
            empty={rows !== undefined && rows.length === 0}
            headingLevel="h2"
            loading={
              <ListSkeleton
                label={t('tenants.loading')}
                rows={[
                  <Skeleton key="a" variant="text" width="60%" />,
                  <Skeleton key="b" variant="text" width="42%" />,
                ]}
              />
            }
            errorState={{
              title: t('tenants.error.title'),
              description: t('tenants.error.description'),
              retryLabel: t('tenants.retry'),
              onRetry: () => void tenants.refetch(),
            }}
            emptyState={{
              title: t('tenants.empty.title'),
              description: t('tenants.empty.description'),
            }}
          >
            {(settled) => (
              <DataTable
                rows={settled}
                columns={columns}
                rowKey={(tenant) => tenant.name}
              />
            )}
          </AdminAsyncSection>
        </Box>
      </SectionCard>
    </Box>
  )
}
