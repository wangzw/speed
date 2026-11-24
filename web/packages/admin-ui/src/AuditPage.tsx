import { useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  ActionButton,
  DataTable,
  FormField,
  ListSkeleton,
  LONG_TEXT_COLUMN_MAX_WIDTH,
  PRIMARY_COLUMN_MAX_WIDTH,
  SectionCard,
  errorCodeOf,
  useFormatInstant,
  useResolvedTimeZone,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { instantToWallClock, wallClockToInstant } from '@speed/i18n'
import type { WallClock } from '@speed/i18n'
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

const DEFAULT_LIMIT = 25

const EXPORT_NOTE_ID = 'audit-export-note'

const EXPORT_APPLY_FIRST_ID = 'audit-export-apply-first'

type BoundProblem = 'outOfRange' | 'timeUnavailable'

interface FilterDrafts {
  readonly since: string
  readonly until: string
  readonly action: string
  readonly outcome: string
  readonly actor: string
}

const EMPTY_FILTER_DRAFTS: FilterDrafts = {
  since: '',
  until: '',
  action: '',
  outcome: '',
  actor: '',
}

function parseSet(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((part) => part.trim())
    .filter((part) => part !== '')
}

function toWallClock(local: string): WallClock | undefined {
  const match =
    /^(\d{4,})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?$/.exec(local)
  if (match === null) {
    return undefined
  }
  return {
    year: Number(match[1]),
    month: Number(match[2]),
    day: Number(match[3]),
    hour: Number(match[4]),
    minute: Number(match[5]),
    second: match[6] === undefined ? 0 : Number(match[6]),
  }
}

function toInstant(
  local: string,
  timeZone: string,
  occurrence: 'earlier' | 'later',
  preserved?: string,
): string | undefined {
  if (local === '') {
    return undefined
  }
  const wallClock = toWallClock(local)
  if (wallClock === undefined) {
    return undefined
  }
  const resolved = wallClockToInstant(wallClock, timeZone, {
    overlap: occurrence,
  })
  if (resolved === null) {
    return undefined
  }
  if (preserved !== undefined) {
    const preservedClock = instantToWallClock(preserved, timeZone)
    if (
      preservedClock !== null &&
      sameReading(preservedClock, wallClock)
    ) {
      return preserved
    }
  }
  return resolved.toISOString()
}

function sameReading(left: WallClock, right: WallClock): boolean {
  return (
    left.year === right.year &&
    left.month === right.month &&
    left.day === right.day &&
    left.hour === right.hour &&
    left.minute === right.minute &&
    (left.second ?? 0) === (right.second ?? 0)
  )
}

function isRepresentableReading(clock: WallClock): boolean {
  const second = clock.second ?? 0
  const fields = [
    clock.year,
    clock.month,
    clock.day,
    clock.hour,
    clock.minute,
    second,
  ]
  if (!fields.every((field) => Number.isInteger(field))) {
    return false
  }
  if (
    clock.year < 1 ||
    clock.year > 9999 ||
    clock.month < 1 ||
    clock.month > 12 ||
    clock.day < 1 ||
    clock.day > 31 ||
    clock.hour < 0 ||
    clock.hour > 23 ||
    clock.minute < 0 ||
    clock.minute > 59 ||
    second < 0 ||
    second > 59
  ) {
    return false
  }
  const date = new Date(0)
  date.setUTCFullYear(clock.year, clock.month - 1, clock.day)
  date.setUTCHours(clock.hour, clock.minute, second, 0)
  return (
    date.getUTCFullYear() === clock.year &&
    date.getUTCMonth() === clock.month - 1 &&
    date.getUTCDate() === clock.day &&
    date.getUTCHours() === clock.hour &&
    date.getUTCMinutes() === clock.minute &&
    date.getUTCSeconds() === second
  )
}

function boundProblem(local: string, timeZone: string): BoundProblem | null {
  if (local === '') {
    return null
  }
  const wallClock = toWallClock(local)
  if (wallClock === undefined || !isRepresentableReading(wallClock)) {
    return 'outOfRange'
  }
  return wallClockToInstant(wallClock, timeZone, {
    overlap: 'earlier',
  }) === null
    ? 'timeUnavailable'
    : null
}

function wallClockDraft(clock: WallClock): string {
  const pad = (value: number, width = 2): string =>
    String(value).padStart(width, '0')
  const base = `${pad(clock.year, 4)}-${pad(clock.month)}-${pad(clock.day)}T${pad(clock.hour)}:${pad(clock.minute)}`
  const second = clock.second ?? 0
  return second === 0 ? base : `${base}:${pad(second)}`
}

function draftMatchesInstant(
  local: string,
  instant: string | undefined,
  timeZone: string,
  occurrence: 'earlier' | 'later',
): boolean {
  if (local === '') {
    return instant === undefined
  }
  const resolved = toInstant(local, timeZone, occurrence, instant)
  return resolved !== undefined && resolved === instant
}

function boundProblemKey(problem: BoundProblem): string {
  return problem === 'outOfRange'
    ? 'audit.filters.timeOutOfRange'
    : 'audit.filters.timeUnavailable'
}

/** AuditPage reads, exports and links the platform's audit records. `basePath`
 * is the query page's matched path; a row's action links to the record's
 * detail page below it, and absent leaves the action as plain text. */
export function AuditPage({ basePath }: { readonly basePath?: string }) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const formatTime = useFormatInstant()
  const displayTimeZone = useResolvedTimeZone()

  const [sinceInstant, setSinceInstant] = useState<string | undefined>(
    undefined,
  )
  const [untilInstant, setUntilInstant] = useState<string | undefined>(
    undefined,
  )
  const [action, setAction] = useState('')
  const [outcome, setOutcome] = useState('')
  const [actor, setActor] = useState('')
  const form = useForm<FilterDrafts>({ defaultValues: EMPTY_FILTER_DRAFTS })
  const drafts = form.watch()
  const sinceInput = useRef<HTMLInputElement>(null)
  const untilInput = useRef<HTMLInputElement>(null)
  const [cursors, setCursors] = useState<readonly string[]>([''])
  const [page, setPage] = useState(0)
  // The ref guards a second activation in the same tick, before the re-render
  // that loads the control.
  const [exporting, setExporting] = useState(false)
  const [exportError, setExportError] = useState<string | null>(null)
  const exportInFlight = useRef(false)

  const appliedActions = useMemo(() => parseSet(action), [action])
  const appliedOutcomes = useMemo(() => parseSet(outcome), [outcome])
  const appliedActors = useMemo(() => parseSet(actor), [actor])
  const cursor = cursors[page] ?? ''

  const draftsDirty =
    !draftMatchesInstant(
      drafts.since,
      sinceInstant,
      displayTimeZone,
      'earlier',
    ) ||
    !draftMatchesInstant(
      drafts.until,
      untilInstant,
      displayTimeZone,
      'later',
    ) ||
    drafts.action !== action ||
    drafts.outcome !== outcome ||
    drafts.actor !== actor

  const previousTimeZone = useRef(displayTimeZone)
  useEffect(() => {
    if (previousTimeZone.current === displayTimeZone) {
      return
    }
    previousTimeZone.current = displayTimeZone
    const current = form.getValues()
    const regenerate = (instant: string | undefined, draft: string): string => {
      if (instant === undefined) {
        return draft
      }
      const clock = instantToWallClock(instant, displayTimeZone)
      return clock === null ? draft : wallClockDraft(clock)
    }
    const since = regenerate(sinceInstant, current.since)
    const until = regenerate(untilInstant, current.until)
    form.setValue('since', since)
    form.setValue('until', until)
    const sinceProblem = boundProblem(since, displayTimeZone)
    const untilProblem = boundProblem(until, displayTimeZone)
    if (sinceProblem === null) {
      form.clearErrors('since')
    } else {
      form.setError('since', {
        type: 'manual',
        message: boundProblemKey(sinceProblem),
      })
    }
    if (untilProblem === null) {
      form.clearErrors('until')
    } else {
      form.setError('until', {
        type: 'manual',
        message: boundProblemKey(untilProblem),
      })
    }
  }, [displayTimeZone, sinceInstant, untilInstant, form])

  const appliedFilters = useMemo(
    () => ({
      ...(sinceInstant === undefined ? {} : { since: sinceInstant }),
      ...(untilInstant === undefined ? {} : { until: untilInstant }),
      ...(appliedActions.length === 0 ? {} : { action: appliedActions }),
      ...(appliedOutcomes.length === 0 ? {} : { outcome: appliedOutcomes }),
      ...(appliedActors.length === 0 ? {} : { actor: appliedActors }),
    }),
    [
      sinceInstant,
      untilInstant,
      appliedActions,
      appliedOutcomes,
      appliedActors,
    ],
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

  const pageRefresh = usePageRefresh(
    [pageRefreshRead('audit-records', records)],
    () => undefined,
  )

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

  const columns: DataTableColumn<AdminAuditRecord>[] = [
    {
      id: 'time',
      header: t('audit.column.time'),
      cellWrap: 'nowrap',
      cell: (record) => formatTime(record.occurred_at) || null,
    },
    {
      id: 'action',
      header: t('audit.column.action'),
      cellWrap: 'nowrap',
      cellFullValue: (record) => record.action,
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

  const setBoundProblem = (
    name: 'since' | 'until',
    problem: BoundProblem | null,
  ) => {
    if (problem === null) {
      form.clearErrors(name)
      return
    }
    form.setError(name, {
      type: 'manual',
      message: boundProblemKey(problem),
    })
  }

  const draftsMatchApplied = (values: FilterDrafts): boolean =>
    draftMatchesInstant(
      values.since,
      sinceInstant,
      displayTimeZone,
      'earlier',
    ) &&
    draftMatchesInstant(
      values.until,
      untilInstant,
      displayTimeZone,
      'later',
    ) &&
    values.action === action &&
    values.outcome === outcome &&
    values.actor === actor

  const applyFilters = () => {
    const values = form.getValues()
    const sinceProblem = boundProblem(values.since, displayTimeZone)
    const untilProblem = boundProblem(values.until, displayTimeZone)
    setExportError(null)
    setBoundProblem('since', sinceProblem)
    setBoundProblem('until', untilProblem)
    if (sinceProblem !== null || untilProblem !== null) {
      if (sinceProblem !== null) {
        sinceInput.current?.focus()
      } else {
        untilInput.current?.focus()
      }
      return
    }
    setSinceInstant(
      toInstant(values.since, displayTimeZone, 'earlier', sinceInstant),
    )
    setUntilInstant(
      toInstant(values.until, displayTimeZone, 'later', untilInstant),
    )
    setAction(values.action)
    setOutcome(values.outcome)
    setActor(values.actor)
    setCursors([''])
    setPage(0)
  }

  const resetFilters = () => {
    form.reset(EMPTY_FILTER_DRAFTS)
    setSinceInstant(undefined)
    setUntilInstant(undefined)
    setAction('')
    setOutcome('')
    setActor('')
    setExportError(null)
    setCursors([''])
    setPage(0)
  }

  const startExport = async () => {
    const exportRecords = operations.exportAuditRecords
    if (exportRecords === undefined || exportInFlight.current) {
      return
    }
    setExportError(null)
    // The control's unavailable state reflects the last render only: a draft
    // edited just before the click still reaches this handler, so this guard
    // is not redundant.
    if (!draftsMatchApplied(form.getValues())) {
      return
    }
    exportInFlight.current = true
    setExporting(true)
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
            <FormField
              name="since"
              control={form.control}
              render={({ field, invalid, errorMessage }) => (
                <TextField
                  {...field}
                  inputRef={sinceInput}
                  size="small"
                  type="datetime-local"
                  onChange={(event) => {
                    field.onChange(event)
                    form.clearErrors('since')
                  }}
                  label={t('audit.filters.since')}
                  error={invalid}
                  helperText={errorMessage === null ? undefined : t(errorMessage)}
                  slotProps={{ inputLabel: { shrink: true } }}
                />
              )}
            />
            <FormField
              name="until"
              control={form.control}
              render={({ field, invalid, errorMessage }) => (
                <TextField
                  {...field}
                  inputRef={untilInput}
                  size="small"
                  type="datetime-local"
                  onChange={(event) => {
                    field.onChange(event)
                    form.clearErrors('until')
                  }}
                  label={t('audit.filters.until')}
                  error={invalid}
                  helperText={
                    errorMessage === null ? undefined : t(errorMessage)
                  }
                  slotProps={{ inputLabel: { shrink: true } }}
                />
              )}
            />
            <FormField
              name="action"
              control={form.control}
              render={({ field }) => (
                <TextField
                  {...field}
                  size="small"
                  onChange={(event) => {
                    field.onChange(event)
                  }}
                  label={t('audit.filters.action')}
                  placeholder={t('audit.filters.valuePlaceholder')}
                />
              )}
            />
            <FormField
              name="outcome"
              control={form.control}
              render={({ field }) => (
                <TextField
                  {...field}
                  size="small"
                  onChange={(event) => {
                    field.onChange(event)
                  }}
                  label={t('audit.filters.outcome')}
                  placeholder={t('audit.filters.valuePlaceholder')}
                />
              )}
            />
            <FormField
              name="actor"
              control={form.control}
              render={({ field }) => (
                <TextField
                  {...field}
                  size="small"
                  onChange={(event) => {
                    field.onChange(event)
                  }}
                  label={t('audit.filters.actor')}
                  placeholder={t('audit.filters.valuePlaceholder')}
                />
              )}
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
              <ActionButton
                type="button"
                busy={exporting}
                unavailable={draftsDirty}
                unavailableDescribedBy={EXPORT_APPLY_FIRST_ID}
                aria-describedby={EXPORT_NOTE_ID}
                onClick={() => {
                  void startExport()
                }}
              >
                {t('audit.export.label')}
              </ActionButton>
              <Typography
                id={EXPORT_NOTE_ID}
                variant="caption"
                color="text.secondary"
              >
                {t('audit.export.note')}
              </Typography>
              {draftsDirty ? (
                <Typography
                  id={EXPORT_APPLY_FIRST_ID}
                  variant="caption"
                  color="text.secondary"
                >
                  {t('audit.export.applyFirst')}
                </Typography>
              ) : null}
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
        <ActionButton
          type="button"
          size="small"
          unavailable={page === 0}
          onClick={() => setPage((current) => Math.max(0, current - 1))}
        >
          {t('audit.prevPage')}
        </ActionButton>
        <ActionButton
          type="button"
          size="small"
          unavailable={!hasNextPage}
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
        </ActionButton>
      </Box>
    </Box>
  )
}
