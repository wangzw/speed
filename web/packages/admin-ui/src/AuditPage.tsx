/**
 * AuditPage: the audit-query page of the console.
 *
 * It reads the audit journal's records -- the platform's append-only
 * trail, whose rows span every tenant -- and renders them newest first as
 * a table (time, action, object, actor and outcome), through ui-kit's
 * DataTable; the columns shed the least important first as the table's
 * container narrows. The toolbar beside the filters exports the records
 * the applied filters match as one NDJSON file: the control calls the
 * operations bundle's export action once, saves the Blob the streamed
 * route answers under the export's UTC instant, and reports a refusal in
 * the toolbar rather than in the read's placeholder. A caption under the
 * control states what the file covers -- every record the applied filters
 * match, not just the page on screen -- and is the control's accessible
 * description. A download is its own confirmation, so a successful export
 * adds no success message; a bundle without the export action renders no
 * control.
 *
 * The read is an operation on the console operations bundle -- the host
 * binds the generated /api/v1/system/audit operation to it -- so the page builds
 * no client and writes no query key. The query is narrowed server-side by
 * the filters the toolbar carries: a `since` and `until` bound (two
 * temporal bounds the single free-text filter box cannot express) and
 * `action`, `outcome` and `actor` sets (each comma- or space-separated),
 * applied on submit so a keystroke never fires a request. The rows page by
 * cursor -- the read answers with the next page's cursor -- so the footer
 * advances through a cursor stack the page keeps; the total is unknown (a
 * keyset read does not count), which the footer shows as its no-total
 * wording.
 *
 * The actor column renders a readable identity, not the raw account
 * identifier: the account identity unit resolves the account's display
 * name through the same bound surface and falls back to the recorded value
 * when the actor names no account (a system or import actor). That
 * resolution is a second read (GET /api/v1/system/auth/accounts/:id), gated on
 * authn.account.read, which this page's audit.read does not imply: a viewer
 * holding audit.read without account.read is refused it and the cell marks
 * the value as unresolved rather than passing it off as a name. The
 * recorded time is rendered through @speed/i18n's formatInstant in the
 * current language and explicitly in UTC, the zone the view carries it in,
 * never the process-local default; a malformed timestamp renders no time
 * rather than throwing. The record's own fields are journal data (not a
 * closed vocabulary the surface guards): they render as recorded.
 *
 * The page opens with ui-kit's PageHeader: the page title as the level-one
 * heading, a breadcrumb trail from the console to this page, and a
 * refresh action that re-reads the journal. The async section's
 * placeholders render one level down, so the heading order never skips.
 */

import { useEffect, useMemo, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  DataTable,
  ListSkeleton,
  LONG_TEXT_COLUMN_MAX_WIDTH,
  PRIMARY_COLUMN_MAX_WIDTH,
  SectionCard,
  errorCodeOf,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { formatInstant } from '@speed/i18n'
import { AdminAsyncSection } from './internal/async-section.js'
import {
  AccountIdentity,
  AuditObjectIdentity,
  isAccountObject,
} from './internal/account-identity.js'
import { AUDIT_RECORD_COLUMN_PRIORITY } from './internal/audit-record-columns.js'
import { exportFileName, saveBlob } from './internal/export-file.js'
import { InlineError } from './internal/inline-error.js'
import { AdminLink } from './internal/navigation.js'
import { RefreshButton } from '@speed/ui-kit'
import { RefreshStatus } from './internal/refresh-status.js'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  pageRefreshRead,
  usePageRefresh,
} from '@speed/ui-kit'
import { useAdminOperations } from './operations.js'
import type { AdminAuditRecord } from './operations.js'
import { AdminPageHeader } from './internal/page-header.js'

/** The default page size (rows per cursor page). */
const DEFAULT_LIMIT = 25

/** The id binding the export control's scope note as its description. */
const EXPORT_NOTE_ID = 'audit-export-note'

/**
 * Splits one filter's free text into the set the read takes: comma- or
 * whitespace-separated names, blanks dropped. An empty input means no
 * filter. The action, outcome and actor boxes share this shape.
 */
function parseSet(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((part) => part.trim())
    .filter((part) => part !== '')
}

/**
 * A datetime-local value (the browser control's `YYYY-MM-DDTHH:mm`) as an
 * RFC 3339 instant in UTC. An empty or unparseable value yields undefined
 * (no bound) rather than an invalid request parameter.
 */
function toInstant(local: string): string | undefined {
  if (local === '') {
    return undefined
  }
  const date = new Date(local)
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
}

export function AuditPage({ basePath }: { readonly basePath?: string }) {
  const { t, i18n } = useAdminUiTranslation()
  const operations = useAdminOperations()

  // The applied filters (committed on submit) and their drafts.
  const [since, setSince] = useState('')
  const [until, setUntil] = useState('')
  const [action, setAction] = useState('')
  const [outcome, setOutcome] = useState('')
  const [actor, setActor] = useState('')
  const [sinceDraft, setSinceDraft] = useState('')
  const [untilDraft, setUntilDraft] = useState('')
  const [actionDraft, setActionDraft] = useState('')
  const [outcomeDraft, setOutcomeDraft] = useState('')
  const [actorDraft, setActorDraft] = useState('')
  // The cursor stack: cursors[i] is the cursor that reads page i ('' is
  // the first page), and the page advances one index at a time.
  const [cursors, setCursors] = useState<readonly string[]>([''])
  const [page, setPage] = useState(0)
  // The export action's own state: whether one is in flight (the ref also
  // guards a second activation in the same tick, before the re-render that
  // loads the control) and the code of its failure, rendered in the
  // toolbar.
  const [exporting, setExporting] = useState(false)
  const [exportError, setExportError] = useState<string | null>(null)
  const exportInFlight = useRef(false)

  const appliedActions = useMemo(() => parseSet(action), [action])
  const appliedOutcomes = useMemo(() => parseSet(outcome), [outcome])
  const appliedActors = useMemo(() => parseSet(actor), [actor])
  const cursor = cursors[page] ?? ''

  // The filters the list read -- and the export -- take: the applied
  // values, no cursor and no page size. An export is not a page; a page
  // window belongs to the list read below.
  const appliedFilters = useMemo(
    () => ({
      ...(since === '' ? {} : { since: toInstant(since) }),
      ...(until === '' ? {} : { until: toInstant(until) }),
      ...(appliedActions.length === 0 ? {} : { action: appliedActions }),
      ...(appliedOutcomes.length === 0 ? {} : { outcome: appliedOutcomes }),
      ...(appliedActors.length === 0 ? {} : { actor: appliedActors }),
    }),
    [since, until, appliedActions, appliedOutcomes, appliedActors],
  )

  const params = useMemo(
    () => ({
      ...appliedFilters,
      ...(cursor === '' ? {} : { cursor }),
      limit: DEFAULT_LIMIT,
    }),
    [appliedFilters, cursor],
  )

  const records = operations.useAuditRecords(params)
  const pageData = records.data

  // The journal read is the page's whole body: the refresh action re-reads
  // it, and the action group shows when it last settled.
  const pageRefresh = usePageRefresh(
    [pageRefreshRead('audit-records', records)],
    () => undefined,
  )

  // Record the next page's cursor the read answered with, so the footer
  // can advance to it. The cursor for page i+1 is only known once page i
  // has answered, so the stack grows as pages are visited.
  useEffect(() => {
    const next = pageData?.next
    if (next === undefined || next === '' || page + 1 < cursors.length) {
      return
    }
    setCursors((previous) => {
      const grown = [...previous]
      grown[page + 1] = next
      return grown
    })
  }, [pageData, page, cursors.length])

  // The columns the table renders: when it happened, what it was, to what,
  // who did it and how it ended. The object column carries the route the
  // action names, so it shares the record's identity with the action and
  // outranks the actor; the actor and the outcome share the `md` tier so a
  // failure stays visible at laptop width. The priorities are the set both
  // audit-record tables share (see AUDIT_RECORD_COLUMN_PRIORITY).
  const columns: DataTableColumn<AdminAuditRecord>[] = [
    {
      id: 'time',
      header: t('audit.column.time'),
      cellWrap: 'nowrap',
      cell: (record) =>
        formatInstant(record.occurred_at, i18n.language, {
          timeZone: 'UTC',
        }) || null,
    },
    {
      id: 'action',
      header: t('audit.column.action'),
      cellWrap: 'nowrap',
      cellFullValue: (record) => record.action,
      // A recorded action (an HTTP method and route) can run long; the
      // primary-identity cap truncates the text and the complete value
      // stays on the title and copy control.
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (record) => (
        <Typography variant="body2" noWrap>
          {basePath === undefined ? (
            record.action
          ) : (
            <AdminLink href={`${basePath}/${encodeURIComponent(record.id)}`}>
              {record.action}
            </AdminLink>
          )}
        </Typography>
      ),
    },
    {
      id: 'object',
      header: t('audit.column.object'),
      priority: AUDIT_RECORD_COLUMN_PRIORITY.object,
      cellWrap: 'nowrap',
      // An object marked as an account resolves through the account read, and
      // its copy control is withheld: the opaque id is not a value an operator
      // acts on. Every other object -- a session id, a tenant alias, a role
      // name, a permission code -- stays as recorded and copyable.
      cellFullValue: (record) =>
        record.object === '' || isAccountObject(record.object)
          ? undefined
          : record.object,
      contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
      cell: (record) => (
        <Typography variant="body2" noWrap>
          <AuditObjectIdentity object={record.object} />
        </Typography>
      ),
    },
    {
      id: 'actor',
      header: t('audit.column.actor'),
      priority: AUDIT_RECORD_COLUMN_PRIORITY.actor,
      cellWrap: 'nowrap',
      // The actor's identity comes from the account read, so the copy
      // control is withheld rather than handing over the opaque id.
      contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
      cell: (record) => <AccountIdentity account={record.actor} />,
    },
    {
      id: 'outcome',
      header: t('audit.column.outcome'),
      priority: AUDIT_RECORD_COLUMN_PRIORITY.outcome,
      cellWrap: 'nowrap',
      cell: (record) => record.outcome,
    },
  ]

  const rows = pageData?.entries
  const hasNextPage = pageData?.next !== undefined && pageData.next !== ''
  const pending = records.isPending && pageData === undefined

  /** Applies the drafts as the active filters, resetting to the first page. */
  const applyFilters = () => {
    setSince(sinceDraft)
    setUntil(untilDraft)
    setAction(actionDraft)
    setOutcome(outcomeDraft)
    setActor(actorDraft)
    setCursors([''])
    setPage(0)
  }

  /** Clears every filter and returns to the first page. */
  const resetFilters = () => {
    setSinceDraft('')
    setUntilDraft('')
    setActionDraft('')
    setOutcomeDraft('')
    setActorDraft('')
    setSince('')
    setUntil('')
    setAction('')
    setOutcome('')
    setActor('')
    setCursors([''])
    setPage(0)
  }

  /**
   * Exports every record the applied filters match, once per activation:
   * the action fetches the NDJSON Blob and hands it to the browser as a
   * file named by this click's instant. A failure replaces the toolbar's
   * error text; a success adds nothing, because the download is its own
   * confirmation.
   */
  const startExport = async () => {
    const exportRecords = operations.exportAuditRecords
    if (exportRecords === undefined || exportInFlight.current) {
      return
    }
    exportInFlight.current = true
    setExporting(true)
    setExportError(null)
    const startedAt = new Date()
    try {
      const blob = await exportRecords(appliedFilters)
      saveBlob(blob, exportFileName(startedAt))
    } catch (error) {
      setExportError(errorCodeOf(error))
    } finally {
      exportInFlight.current = false
      setExporting(false)
    }
  }

  return (
    <Box>
      <AdminPageHeader
        title={t('audit.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('audit.title') },
        ]}
        actions={
          <>
            <RefreshStatus
              updatedAt={pageRefresh.updatedAt}
              announcements={pageRefresh.announcements}
            />
            <RefreshButton
              busy={pageRefresh.busy}
              label={t('common.refresh')}
              onClick={pageRefresh.refresh}
            />
          </>
        }
        sx={{ mb: 2 }}
      />

      <SectionCard sx={{ mb: 3 }}>
        <Box
          sx={{
            display: 'flex',
            flexWrap: 'wrap',
            alignItems: 'flex-end',
            columnGap: 2,
            rowGap: 1,
          }}
        >
          <Box
            role="group"
            aria-label={t('audit.filters.label')}
            sx={{
              display: 'flex',
              flexWrap: 'wrap',
              alignItems: 'flex-end',
              columnGap: 2,
              rowGap: 1,
              flexGrow: 1,
            }}
          >
            <TextField
              size="small"
              type="datetime-local"
              value={sinceDraft}
              onChange={(event) => setSinceDraft(event.target.value)}
              label={t('audit.filters.since')}
              slotProps={{ inputLabel: { shrink: true } }}
            />
            <TextField
              size="small"
              type="datetime-local"
              value={untilDraft}
              onChange={(event) => setUntilDraft(event.target.value)}
              label={t('audit.filters.until')}
              slotProps={{ inputLabel: { shrink: true } }}
            />
            <TextField
              size="small"
              value={actionDraft}
              onChange={(event) => setActionDraft(event.target.value)}
              label={t('audit.filters.action')}
              placeholder={t('audit.filters.valuePlaceholder')}
            />
            <TextField
              size="small"
              value={outcomeDraft}
              onChange={(event) => setOutcomeDraft(event.target.value)}
              label={t('audit.filters.outcome')}
              placeholder={t('audit.filters.valuePlaceholder')}
            />
            <TextField
              size="small"
              value={actorDraft}
              onChange={(event) => setActorDraft(event.target.value)}
              label={t('audit.filters.actor')}
              placeholder={t('audit.filters.valuePlaceholder')}
            />
            <Button type="button" variant="contained" onClick={applyFilters}>
              {t('audit.filters.apply')}
            </Button>
            <Button type="button" variant="outlined" onClick={resetFilters}>
              {t('audit.filters.reset')}
            </Button>
          </Box>
          {operations.exportAuditRecords === undefined ? null : (
            <Box
              sx={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'flex-start',
                rowGap: 0.5,
              }}
            >
              <Button
                type="button"
                variant="outlined"
                loading={exporting}
                aria-busy={exporting || undefined}
                aria-describedby={EXPORT_NOTE_ID}
                onClick={() => {
                  void startExport()
                }}
              >
                {exporting
                  ? t('audit.export.busy')
                  : t('audit.export.label')}
              </Button>
              <Typography
                id={EXPORT_NOTE_ID}
                variant="caption"
                color="text.secondary"
              >
                {t('audit.export.note')}
              </Typography>
            </Box>
          )}
        </Box>
        {exportError === null ? null : (
          <Box sx={{ mt: 2 }}>
            <InlineError code={exportError} />
          </Box>
        )}
      </SectionCard>

      <SectionCard flushContent>
        <AdminAsyncSection
          pending={pending}
          payload={rows}
          error={records.error}
          refreshing={records.isFetching}
          empty={rows !== undefined && rows.length === 0}
          headingLevel="h2"
          loading={
            <ListSkeleton
              label={t('audit.loading')}
              rows={[
                <Skeleton key="a" variant="text" width="62%" />,
                <Skeleton key="b" variant="text" width="48%" />,
              ]}
            />
          }
          errorState={{
            title: t('audit.error.title'),
            description: t('audit.error.description'),
            retryLabel: t('audit.retry'),
            onRetry: () => void records.refetch(),
          }}
          emptyState={{
            title: t('audit.empty.title'),
            description: t('audit.empty.description'),
          }}
        >
          {(settled) => (
            <DataTable
              rows={settled}
              columns={columns}
              rowKey={(record) => record.id}
            />
          )}
        </AdminAsyncSection>
      </SectionCard>

      <Box
        sx={{
          mt: 2,
          display: 'flex',
          alignItems: 'center',
          columnGap: 2,
        }}
      >
        <Button
          type="button"
          variant="outlined"
          size="small"
          disabled={page === 0}
          onClick={() => setPage((current) => Math.max(0, current - 1))}
        >
          {t('audit.prevPage')}
        </Button>
        <Button
          type="button"
          variant="outlined"
          size="small"
          disabled={!hasNextPage}
          onClick={() => {
            setCursors((previous) => {
              const grown = [...previous]
              grown[page + 1] = pageData?.next ?? ''
              return grown
            })
            setPage((current) => current + 1)
          }}
        >
          {t('audit.nextPage')}
        </Button>
      </Box>
    </Box>
  )
}
