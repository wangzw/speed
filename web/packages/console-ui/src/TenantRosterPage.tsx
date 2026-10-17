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
 * -- the host binds the generated /tenant/admin operations to it -- so
 * the page builds no client and writes no query key. The row's state
 * renders from the roster's own spelling (active / disabled); a value
 * outside that closed set renders the generic label, never a raw value.
 * A failed list render settles into the retryable error placeholder.
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
 * another row's. A refusal resolves through the console-ui namespace's
 * whitelist (a roster collision reads the conflict text), never a raw key,
 * and brings its region into view.
 *
 * The page opens with ui-kit's PageHeader: the page title as the level-one
 * heading, a breadcrumb trail from the console to this page, and a
 * refresh action that re-reads the roster. The async section's
 * placeholders render at the next level down, so the heading order never
 * skips.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  DataTable,
  ListSkeleton,
  PageHeader,
  useWriteFeedback,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { useConsoleUiErrorText } from './internal/error-text.js'
import { ConsoleLink } from './internal/navigation.js'
import { isRejectedIdentifier } from './internal/identifier.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import {
  failureTextOf,
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleTenant } from './operations.js'

/**
 * The roster's closed status set, one bundle key each under
 * tenants.status. A value outside it renders the generic other label,
 * never a raw value.
 */
const KNOWN_STATUSES = new Set<string>(['active', 'disabled'])

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
function TenantRowActions({ tenant }: { readonly tenant: ConsoleTenant }) {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
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
  const resolveErrorText = useConsoleUiErrorText()
  const failureText = failureTextOf(feedback.outcome, resolveErrorText)
  const statusErrorId = `tenant-status-error-${encodeURIComponent(tenant.id)}`

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

export function TenantRosterPage({ basePath }: { readonly basePath?: string }) {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const tenants = operations.useTenants()
  const createTenant = operations.useCreateTenant()
  const [newId, setNewId] = useState('')
  const [newAlias, setNewAlias] = useState('')
  // The create form is one scope; its region sits directly under it.
  const createFeedback = useWriteFeedback()
  const createRegionRef = useFailureScroll(createFeedback.outcome)

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
  const columns: DataTableColumn<ConsoleTenant>[] = [
    {
      id: 'id',
      header: t('tenants.column.id'),
      cellWrap: 'nowrap',
      cellFullValue: (tenant) => tenant.id,
      cell: (tenant) =>
        basePath === undefined ? (
          <Typography variant="body2">{tenant.id}</Typography>
        ) : (
          <ConsoleLink href={`${basePath}/${encodeURIComponent(tenant.id)}`}>
            {tenant.id}
          </ConsoleLink>
        ),
    },
    {
      id: 'alias',
      header: t('tenants.column.alias'),
      cellWrap: 'nowrap',
      cell: (tenant) => (
        <Typography variant="body2" sx={{ fontWeight: 500 }}>
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
        <Typography variant="body2" color="text.secondary">
          {statusLabel(tenant.status, t)}
        </Typography>
      ),
    },
    {
      id: 'actions',
      header: t('tenants.column.actions'),
      cell: (tenant) => <TenantRowActions tenant={tenant} />,
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
          <Button
            type="button"
            variant="outlined"
            size="small"
            disabled={tenants.isFetching}
            loading={tenants.isFetching}
            onClick={() => void tenants.refetch()}
          >
            {t('common.refresh')}
          </Button>
        }
        sx={{ mb: 2 }}
      />

      <Box component="section" sx={{ mb: 3 }}>
        <Typography variant="h2" sx={{ fontSize: '1.25rem', mb: 1 }}>
          {t('tenants.create.title')}
        </Typography>
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
      </Box>

      <ConsoleAsyncSection
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
      </ConsoleAsyncSection>
    </Box>
  )
}
