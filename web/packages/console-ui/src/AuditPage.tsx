/**
 * AuditPage: the audit-query page of the console.
 *
 * It reads the audit journal's records -- the platform's append-only
 * trail, whose rows span every tenant -- and renders them newest first as
 * a table (time, action, object, actor and outcome), through ui-kit's
 * DataTable; the columns shed the least important first as the table's
 * container narrows. The read surface carries no export: exporting is another
 * permission and another module's to carry, so this page only lists.
 *
 * The read is an operation on the console operations bundle -- the host
 * binds the generated /audit/admin operation to it -- so the page builds
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
 * resolution is a second read (GET /auth/admin/accounts/:id), gated on
 * authn.account.read, which this page's audit.read does not imply: a viewer
 * holding audit.read without account.read is refused it and the cell marks
 * the value as unresolved rather than passing it off as a name. The
 * recorded time is rendered through Intl in the current language and
 * explicitly in UTC,
 * the zone the view carries it in, never the process-local default; a
 * malformed timestamp renders no time rather than throwing. The record's
 * own fields are journal data (not a closed vocabulary the surface
 * guards): they render as recorded.
 *
 * The page opens with ui-kit's PageHeader: the page title as the level-one
 * heading, a breadcrumb trail from the console to this page, and a
 * refresh action that re-reads the journal. The async section's
 * placeholders render one level down, so the heading order never skips.
 */

import { useEffect, useMemo, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  DataTable,
  ListSkeleton,
  PageHeader,
  primaryColumnMaxWidthSx,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { CappedAccountIdentity } from './internal/account-identity.js'
import { AUDIT_RECORD_COLUMN_PRIORITY } from './internal/audit-record-columns.js'
import { ConsoleLink } from './internal/navigation.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleAuditRecord } from './operations.js'

/** The default page size (rows per cursor page). */
const DEFAULT_LIMIT = 25

/** Parses a server timestamp; null for an absent or malformed value so a
 * bad row can never reach Intl and throw. */
function parseDate(iso: string | undefined): Date | null {
  if (iso === undefined) {
    return null
  }
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? null : date
}

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
  const { t, i18n } = useConsoleUiTranslation()
  const operations = useConsoleOperations()

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

  const appliedActions = useMemo(() => parseSet(action), [action])
  const appliedOutcomes = useMemo(() => parseSet(outcome), [outcome])
  const appliedActors = useMemo(() => parseSet(actor), [actor])
  const cursor = cursors[page] ?? ''

  const params = useMemo(
    () => ({
      ...(since === '' ? {} : { since: toInstant(since) }),
      ...(until === '' ? {} : { until: toInstant(until) }),
      ...(appliedActions.length === 0 ? {} : { action: appliedActions }),
      ...(appliedOutcomes.length === 0 ? {} : { outcome: appliedOutcomes }),
      ...(appliedActors.length === 0 ? {} : { actor: appliedActors }),
      ...(cursor === '' ? {} : { cursor }),
      limit: DEFAULT_LIMIT,
    }),
    [since, until, appliedActions, appliedOutcomes, appliedActors, cursor],
  )

  const records = operations.useAuditRecords(params)
  const pageData = records.data

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

  // The view carries each record's time in UTC, so it is formatted in UTC
  // explicitly -- never the engine's implicit process-local default.
  const formatTime = useMemo(
    () =>
      new Intl.DateTimeFormat(i18n.language, {
        dateStyle: 'medium',
        timeStyle: 'short',
        timeZone: 'UTC',
      }),
    [i18n.language],
  )

  // The columns the table renders: when it happened, what it was, to what,
  // who did it and how it ended. The object column carries the route the
  // action names, so it shares the record's identity with the action and
  // outranks the actor; the actor and the outcome share the `md` tier so a
  // failure stays visible at laptop width. The priorities are the set both
  // audit-record tables share (see AUDIT_RECORD_COLUMN_PRIORITY).
  const columns: DataTableColumn<ConsoleAuditRecord>[] = [
    {
      id: 'time',
      header: t('audit.column.time'),
      cellWrap: 'nowrap',
      cell: (record) => {
        const occurred = parseDate(record.occurred_at)
        return occurred === null ? null : formatTime.format(occurred)
      },
    },
    {
      id: 'action',
      header: t('audit.column.action'),
      cellWrap: 'nowrap',
      cellFullValue: (record) => record.action,
      // A recorded action (an HTTP method and route) can run long; the
      // shared width cap truncates the cell and the complete value stays
      // on the title and copy control.
      cell: (record) => (
        <Typography variant="body2" noWrap sx={primaryColumnMaxWidthSx}>
          {basePath === undefined ? (
            record.action
          ) : (
            <ConsoleLink href={`${basePath}/${encodeURIComponent(record.id)}`}>
              {record.action}
            </ConsoleLink>
          )}
        </Typography>
      ),
    },
    {
      id: 'object',
      header: t('audit.column.object'),
      priority: AUDIT_RECORD_COLUMN_PRIORITY.object,
      cellWrap: 'nowrap',
      cellFullValue: (record) =>
        record.object === '' ? undefined : record.object,
      cell: (record) => (
        <Typography variant="body2" noWrap sx={primaryColumnMaxWidthSx}>
          {record.object}
        </Typography>
      ),
    },
    {
      id: 'actor',
      header: t('audit.column.actor'),
      priority: AUDIT_RECORD_COLUMN_PRIORITY.actor,
      cellWrap: 'nowrap',
      cellFullValue: (record) =>
        record.actor === '' ? undefined : record.actor,
      cell: (record) => <CappedAccountIdentity account={record.actor} />,
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

  return (
    <Box>
      <PageHeader
        title={t('audit.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('audit.title') },
        ]}
        actions={
          <Button
            type="button"
            variant="outlined"
            size="small"
            disabled={records.isFetching}
            loading={records.isFetching}
            onClick={() => void records.refetch()}
          >
            {t('common.refresh')}
          </Button>
        }
        sx={{ mb: 2 }}
      />

      <Box
        component="section"
        aria-label={t('audit.filters.label')}
        sx={{
          mb: 3,
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'flex-end',
          columnGap: 2,
          rowGap: 1,
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

      <ConsoleAsyncSection
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
      </ConsoleAsyncSection>

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
