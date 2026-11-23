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
 * control. The export acts on the applied filters, never on newer drafts:
 * while any draft differs from the applied set the control is unavailable,
 * and a hint beside it -- the control's accessible description -- says to
 * apply first, so the visible fields and the payload cannot disagree. The
 * control stays focusable, so a keyboard user keeps their place while the
 * condition holds and while an export runs. A later attempt replaces the
 * previous attempt's failure, and applying or resetting the filters clears
 * it, so no stale banner survives either.
 *
 * The read is an operation on the console operations bundle -- the host
 * binds the generated /api/v1/system/audit operation to it -- so the page builds
 * no client and writes no query key. The query is narrowed server-side by
 * the filters the toolbar carries: a `since` and `until` bound (two
 * temporal bounds the single free-text filter box cannot express) and
 * `action`, `outcome` and `actor` sets (each comma- or space-separated),
 * applied on submit so a keystroke never fires a request. A bound the
 * display zone cannot map to an instant is refused on its field and never
 * applied, so a field that shows a time always reaches the server as that
 * bound; an impossible reading is reported as out of the accepted range, a
 * real reading the zone skips as unavailable there, and the first refused
 * field takes the focus. The export refuses the same way. A display-zone
 * change re-reads the applied instants as wall clocks in the new zone and
 * regenerates the time drafts from them, re-checking each against the new
 * zone without moving the moments they denote; in a fall-back overlap a new
 * reading takes the earlier occurrence for the lower bound and the later one
 * for the upper, so the range spans both repeated hours, while a regenerated
 * draft keeps the applied instant it was read from. A field's refusal is
 * translated when it renders, so it follows a language change like every
 * other string on the page. The rows page by
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
 * recorded time is rendered through ui-kit's useFormatInstant in the active
 * language and the resolved display zone; a malformed timestamp renders no
 * time rather than throwing. The record's own fields are journal data (not a
 * closed vocabulary the surface guards): they render as recorded.
 *
 * The page opens with ui-kit's PageHeader: the page title as the level-one
 * heading, a breadcrumb trail from the console to this page, and a
 * refresh action that re-reads the journal. The async section's
 * placeholders render one level down, so the heading order never skips.
 */

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

/** The default page size (rows per cursor page). */
const DEFAULT_LIMIT = 25

/** The id binding the export control's scope note as its description. */
const EXPORT_NOTE_ID = 'audit-export-note'

/** The id binding the export control's unapplied-changes hint. */
const EXPORT_APPLY_FIRST_ID = 'audit-export-apply-first'

/** How a bound falls short of the instant it should name. */
type BoundProblem = 'outOfRange' | 'timeUnavailable'

/** The filter toolbar's drafts, in the shape react-hook-form holds them. */
interface FilterDrafts {
  readonly since: string
  readonly until: string
  readonly action: string
  readonly outcome: string
  readonly actor: string
}

/** The empty filter toolbar: no bound, no set. */
const EMPTY_FILTER_DRAFTS: FilterDrafts = {
  since: '',
  until: '',
  action: '',
  outcome: '',
  actor: '',
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
 * A datetime-local value (the browser control's `YYYY-MM-DDTHH:mm`, with an
 * optional `:ss`) as the wall-clock reading it names. A value the control
 * cannot produce yields undefined; an impossible calendar reading is
 * rejected when the reading is resolved to an instant.
 */
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

/**
 * A datetime-local value as an RFC 3339 instant: the control carries a wall
 * clock, and `timeZone` is the zone it is read in. An empty value, an
 * unparseable one, or a reading that names no instant in that zone (a
 * spring-forward gap, an impossible date) yields undefined. In a fall-back
 * overlap a new reading takes the occurrence `occurrence` names: the earlier
 * one for a lower bound and the later one for an upper bound, so the range
 * spans both repeated hours.
 *
 * A reading that says the same wall clock as `preserved` resolves to that
 * instant. A datetime-local field cannot tell the two occurrences apart, so a
 * draft regenerated from an applied instant would otherwise resolve to the
 * other one and move a bound the operator never touched. The preserved
 * instant is used only while the reading is unchanged; editing the field to a
 * different reading, or clearing it, resolves normally.
 */
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

/** Whether two wall-clock readings name the same fields. */
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

/**
 * Whether a reading names a real calendar instant, ignoring the zone: the
 * fields are whole and in range and the calendar does not overflow (31
 * February). A reading the zone skips is representable here; only the zone
 * conversion can call it unavailable.
 */
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

/**
 * Why a non-empty time draft names no instant in `timeZone`, or null when
 * it does. The two refusals are told apart because they are not the same
 * mistake: a reading the calendar or the field range rejects is out of the
 * accepted range, while a real reading the display zone skips (the
 * spring-forward gap) does not exist there. A blank draft is no bound and
 * is fine.
 */
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

/** A wall-clock reading as the datetime-local value the control shows. */
function wallClockDraft(clock: WallClock): string {
  const pad = (value: number, width = 2): string =>
    String(value).padStart(width, '0')
  const base = `${pad(clock.year, 4)}-${pad(clock.month)}-${pad(clock.day)}T${pad(clock.hour)}:${pad(clock.minute)}`
  const second = clock.second ?? 0
  return second === 0 ? base : `${base}:${pad(second)}`
}

/**
 * Whether a time draft still names the instant it was applied as. An empty
 * draft matches only an absent bound; a draft that names no instant never
 * matches, so a refused or edited reading counts as an unapplied change.
 */
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

/** The translation key naming why a bound is refused. */
function boundProblemKey(problem: BoundProblem): string {
  return problem === 'outOfRange'
    ? 'audit.filters.timeOutOfRange'
    : 'audit.filters.timeUnavailable'
}

export function AuditPage({ basePath }: { readonly basePath?: string }) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const formatTime = useFormatInstant()
  const displayTimeZone = useResolvedTimeZone()

  // The applied filters (committed on submit) and their drafts. A time
  // bound is held as the instant it resolved to when applied, so a later
  // display-zone change cannot reinterpret or drop it.
  const [sinceInstant, setSinceInstant] = useState<string | undefined>(
    undefined,
  )
  const [untilInstant, setUntilInstant] = useState<string | undefined>(
    undefined,
  )
  const [action, setAction] = useState('')
  const [outcome, setOutcome] = useState('')
  const [actor, setActor] = useState('')
  // The drafts are react-hook-form's state so the time fields render their
  // own refusals through FormField's validation-error contract. They are not
  // the applied filters: a submit copies them across, and a display-zone
  // change regenerates the time drafts from the applied instants.
  const form = useForm<FilterDrafts>({ defaultValues: EMPTY_FILTER_DRAFTS })
  const drafts = form.watch()
  const sinceInput = useRef<HTMLInputElement>(null)
  const untilInput = useRef<HTMLInputElement>(null)
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

  // Whether the drafts still describe the applied filters. The export is an
  // action on the applied set, so a draft that differs in any field means
  // the visible toolbar and the payload disagree; the control stays off
  // until the operator applies the change.
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

  // A display-zone change re-reads the applied instants as wall clocks in
  // the new zone and re-checks every time draft against it. The applied
  // instants themselves never move, so an export keeps the moment the
  // operator applied; only the readings shown for it change.
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

  // The filters the list read -- and the export -- take: the applied
  // values, no cursor and no page size. An export is not a page; a page
  // window belongs to the list read below. Both time bounds are already
  // instants, so a non-empty field never turns into a missing parameter.
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
      cell: (record) => formatTime(record.occurred_at) || null,
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
      // acts on. Every other object -- a session id, a tenant name, a role
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

  /**
   * Reports a time draft's problem on its own field, or clears it. The error
   * carries the translation key, not its text, so the field renders it in the
   * language active when it is painted.
   */
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

  /** Whether the given drafts still describe the applied filters. */
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

  /**
   * Applies the drafts as the active filters, resetting to the first page.
   * A time draft that names no instant in the display zone is refused on
   * its field instead: applying it would otherwise drop the bound while the
   * field still shows the value. The first refused field takes the focus so
   * the refusal is announced where it can be corrected. A draft whose
   * reading is unchanged from an applied bound keeps that bound, so
   * applying a regenerated draft never moves an instant across a fall-back
   * overlap.
   */
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

  /** Clears every filter and returns to the first page. */
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

  /**
   * Exports every record the applied filters match, once per activation:
   * the action fetches the NDJSON Blob and hands it to the browser as a
   * file named by this click's instant. A failure replaces the toolbar's
   * error text; a success adds nothing, because the download is its own
   * confirmation. The gate and the payload come from the same applied
   * state: a draft that differs from it refuses the export rather than
   * sending a set the toolbar does not show. The unavailable control is the
   * visible half of that gate; the draft check here closes the same window
   * for a state the control has not painted yet.
   */
  const startExport = async () => {
    const exportRecords = operations.exportAuditRecords
    if (exportRecords === undefined || exportInFlight.current) {
      return
    }
    // A later activation replaces the previous attempt's failure.
    setExportError(null)
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
