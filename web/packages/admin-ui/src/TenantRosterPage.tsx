/**
 * TenantRosterPage: the roster-administration page of the console.
 *
 * The platform side of the tenant story: it lists the whole roster (the
 * tenants the process serves) as a table (display name, name, status and
 * the row's write control) and enables or disables a tenant. Registration
 * is a page of its own (/tenants/new, TenantCreatePage), reached from the
 * page header's "register organization" link; a name is immutable after
 * creation, so the name cell is read-only and the page offers no rename
 * control. The name is what the request surface addresses the tenant by and
 * row's identity here: the row key, the detail link and the row's
 * accessible names all use it, never an internal identifier (the roster view
 * does not carry one). Switching the current session's tenant is the
 * tenant-side action and lives in @speed/tenancy-ui; managing the roster is
 * this page, and the two share neither a control nor a query key.
 *
 * Every read and write is an operation on the console operations bundle
 * -- the host binds the generated /api/v1/system/tenants operations to it -- so
 * the page builds no client and writes no query key. The row's state
 * renders from the roster's own spelling (active / disabled / deleted); a
 * value outside that closed set renders the generic label, never a raw
 * value. A disabled row also shows who disabled it (an operator or the
 * account's own state). A deleted row is a tombstone: its state is terminal,
 * so the row carries no write control at all. A failed list render settles
 * into the retryable error placeholder.
 *
 * Each row's enable/disable control is a scope of its own, with the region
 * and the inline marker inside that row, so one row's refusal never reads
 * as another row's. A refusal resolves through the admin-ui namespace's
 * whitelist (a roster collision reads the conflict text), never a raw key,
 * and brings its region into view.
 *
 * The page opens with ui-kit's PageHeader: the page title as the level-one
 * heading, a breadcrumb trail from the console to this page, and the
 * register link plus a refresh action that re-reads the roster. The refresh
 * action is unavailable while any row's status slot has a write in flight. It
 * bumps a refresh generation the rows watch, so every row clears its own
 * scope and inline marker without being remounted. The async section's
 * placeholders render at the next level down, so the heading order never
 * skips.
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

/**
 * The roster's closed status set, one bundle key each under
 * tenants.status. A value outside it renders the generic other label,
 * never a raw value.
 */
const KNOWN_STATUSES = new Set<string>(['active', 'disabled', 'deleted'])

/** The status label for a roster value, guarding the closed set. */
function statusLabel(
  status: string,
  t: (key: string) => string,
): string {
  return KNOWN_STATUSES.has(status)
    ? t(`tenants.status.${status}`)
    : t('tenants.status.other')
}

/**
 * Who disabled a row, from the roster's own closed source set. Only called
 * for a disabled row: the source is absent on every other state.
 */
function disabledByLabel(
  source: string | undefined,
  t: (key: string) => string,
): string {
  return source === 'account'
    ? t('tenants.disabledBy.account')
    : t('tenants.disabledBy.operator')
}

/**
 * One row's write control: the enable/disable toggle.
 *
 * The row is its own feedback scope, with one slot and one region inside the
 * row, so a refusal in one row never shows in another row's region and a
 * success names this tenant. A failed action marks its own toggle with the
 * same resolved text the region shows.
 *
 * Every control here repeats across the rows, so the toggle carries the row's
 * own name in its accessible name -- a screen-reader user hears "disable
 * tenant acme", never a bare "disable" indistinguishable from the row above.
 *
 * The toggle saves on change, so it stays focusable while its write is in
 * flight: a natively disabled control would drop the focus the activation
 * gave it. It reports `aria-busy` and `aria-disabled` and swallows further
 * activation.
 */
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
  // The row's own scope, with its region inside this row.
  const feedback = useWriteFeedback()
  const regionRef = useFailureScroll(feedback.outcome)
  // Whether the current failure names this row's toggle, so the inline
  // marker sits on the control that was refused.
  const [failed, setFailed] = useState(false)
  const resolveErrorText = useAdminUiErrorText()
  const failureText = failureTextOf(feedback.outcome, resolveErrorText)
  const statusErrorId = `tenant-status-error-${encodeURIComponent(tenant.name)}`

  // The page's refresh action re-reads the roster and bumps the generation:
  // this row then clears its scope and its inline marker, because a result
  // written before the re-read would describe the page as it stood then.
  const generationRef = useRef(refreshGeneration)
  useEffect(() => {
    if (generationRef.current === refreshGeneration) {
      return
    }
    generationRef.current = refreshGeneration
    feedback.reset()
    setFailed(false)
  }, [refreshGeneration, feedback.reset])

  // The page's refresh action is unavailable while any scope in the page is
  // writing, but this row's scope lives here; the row reports its slot up
  // and withdraws it when it unmounts.
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

export interface TenantRosterPageProps {
  /**
   * The roster's matched path, for the rows' detail links; absent leaves
   * the display name as plain text.
   */
  readonly basePath?: string
  /**
   * Host columns inserted after the roster's own display name, name and
   * status and before the row-actions column. A host column that repeats
   * across rows takes its own ui-kit priority so a narrow container sheds
   * it in a chosen order; the page does not choose for it.
   */
  readonly extraColumns?: readonly DataTableColumn<AdminTenant>[]
}

export function TenantRosterPage({
  basePath,
  extraColumns,
}: TenantRosterPageProps) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const tenants = operations.useTenants()
  // The names whose row scope has a write in flight. Each row reports its
  // own slot up (see TenantRowActions), so the refresh action can disable
  // for the union of every row's scope.
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

  // The refresh action resets every row scope (each row watches the bumped
  // generation) and re-reads the roster. The lifecycle actions report their
  // landed writes to a page-level scope -- a delete removes the row that
  // carried the control -- so that scope is reset with the re-read too.
  const lifecycleFeedback = useWriteFeedback()
  // The roster's list container: the focus target when a deletion leaves no
  // adjacent row to take the departed control's place.
  const rosterListRef = useRef<HTMLDivElement | null>(null)
  // The tenant a landed deletion took out, with the index its delete control
  // held, so the adjacent row's delete control can take the focus.
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

  // The columns the table renders: a row's read-only fields and its write
  // controls. Status carries a priority, so a narrow viewport sheds it
  // before the display name, the name and the controls. When the host
  // supplies the roster's own path (basePath), the display name links to
  // that tenant's detail page, addressed by the row's name; without it the
  // cell is plain text.
  const columns: DataTableColumn<AdminTenant>[] = [
    {
      id: 'displayName',
      header: t('tenantProfile.field.displayName'),
      cellWrap: 'nowrap',
      cellFullValue: (tenant) =>
        displayNameOf(tenant.display_name, tenant.name),
      // The display name is the page's primary label; the shared cap bounds
      // the cell content and its copy control together, and the complete
      // value stays reachable through the title and the control.
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
        // The name is what the request surface addresses the tenant by and
        // the row's stable identity: it never hides, and the shared
        // primary-column cap keeps a long name from pushing the row's
        // controls out of a narrow container. The full value rides the
        // cell's title.
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
    // Host columns sit between the roster's own fields and the row's write
    // controls, so the controls stay the last cell of every row.
    ...(extraColumns ?? []),
    {
      id: 'actions',
      align: 'right',
      header: t('tenants.column.actions'),
      // A tombstone is read-only: deletion is terminal, so the status route
      // cannot change it. The cell is empty and the row's write scope is
      // never mounted.
      cell: (tenant) =>
        tenant.status === 'deleted' ? null : (
          <Box sx={{ display: 'flex', flexDirection: 'column', rowGap: 1 }}>
            <TenantRowActions
              tenant={tenant}
              refreshGeneration={refreshGeneration}
              onWritePendingChange={reportRowWritePending}
            />
            {/* The lifecycle group is its own control group with its own
                result area. A landed write reports to the page's scope,
                because a delete takes this row out of the table. */}
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
  // A landed deletion takes the row's lifecycle group out of the document,
  // and with it the delete control that started the write. The same control
  // on the next row takes the focus, else the previous row's, else the list
  // container: it never falls back to the document body.
  useEffect(() => {
    const removed = deletionFocus.current
    if (removed === null) {
      return
    }
    // A failed re-read never confirms the row left. Drop the record so a
    // later read (a refresh, the retry) cannot move focus on its own.
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
  // A load with no data yet -- the first fetch in flight, or one parked
  // by react-query's default networkMode 'online' while offline -- keeps
  // the loading branch: isPending alone tracks "no answer yet".
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
              // A re-read mid-write would race the write: the control is
              // disabled while any row's status slot has a write in flight.
              writePending={pendingRowWrites.size > 0}
              label={t('common.refresh')}
              onClick={pageRefresh.refresh}
            />
          </Box>
        }
        sx={{ mb: 2 }}
      />

      {/* The lifecycle actions' page-level result: their row may leave the
          table after a delete, so the confirmation cannot live in the row. */}
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
