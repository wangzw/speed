/**
 * SettingsPage: the settings read-write page of the console.
 *
 * The declared settings are grouped by namespace, and the page presents
 * them the way the layout design's settings archetype does: a secondary
 * navigation (narrower than the shell's own sidebar) listing the
 * namespaces, beside the settings themselves, with a sticky save bar at
 * the bottom. Choosing a namespace narrows the read to that namespace
 * (GET /api/v1/system/settings/:namespace over the global layer); the
 * "all namespaces" entry reads the whole declared set. The namespaces in
 * the navigation come from that whole-set read, so a namespace shows up
 * as soon as any of its items is declared.
 *
 * The items render as a full-width table (key, type, current value, the
 * row's state and its remove control), through ui-kit's DataTable. The
 * key cell also carries the row's own declaration in two stacked lines:
 * the setting's description and its declared default, labelled apart
 * from the current value beside it. The key line alone keeps the shared
 * primary-width cap and truncates there (the copy control carries the
 * complete key); the declaration is prose and wraps at the cell's
 * available width. The rows are top-aligned, so the value input and the
 * copy control sit beside the key line rather than at the middle of a
 * tall row. The declaration is the server's
 * answer, so a row never shows another row's description or default; a
 * setting with no declared default renders neither an empty line nor a
 * label over emptiness. A large declared set is paged rather than run
 * down one long column, so
 * the list stays a fixed height however many settings a deployment
 * declares. The judgement a write lands under is layered and
 * server-side (the tenant layer in that tenant's domain, the global
 * layer on the platform); this page renders the answer and nothing else.
 * Every read and write is an operation on the console operations bundle,
 * so the page builds no client and writes no query key. A sensitive
 * item's value arrives already masked by the server; the page renders the
 * masked text as-is and never masks a second time.
 *
 * A row's state comes from whether the selected layer holds its own row:
 * the view's `has_row` says so. The `written` text cannot: an explicitly
 * written empty string is an empty text too, so it would read as the
 * declared default. A held row is marked overridden and a missing one as
 * the declared default. Only a held row carries a remove control: a
 * default row has nothing to take back, so the control is not rendered at
 * all rather than offered as a switch that can only be a no-op.
 *
 * A sensitive item's text arrives masked, so `written` and `value` show
 * the mask; `has_row` still reports the row's existence. The page marks
 * such a row default or overridden like any other and renders the remove
 * control only where there is a row to remove; the key cell carries the
 * sensitive marker that explains the masked text.
 *
 * Editing an item fills its draft (the table's value cell is the draft
 * input); the sticky save bar writes every changed item and stays disabled
 * until something changed. The batch is one write attempt in one feedback
 * scope: the save bar issues every item's PUT through `mutateAsync`, waits
 * for all of them with `Promise.allSettled`, and reports one result under
 * the table. Every landed item clears its draft; a partial landing reports
 * the first failure in submission order, keeps every failed item's draft,
 * row marker and pending-save count, and pages the table to the page
 * holding that first failed item. The row marker is the failed item's own
 * value field: it renders in error, names the row's error through its
 * helper text, and MUI wires `aria-invalid` and the helper text's id into
 * `aria-describedby`. The row's helper text is resolved from the same
 * failure code the region resolves, so the two never carry different
 * copy. A failure that settles in this scope also scrolls the region into
 * view -- the region sits under the table, which may be taller than the
 * viewport -- while a success leaves the page where it is; the scroll is
 * unanimated when the operator asks for reduced motion. Success is
 * reported only when nothing failed. The row's remove action shares the
 * same scope and names the removed setting. A refused write resolves
 * through the admin-ui whitelist (a value outside the item's declared
 * domain reads the invalid-value text), never a raw key. A refused read
 * settles into the permission placeholder (403, which replaces any content
 * on screen) or the retryable error placeholder (a first failure); a failed
 * refresh that kept its content shows the stale marker. The page's refresh
 * action resets the scope and the failed row's marker and re-reads the
 * selected namespace and the namespace navigation -- one request when the
 * "all namespaces" entry makes the two reads one key -- without touching
 * unsaved drafts or the table's page. The page opens with ui-kit's
 * PageHeader, so the heading order never skips.
 */

import { useEffect, useMemo, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import List from '@mui/material/List'
import ListItem from '@mui/material/ListItem'
import ListItemButton from '@mui/material/ListItemButton'
import ListItemText from '@mui/material/ListItemText'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  DataTable,
  ListSkeleton,
  PageHeader,
  SectionCard,
  primaryColumnMaxWidthSx,
  useWriteFeedback,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { AdminAsyncSection, isForbidden } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { useAdminUiErrorText } from './internal/error-text.js'
import { RefreshButton } from '@speed/ui-kit'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  pageRefreshRead,
  usePageRefresh,
} from '@speed/ui-kit'
import { WriteFeedbackRegion } from './internal/write-feedback.js'
import { settingKeyRest, useAdminOperations } from './operations.js'
import type { AdminSetting } from './operations.js'

/** The sentinel the "all namespaces" navigation entry selects. */
const ALL = ''

/** The table's default page size. */
const DEFAULT_ROWS_PER_PAGE = 10

/** A row's state as the answer lets the page tell it. */
type SettingState = 'default' | 'overridden'

/**
 * The state of one row: `default` when the selected layer holds no row and
 * the value falls back to the declaration, `overridden` when the layer wrote
 * one. `has_row` is the answer's own statement of that, independent of the
 * row's text — which is empty for a written empty string too, and is the
 * server's mask string for a sensitive item.
 */
function settingState(setting: AdminSetting): SettingState {
  return setting.has_row ? 'overridden' : 'default'
}

export function SettingsPage() {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const [selected, setSelected] = useState<string>(ALL)
  // The per-item drafts, keyed by the item's full key. An absent entry (or
  // one equal to the item's value) is unchanged.
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  // The table's page and page size.
  const [page, setPage] = useState(0)
  const [rowsPerPage, setRowsPerPage] = useState(DEFAULT_ROWS_PER_PAGE)
  // The page size as it stands when a batch settles. The settlement pages
  // to the first failed item, and a size captured when the save started
  // would compute a page under the current slice's sizing.
  const rowsPerPageRef = useRef(rowsPerPage)
  useEffect(() => {
    rowsPerPageRef.current = rowsPerPage
  }, [rowsPerPage])
  // The full key of the item the batch reported first, or null. It is the
  // row the inline marker belongs to; cleared when a new attempt starts and
  // when the namespace changes, so a marker never outlives the result it
  // belongs to.
  const [failedKey, setFailedKey] = useState<string | null>(null)
  // The settings table's feedback scope: the batch save and the row remove
  // actions share one slot and one region under the table.
  const feedback = useWriteFeedback()
  // The region may sit under a table taller than the viewport, so a settled
  // failure scrolls it into view (see the effect below).
  const feedbackRef = useRef<HTMLDivElement | null>(null)
  // The console's error-text resolver, the same one the region binds: the
  // failed row's hint and the region's message resolve the one failure code
  // through the same whitelist, so the two can never carry different copy.
  const resolveErrorText = useAdminUiErrorText()
  const failureCode =
    feedback.outcome !== null && feedback.outcome.kind === 'failure'
      ? feedback.outcome.code
      : null
  const failureText = failureCode === null ? null : resolveErrorText(failureCode)

  // The whole declared set, read for the namespace navigation (its
  // namespaces are the grouping the page offers).
  const allSettings = operations.useSettings(undefined)
  // The selected namespace's items (the whole set for the "all" entry).
  const settings = operations.useSettings(selected === ALL ? undefined : selected)
  const setSetting = operations.useSetSetting()
  const removeSetting = operations.useRemoveSetting()
  const confirm = usePendingConfirm()

  // The refresh action resets the scope and the failed row's marker and
  // re-reads both body reads; it keeps every unsaved draft and the table's
  // page. With "all namespaces" chosen the selected read and the navigation
  // read are one query, and pass one key so the re-read goes out once.
  const pageRefresh = usePageRefresh(
    [
      pageRefreshRead(
        selected === ALL ? 'settings:all' : `settings:${selected}`,
        settings,
      ),
      pageRefreshRead('settings:all', allSettings),
    ],
    () => {
      setFailedKey(null)
      feedback.reset()
    },
  )

  // A namespace change starts a fresh form: no draft belongs to a
  // namespace the operator has left, the list starts on its first page,
  // the failed row's marker belongs to the result of the namespace being
  // left, and the scope's result would otherwise read as describing the
  // new namespace, so it is cleared.
  useEffect(() => {
    setDrafts({})
    setPage(0)
    setFailedKey(null)
    feedback.reset()
  }, [selected, feedback.reset])

  // A failure that settled in this scope brings its region into view: the
  // region is pinned under the table by design, and a long declared set
  // pushes it past the viewport, leaving the operator with no visible
  // reason. Success never moves the page, and neither does a cleared slot.
  // `block: 'center'` keeps the region clear of the sticky save bar that
  // precedes it; reduced motion jumps instead of animating.
  useEffect(() => {
    if (feedback.outcome?.kind !== 'failure') {
      return
    }
    const region = feedbackRef.current
    if (region === null) {
      return
    }
    const reduceMotion =
      window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
    region.scrollIntoView?.({
      behavior: reduceMotion ? 'auto' : 'smooth',
      block: 'center',
    })
  }, [feedback.outcome])

  const namespaces = useMemo(() => {
    const set = new Set<string>()
    for (const setting of allSettings.data ?? []) {
      set.add(setting.namespace)
    }
    return [...set].sort((a, b) => a.localeCompare(b))
  }, [allSettings.data])

  const items = settings.data
  const pending = settings.isPending && items === undefined
  // The register branch of the async section: the table and the save bar
  // that writes it render only for a settled, non-empty, non-refused read.
  // A refusal replaces the content, so a cached payload left over from the
  // previous read must not leave the bar standing beside the placeholder.
  const registerItems =
    items !== undefined && items.length > 0 && !isForbidden(settings.error)
      ? items
      : undefined
  const changedItems = (settled: readonly AdminSetting[]): readonly AdminSetting[] =>
    settled.filter(
      (setting) =>
        drafts[setting.key] !== undefined && drafts[setting.key] !== setting.value,
    )
  // The rows a batch save would write. The save bar and its count live
  // beside the register rather than inside the clipping table section, so
  // both read the settled payload through one derivation.
  const changedRows =
    registerItems === undefined ? [] : changedItems(registerItems)

  const save = (settled: readonly AdminSetting[]) => {
    const changed = changedItems(settled)
    if (changed.length === 0) {
      return
    }
    // The marker describes this attempt's result, so it is dropped before
    // the new attempt starts and set again only if this one fails.
    setFailedKey(null)
    // One attempt for the whole batch: every PUT is issued before any
    // settles, and `allSettled` keeps the submission order, so the first
    // rejected entry is the first failure in that order rather than
    // whichever request happened to finish first.
    void feedback.run(async () => {
      const results = await Promise.allSettled(
        changed.map((setting) =>
          setSetting.mutateAsync({
            namespace: setting.namespace,
            key: settingKeyRest(setting.key, setting.namespace),
            value: drafts[setting.key] ?? '',
          }),
        ),
      )
      // A landed item's draft is cleared (the next edit starts empty); a
      // failed item keeps its draft, its row marker and its share of the
      // pending count, so it can be corrected and retried.
      const landed = changed.filter(
        (_setting, index) => results[index]?.status === 'fulfilled',
      )
      setDrafts((previous) => {
        const next = { ...previous }
        for (const setting of landed) {
          delete next[setting.key]
        }
        return next
      })
      const failedIndex = results.findIndex(
        (result) => result.status === 'rejected',
      )
      if (failedIndex === -1) {
        return t('settings.saved')
      }
      // The table moves to the page holding the first failed item, so the
      // retained draft that still needs attention is on screen.
      const failedSetting = changed[failedIndex]
      if (failedSetting !== undefined) {
        // The marker belongs to the first failed item -- the one the
        // region reports -- not to every item that failed behind it.
        setFailedKey(failedSetting.key)
        const rowIndex = settled.indexOf(failedSetting)
        setPage(Math.max(0, Math.floor(rowIndex / rowsPerPageRef.current)))
      }
      throw (results[failedIndex] as PromiseRejectedResult).reason
    })
  }

  const remove = (setting: AdminSetting) => {
    confirm.ask({
      title: t('settings.removeTitle'),
      message: t('settings.removeMessage', { key: setting.key }),
      confirmLabel: t('common.confirm'),
      run: () => {
        // The row removal shares the batch's scope and slot, so the marker
        // that belonged to the batch's result is dropped with it; a new
        // result must not be read as the old row's reason.
        setFailedKey(null)
        void feedback.run(async () => {
          await removeSetting.mutateAsync({
            namespace: setting.namespace,
            key: settingKeyRest(setting.key, setting.namespace),
          })
          return t('settings.removed', { key: setting.key })
        })
      },
    })
  }

  // The table's columns: the item's key (with its sensitive marker), its
  // declared type, its editable value and the row's remove control.
  const columns: DataTableColumn<AdminSetting>[] = [
    {
      id: 'key',
      header: t('settings.column.key'),
      cellWrap: 'nowrap',
      cellFullValue: (setting) => setting.key,
      // The key line is the page's primary identity: it keeps the shared
      // primary-width cap and truncates there, with the complete key on
      // the copy control. The declaration beneath it (the description,
      // then the declared default) is prose and must not inherit that
      // cap -- a real description runs to hundreds of characters, which
      // inside 160/200px would wrap into a very tall column of thin
      // text. The column therefore declares no `contentMaxWidth` (which
      // would bound the whole cell); the prose wraps at whatever width
      // the cell gets, while the key line alone carries the cap. The
      // declaration is the server's answer, so a row never shows another
      // row's description or default; a setting with no declared default
      // renders neither an empty line nor a label over emptiness.
      cell: (setting) => (
        <Box
          sx={{
            display: 'flex',
            flexDirection: 'column',
            rowGap: 0.25,
            minWidth: 0,
          }}
        >
          <Box
            sx={{ display: 'flex', alignItems: 'baseline', columnGap: 1, minWidth: 0 }}
          >
            <Typography
              variant="body2"
              noWrap
              sx={{ ...primaryColumnMaxWidthSx, minWidth: 0 }}
            >
              {setting.key}
            </Typography>
            {setting.sensitive && (
              <Typography variant="caption" color="text.secondary" sx={{ flexShrink: 0 }}>
                {t('settings.field.sensitive')}
              </Typography>
            )}
          </Box>
          {setting.description !== '' && (
            <Typography
              variant="caption"
              color="text.secondary"
              sx={{ whiteSpace: 'normal', wordBreak: 'break-word' }}
            >
              {setting.description}
            </Typography>
          )}
          {setting.default !== '' && (
            <Typography
              variant="caption"
              color="text.secondary"
              sx={{ whiteSpace: 'normal', wordBreak: 'break-word' }}
            >
              {t('settings.field.defaultValue', { value: setting.default })}
            </Typography>
          )}
        </Box>
      ),
    },
    {
      id: 'type',
      header: t('settings.column.type'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (setting) => (
        <Typography variant="body2" color="text.secondary">
          {setting.type}
        </Typography>
      ),
    },
    {
      id: 'value',
      header: t('settings.column.value'),
      cell: (setting) => {
        // The failed row's field carries the row marker: MUI renders the
        // helper text in the error color and wires the control's
        // `aria-invalid` and `aria-describedby` to it, which is the form
        // family's documented error contract rather than a second
        // renderer. Both the field and the region read their text from the
        // same resolved failure code.
        const failed = failedKey !== null && setting.key === failedKey
        return (
          <TextField
            size="small"
            fullWidth
            value={drafts[setting.key] ?? setting.value}
            onChange={(event) =>
              setDrafts((previous) => ({
                ...previous,
                [setting.key]: event.target.value,
              }))
            }
            error={failed}
            helperText={failed ? failureText ?? undefined : undefined}
            // The column header labels the cell; the key names the field for
            // the accessibility tree, so each row's input is distinguishable.
            slotProps={{ htmlInput: { 'aria-label': setting.key } }}
          />
        )
      },
    },
    {
      id: 'state',
      header: t('settings.column.state'),
      cellWrap: 'nowrap',
      cell: (setting) => (
        <Typography variant="body2" color="text.secondary">
          {t(`settings.state.${settingState(setting)}`)}
        </Typography>
      ),
    },
    {
      id: 'actions',
      header: t('settings.column.actions'),
      cellWrap: 'nowrap',
      // A declared default has no row to take back: the remove control is
      // left out rather than rendered as a switch that can never act.
      // `has_row` says so for a sensitive item too, whose masked text
      // reveals nothing about its row.
      cell: (setting) =>
        setting.has_row ? (
          <Button
            type="button"
            size="small"
            variant="text"
            color="error"
            aria-label={t('settings.removeAria', { key: setting.key })}
            disabled={feedback.pending}
            onClick={() => remove(setting)}
          >
            {t('settings.remove')}
          </Button>
        ) : null,
    },
  ]

  return (
    <Box>
      <PageHeader
        title={t('settings.title')}
        breadcrumbs={[{ label: t('shell.title') }, { label: t('settings.title') }]}
        actions={
          <RefreshButton
            // A refresh changes the set the batch is settling against, so it
            // is natively disabled while the scope has a write in flight;
            // while either body read is in flight it is marked busy.
            busy={pageRefresh.busy}
            writePending={feedback.pending}
            label={t('common.refresh')}
            onClick={pageRefresh.refresh}
          />
        }
        sx={{ mb: 2 }}
      />

      <Box
        sx={{
          display: 'flex',
          flexDirection: { xs: 'column', md: 'row' },
          gap: 3,
          // Below md the box is a column: the content area must stretch
          // across the cross axis, or it collapses to its min-content
          // width and the table scrolls inside a sliver. From md up the
          // row layout keeps both columns at their own width.
          alignItems: { xs: 'stretch', md: 'flex-start' },
        }}
      >
        <Box
          component="nav"
          aria-label={t('settings.nav.label')}
          sx={{
            width: { xs: '100%', md: '13rem' },
            flexShrink: 0,
          }}
        >
          {/* The secondary navigation is one of the page's content blocks:
              it takes the same SectionCard container surface as the
              register beside it, with the list running flush so its own
              row padding stays the only inset. */}
          <SectionCard flushContent>
            <List dense>
              <ListItem disablePadding>
                <ListItemButton
                  selected={selected === ALL}
                  disabled={feedback.pending}
                  onClick={() => setSelected(ALL)}
                >
                  <ListItemText primary={t('settings.nav.all')} />
                </ListItemButton>
              </ListItem>
              {namespaces.map((namespace) => (
                <ListItem key={namespace} disablePadding>
                  <ListItemButton
                    selected={selected === namespace}
                    disabled={feedback.pending}
                    onClick={() => setSelected(namespace)}
                  >
                    <ListItemText primary={namespace} />
                  </ListItemButton>
                </ListItem>
              ))}
            </List>
          </SectionCard>
        </Box>

        {/* The register fills the content area beside the namespace nav (a
            register takes the width it is given). Its table section is
            flush, so the section's own clipping rounds the table's
            corners; the sticky save bar therefore sits under the section
            rather than inside it, where the clipping would pin it to the
            card instead of the viewport. */}
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <SectionCard flushContent>
            <AdminAsyncSection
              pending={pending}
              payload={items}
              error={settings.error}
              refreshing={settings.isFetching}
              empty={items !== undefined && items.length === 0}
              headingLevel="h2"
              loading={
                <ListSkeleton
                  label={t('settings.loading')}
                  rows={[
                    <Skeleton key="a" variant="text" width="58%" />,
                    <Skeleton key="b" variant="text" width="44%" />,
                  ]}
                />
              }
              errorState={{
                title: t('settings.error.title'),
                description: t('settings.error.description'),
                retryLabel: t('settings.retry'),
                onRetry: () => void settings.refetch(),
              }}
              emptyState={{
                title: t('settings.empty.title'),
                description: t('settings.empty.description'),
              }}
            >
              {(settled) => {
                const pageRows = settled.slice(
                  page * rowsPerPage,
                  page * rowsPerPage + rowsPerPage,
                )
                return (
                  <DataTable
                    rows={pageRows}
                    columns={columns}
                    rowKey={(setting) => `${setting.namespace}.${setting.key}`}
                    // Each row's key cell stacks the identity and its
                    // declaration, so the row is top-aligned: the value
                    // input and the copy control sit beside the key line
                    // rather than dropping to the middle of a tall row.
                    rowVerticalAlign="top"
                    pagination={{
                      page,
                      rowsPerPage,
                      count: settled.length,
                      // Paging and sizing change the slice the batch's
                      // result is read against, so they are inert while it
                      // is in flight (the footer carries no disabled prop).
                      onPageChange: (value) => {
                        if (feedback.pending) return
                        setPage(value)
                      },
                      onRowsPerPageChange: (value) => {
                        if (feedback.pending) return
                        setRowsPerPage(value)
                        setPage(0)
                      },
                    }}
                  />
                )
              }}
            </AdminAsyncSection>
          </SectionCard>

          {registerItems !== undefined && (
            <>
              {/* The sticky save bar: one write per changed item. It
                  paints the content block's own surface rather than an
                  opaque palette color, so a glass theme keeps the bar on
                  the same material as the table; the backdrop filter keeps
                  the rows that scroll underneath from showing through
                  crisply. Outside a glass theme the container fill is the
                  opaque paper color, unchanged. */}
              <Box
                component="div"
                sx={{
                  position: 'sticky',
                  bottom: 0,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'flex-end',
                  gap: 2,
                  paddingY: 2,
                  paddingX: 3,
                  borderTop: 1,
                  borderColor: 'divider',
                  backgroundColor: (theme) => theme.surface.container.fill,
                  backdropFilter: (theme) =>
                    theme.surface.container.backdropFilter ?? 'none',
                  WebkitBackdropFilter: (theme) =>
                    theme.surface.container.backdropFilter ?? 'none',
                }}
              >
                {changedRows.length > 0 && (
                  <Typography variant="body2" color="text.secondary">
                    {t('settings.saveBar.pending', { count: changedRows.length })}
                  </Typography>
                )}
                <Button
                  type="button"
                  variant="contained"
                  loading={feedback.pending}
                  disabled={changedRows.length === 0 || feedback.pending}
                  onClick={() => save(registerItems)}
                >
                  {t('settings.saveBar.save')}
                </Button>
              </Box>

              <Box ref={feedbackRef}>
                <WriteFeedbackRegion outcome={feedback.outcome} />
              </Box>
            </>
          )}
        </Box>
      </Box>

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}
