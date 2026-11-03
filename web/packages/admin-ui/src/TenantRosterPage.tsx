/**
 * TenantRosterPage: the roster-administration page of the console.
 *
 * The platform side of the tenant story: it lists the whole roster (the
 * tenants the process serves) as a table (identifier, alias, status and
 * the row's write controls), registers a tenant not registered yet,
 * renames a tenant's alias and enables or disables a tenant. Switching
 * the current session's tenant is the tenant-side action and lives in
 * @speed/tenancy-ui; managing the roster is this page, and the two share
 * neither a control nor a query key.
 *
 * Every read and write is an operation on the console operations bundle
 * -- the host binds the generated /api/v1/system/tenants operations to it -- so
 * the page builds no client and writes no query key. The row's state
 * renders from the roster's own spelling (active / disabled / deleted); a
 * value outside that closed set renders the generic label, never a raw
 * value. A disabled row also shows who disabled it (an operator or the
 * account's own state). A deleted row is a tombstone: its state is terminal,
 * so the row carries no write controls at all. A failed list render settles
 * into the retryable error placeholder.
 *
 * The identifier and alias inputs carry a helper that names the rule the
 * backend holds them to (no blanks or control characters) and mark
 * themselves as invalid while a typed value breaks it; a rejected value
 * also holds its submit disabled, so an inadmissible name never leaves
 * the browser and the generic bad-request text is never what the operator
 * meets. The server still judges every write. A write that lands clears
 * the input that carried it -- the create form's identifier and alias
 * together, a row's rename draft on its own -- so the next edit starts
 * empty, and it names the tenant it changed in its scope's success
 * message; a refused write keeps the input so it can be corrected.
 *
 * The create form is one feedback scope. Each row's rename and
 * enable/disable controls are a scope of their own, with the region and
 * the inline marker inside that row, so one row's refusal never reads as
 * another row's. A refusal resolves through the admin-ui namespace's
 * whitelist (a roster collision reads the conflict text), never a raw key,
 * and brings its region into view.
 *
 * The page opens with ui-kit's PageHeader: the page title as the level-one
 * heading, a breadcrumb trail from the console to this page, and a
 * refresh action that re-reads the roster. The refresh action is disabled
 * while a write in any of the page's scopes -- the create form or any
 * row's rename/status slot -- is in flight. It first resets the create
 * form's scope and bumps a refresh generation the rows watch, so every row
 * clears its own scope and inline marker without being remounted and
 * without losing an unsubmitted rename draft. The async section's
 * placeholders render at the next level down, so the heading order never
 * skips.
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  DataTable,
  ListSkeleton,
  PageHeader,
  PRIMARY_COLUMN_MAX_WIDTH,
  SectionCard,
  primaryColumnMaxWidthSx,
  useWriteFeedback,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { AdminAsyncSection } from './internal/async-section.js'
import { useAdminUiErrorText } from './internal/error-text.js'
import { AdminLink } from './internal/navigation.js'
import { isRejectedIdentifier } from './internal/identifier.js'
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
import { useAdminOperations } from './operations.js'
import type { AdminTenant } from './operations.js'

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
 * One row's write controls: the rename draft and the enable/disable
 * toggle. The draft lives here, per row; a landed rename clears it, while
 * a typed value the identifier domain would refuse marks the input and
 * holds the rename submit disabled.
 *
 * The row is its own feedback scope: its rename and enable/disable share
 * one slot and one region inside the row, so a refusal in one row never
 * shows in another row's region and a success names this tenant. A failed
 * action marks its own control -- the draft field for a rename, the toggle
 * for a status change -- with the same resolved text the region shows.
 *
 * Every control here repeats across the rows, so each carries the row's
 * own tenant id in its accessible name (the input, the rename submit, the
 * enable/disable toggle) -- a screen-reader user hears "rename tenant
 * acme", never a bare "rename" indistinguishable from the row above.
 */
function TenantRowActions({
  tenant,
  refreshGeneration,
  onWritePendingChange,
}: {
  readonly tenant: AdminTenant
  /** The page's refresh counter; a change resets this row's scope. */
  readonly refreshGeneration: number
  readonly onWritePendingChange: (id: string, pending: boolean) => void
}) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const renameTenant = operations.useSetTenantAlias()
  const setStatus = operations.useSetTenantStatus()
  // The row's own scope, with its region inside this row.
  const feedback = useWriteFeedback()
  const regionRef = useFailureScroll(feedback.outcome)
  const [alias, setAlias] = useState('')
  // Which of the row's two writes the current failure names, so the
  // inline marker sits on the control that was refused.
  const [failedAction, setFailedAction] = useState<'rename' | 'status' | null>(
    null,
  )
  const resolveErrorText = useAdminUiErrorText()
  const failureText = failureTextOf(feedback.outcome, resolveErrorText)
  const statusErrorId = `tenant-status-error-${encodeURIComponent(tenant.id)}`

  // The page's refresh action re-reads the roster and bumps the generation:
  // this row then clears its scope and its inline marker, because a result
  // written before the re-read would describe the page as it stood then.
  // The row is not remounted -- a rename draft the operator has not
  // submitted stays in the field -- so the change is observed here.
  const generationRef = useRef(refreshGeneration)
  useEffect(() => {
    if (generationRef.current === refreshGeneration) {
      return
    }
    generationRef.current = refreshGeneration
    feedback.reset()
    setFailedAction(null)
  }, [refreshGeneration, feedback.reset])

  // The page's refresh action disables while any scope in the page is
  // writing, but this row's scope lives here; the row reports its slot up
  // and withdraws it when it unmounts.
  useEffect(() => {
    onWritePendingChange(tenant.id, feedback.pending)
    return () => onWritePendingChange(tenant.id, false)
  }, [feedback.pending, onWritePendingChange, tenant.id])

  const disabled = tenant.status === 'disabled'
  // An empty draft is not-yet-an-error; a non-empty one the domain would
  // refuse is.
  const aliasRejected = alias !== '' && isRejectedIdentifier(alias)

  const rename = () => {
    setFailedAction(null)
    void feedback.run(async () => {
      try {
        await renameTenant.mutateAsync({ id: tenant.id, alias })
        // A landed rename clears the draft so the next edit starts empty;
        // a refused one keeps it for a retry.
        setAlias('')
        return t('tenants.renamed', { id: tenant.id })
      } catch (error) {
        setFailedAction('rename')
        throw error
      }
    })
  }

  const toggleStatus = () => {
    setFailedAction(null)
    void feedback.run(async () => {
      try {
        await setStatus.mutateAsync({
          id: tenant.id,
          status: disabled ? 'active' : 'disabled',
        })
        return t(
          disabled ? 'tenants.enabledToast' : 'tenants.disabledToast',
          { id: tenant.id },
        )
      } catch (error) {
        setFailedAction('status')
        throw error
      }
    })
  }

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
      <TextField
        size="small"
        value={alias}
        onChange={(event) => setAlias(event.target.value)}
        label={t('tenants.rename.label')}
        error={aliasRejected || failedAction === 'rename'}
        helperText={
          failedAction === 'rename'
            ? (failureText ?? undefined)
            : t('tenants.identifierHelper')
        }
        slotProps={{
          htmlInput: {
            'aria-label': t('tenants.rename.ariaWithId', { id: tenant.id }),
          },
        }}
      />
      <Button
        type="button"
        size="small"
        variant="outlined"
        aria-label={t('tenants.rename.submitAria', { id: tenant.id })}
        disabled={feedback.pending || aliasRejected}
        loading={feedback.pending}
        onClick={rename}
      >
        {t('tenants.rename.submit')}
      </Button>
      <Button
        type="button"
        size="small"
        variant="outlined"
        aria-label={
          disabled
            ? t('tenants.enableAria', { id: tenant.id })
            : t('tenants.disableAria', { id: tenant.id })
        }
        aria-invalid={failedAction === 'status'}
        aria-describedby={
          failedAction === 'status' ? statusErrorId : undefined
        }
        disabled={feedback.pending}
        loading={feedback.pending}
        onClick={toggleStatus}
      >
        {disabled ? t('tenants.enable') : t('tenants.disable')}
      </Button>
      {failedAction === 'status' && failureText !== null && (
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
   * the identifier as plain text.
   */
  readonly basePath?: string
  /**
   * Host columns inserted after the roster's own identifier, alias and
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
  const createTenant = operations.useCreateTenant()
  const [newId, setNewId] = useState('')
  const [newAlias, setNewAlias] = useState('')
  // The create form is one scope; its region sits directly under it.
  const createFeedback = useWriteFeedback()
  const createRegionRef = useFailureScroll(createFeedback.outcome)
  // The ids whose row scope has a write in flight. Each row reports its
  // own slot up (see TenantRowActions), so the refresh action can disable
  // for the union of the page's scopes and every row's.
  const [pendingRowWrites, setPendingRowWrites] = useState<ReadonlySet<string>>(
    () => new Set<string>(),
  )
  const reportRowWritePending = useCallback((id: string, pending: boolean) => {
    setPendingRowWrites((current) => {
      if (current.has(id) === pending) {
        return current
      }
      const next = new Set(current)
      if (pending) {
        next.add(id)
      } else {
        next.delete(id)
      }
      return next
    })
  }, [])

  // The refresh action resets the create form's scope and bumps the refresh
  // generation the rows watch; both happen before the roster is re-read.
  // Every row scope is reset by its own row, not remounted, and an
  // unsubmitted rename draft survives.
  const [refreshGeneration, setRefreshGeneration] = useState(0)
  const pageRefresh = usePageRefresh(
    [pageRefreshRead('tenants', tenants)],
    () => {
      createFeedback.reset()
      setRefreshGeneration((generation) => generation + 1)
    },
  )

  const idRejected = newId !== '' && isRejectedIdentifier(newId)
  const aliasRejected = newAlias !== '' && isRejectedIdentifier(newAlias)

  const create = () => {
    const id = newId
    const alias = newAlias
    void createFeedback.run(async () => {
      await createTenant.mutateAsync({ id, alias })
      // A landed registration clears both inputs so the form is ready for
      // the next one; a refused one keeps them.
      setNewId('')
      setNewAlias('')
      return t('tenants.created', { id })
    })
  }

  // The columns the table renders: a row's read-only fields and its write
  // controls. Status carries a priority, so a narrow viewport sheds it
  // before the identifier, the alias and the controls. When the host
  // supplies the roster's own path (basePath), the identifier links to
  // that tenant's detail page; without it the cell is plain text.
  const columns: DataTableColumn<AdminTenant>[] = [
    {
      id: 'id',
      header: t('tenants.column.id'),
      cellWrap: 'nowrap',
      cellFullValue: (tenant) => tenant.id,
      // The identifier is host-supplied and has no length bound; the
      // shared cap bounds the cell content and its copy control together,
      // and the complete value stays reachable through the title and the
      // control.
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (tenant) =>
        basePath === undefined ? (
          <Typography variant="body2">{tenant.id}</Typography>
        ) : (
          <AdminLink href={`${basePath}/${encodeURIComponent(tenant.id)}`}>
            {tenant.id}
          </AdminLink>
        ),
    },
    {
      id: 'alias',
      header: t('tenants.column.alias'),
      cellWrap: 'nowrap',
      cell: (tenant) => (
        // The page's primary identity: it never hides, and the shared
        // primary-column cap keeps a long alias from pushing the row's
        // controls out of a narrow container. The full value rides the
        // cell's title.
        <Typography
          variant="body2"
          noWrap
          sx={{ ...primaryColumnMaxWidthSx, fontWeight: 500 }}
          title={tenant.alias}
        >
          {tenant.alias}
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
              {disabledByLabel(tenant.disabledBy, t)}
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
      header: t('tenants.column.actions'),
      // A tombstone is read-only: deletion is terminal, so neither the
      // rename nor the status route can change it. The cell is empty and
      // the row's write scope is never mounted.
      cell: (tenant) =>
        tenant.status === 'deleted' ? null : (
          <TenantRowActions
            tenant={tenant}
            refreshGeneration={refreshGeneration}
            onWritePendingChange={reportRowWritePending}
          />
        ),
    },
  ]

  const rows = tenants.data
  // A load with no data yet -- the first fetch in flight, or one parked
  // by react-query's default networkMode 'online' while offline -- keeps
  // the loading branch: isPending alone tracks "no answer yet".
  const pending = tenants.isPending && rows === undefined

  return (
    <Box>
      <PageHeader
        title={t('tenants.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('tenants.title') },
        ]}
        actions={
          <RefreshButton
            busy={pageRefresh.busy}
            // A re-read mid-write would race the write: the control is
            // disabled while any scope on the page -- the create form's or
            // any row's rename/status slot -- has a write in flight.
            writePending={createFeedback.pending || pendingRowWrites.size > 0}
            label={t('common.refresh')}
            onClick={pageRefresh.refresh}
          />
        }
        sx={{ mb: 2 }}
      />

      <SectionCard title={t('tenants.create.title')} sx={{ mb: 3 }}>
        <Box
          sx={{
            display: 'flex',
            flexWrap: 'wrap',
            alignItems: 'flex-start',
            columnGap: 2,
            rowGap: 1,
          }}
        >
          <TextField
            size="small"
            value={newId}
            onChange={(event) => setNewId(event.target.value)}
            label={t('tenants.create.idLabel')}
            error={idRejected}
            helperText={t('tenants.identifierHelper')}
          />
          <TextField
            size="small"
            value={newAlias}
            onChange={(event) => setNewAlias(event.target.value)}
            label={t('tenants.create.aliasLabel')}
            error={aliasRejected}
            helperText={t('tenants.identifierHelper')}
          />
          <Button
            type="button"
            variant="contained"
            disabled={createFeedback.pending || idRejected || aliasRejected}
            loading={createFeedback.pending}
            onClick={create}
          >
            {t('tenants.create.submit')}
          </Button>
        </Box>
        <Box ref={createRegionRef}>
          <WriteFeedbackRegion outcome={createFeedback.outcome} />
        </Box>
      </SectionCard>

      <SectionCard flushContent>
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
              rowKey={(tenant) => tenant.id}
            />
          )}
        </AdminAsyncSection>
      </SectionCard>
    </Box>
  )
}
