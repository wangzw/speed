/**
 * SettingsPage: the settings read-write page of the console.
 *
 * The declared settings are grouped by namespace, and the page presents
 * them the way the layout design's settings archetype does: a secondary
 * navigation (narrower than the shell's own sidebar) listing the
 * namespaces, beside the settings themselves, with a sticky save bar at
 * the bottom. Choosing a namespace narrows the read to that namespace
 * (GET /settings/admin/settings/:namespace over the global layer); the
 * "all namespaces" entry reads the whole declared set. The namespaces in
 * the navigation come from that whole-set read, so a namespace shows up
 * as soon as any of its items is declared.
 *
 * The items render as a full-width table (key, type, current value, the
 * row's state and its remove control), through ui-kit's DataTable: a
 * large declared set is paged rather than run down one long column, so
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
 * holding that first failed item. Success is reported only when nothing
 * failed. The row's remove action shares the same scope and names the
 * removed setting. A refused write resolves through the console-ui
 * whitelist (a value outside the item's declared domain reads the
 * invalid-value text), never a raw key. A refused read settles into the
 * permission placeholder (403, which replaces any content on screen) or
 * the retryable error placeholder (a first failure); a failed refresh that
 * kept its content shows the stale marker. The page opens with ui-kit's
 * PageHeader, so the heading order never skips.
 */

import { useEffect, useMemo, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import List from '@mui/material/List'
import ListItem from '@mui/material/ListItem'
import ListItemButton from '@mui/material/ListItemButton'
import ListItemText from '@mui/material/ListItemText'
import Paper from '@mui/material/Paper'
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
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'
import { settingKeyRest, useConsoleOperations } from './operations.js'
import type { ConsoleSetting } from './operations.js'

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
function settingState(setting: ConsoleSetting): SettingState {
  return setting.has_row ? 'overridden' : 'default'
}

export function SettingsPage() {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const [selected, setSelected] = useState<string>(ALL)
  // The per-item drafts, keyed by the item's full key. An absent entry (or
  // one equal to the item's value) is unchanged.
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  // The table's page and page size.
  const [page, setPage] = useState(0)
  const [rowsPerPage, setRowsPerPage] = useState(DEFAULT_ROWS_PER_PAGE)
  // The settings table's feedback scope: the batch save and the row remove
  // actions share one slot and one region under the table.
  const feedback = useWriteFeedback()

  // The whole declared set, read for the namespace navigation (its
  // namespaces are the grouping the page offers).
  const allSettings = operations.useSettings(undefined)
  // The selected namespace's items (the whole set for the "all" entry).
  const settings = operations.useSettings(selected === ALL ? undefined : selected)
  const setSetting = operations.useSetSetting()
  const removeSetting = operations.useRemoveSetting()
  const confirm = usePendingConfirm()

  // A namespace change starts a fresh form: no draft belongs to a
  // namespace the operator has left, the list starts on its first page,
  // and the scope's result would otherwise read as describing the new
  // namespace, so it is cleared.
  useEffect(() => {
    setDrafts({})
    setPage(0)
    feedback.reset()
  }, [selected, feedback.reset])

  const namespaces = useMemo(() => {
    const set = new Set<string>()
    for (const setting of allSettings.data ?? []) {
      set.add(setting.namespace)
    }
    return [...set].sort((a, b) => a.localeCompare(b))
  }, [allSettings.data])

  const items = settings.data
  const pending = settings.isPending && items === undefined

  const changedItems = (settled: readonly ConsoleSetting[]): readonly ConsoleSetting[] =>
    settled.filter(
      (setting) =>
        drafts[setting.key] !== undefined && drafts[setting.key] !== setting.value,
    )

  const save = (settled: readonly ConsoleSetting[]) => {
    const changed = changedItems(settled)
    if (changed.length === 0) {
      return
    }
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
        const rowIndex = settled.indexOf(failedSetting)
        setPage(Math.max(0, Math.floor(rowIndex / rowsPerPage)))
      }
      throw (results[failedIndex] as PromiseRejectedResult).reason
    })
  }

  const remove = (setting: ConsoleSetting) => {
    confirm.ask({
      title: t('settings.removeTitle'),
      message: t('settings.removeMessage', { key: setting.key }),
      confirmLabel: t('common.confirm'),
      run: () => {
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
  const columns: DataTableColumn<ConsoleSetting>[] = [
    {
      id: 'key',
      header: t('settings.column.key'),
      cellWrap: 'nowrap',
      cellFullValue: (setting) => setting.key,
      cell: (setting) => (
        <Box
          sx={{ display: 'flex', alignItems: 'baseline', columnGap: 1, minWidth: 0 }}
        >
          <Typography variant="body2">{setting.key}</Typography>
          {setting.sensitive && (
            <Typography variant="caption" color="text.secondary">
              {t('settings.field.sensitive')}
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
      cell: (setting) => (
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
          // The column header labels the cell; the key names the field for
          // the accessibility tree, so each row's input is distinguishable.
          slotProps={{ htmlInput: { 'aria-label': setting.key } }}
        />
      ),
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
          <Button
            type="button"
            variant="outlined"
            size="small"
            onClick={() => void settings.refetch()}
          >
            {t('common.refresh')}
          </Button>
        }
        sx={{ mb: 2 }}
      />

      <Box
        sx={{
          display: 'flex',
          flexDirection: { xs: 'column', md: 'row' },
          gap: 3,
          alignItems: 'flex-start',
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
          <Paper variant="outlined">
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
          </Paper>
        </Box>

        {/* The settings fill the content area beside the namespace nav (a
            register takes the width it is given). */}
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <ConsoleAsyncSection
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
              const changed = changedItems(settled)
              const pageRows = settled.slice(
                page * rowsPerPage,
                page * rowsPerPage + rowsPerPage,
              )
              return (
                <Box>
                  <DataTable
                    rows={pageRows}
                    columns={columns}
                    rowKey={(setting) => `${setting.namespace}.${setting.key}`}
                    pagination={{
                      page,
                      rowsPerPage,
                      count: settled.length,
                      onPageChange: setPage,
                      onRowsPerPageChange: (value) => {
                        setRowsPerPage(value)
                        setPage(0)
                      },
                    }}
                  />

                  {/* The sticky save bar: one write per changed item. */}
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
                      borderTop: 1,
                      borderColor: 'divider',
                      backgroundColor: 'background.default',
                    }}
                  >
                    {changed.length > 0 && (
                      <Typography variant="body2" color="text.secondary">
                        {t('settings.saveBar.pending', { count: changed.length })}
                      </Typography>
                    )}
                    <Button
                      type="button"
                      variant="contained"
                      loading={feedback.pending}
                      disabled={changed.length === 0 || feedback.pending}
                      onClick={() => save(settled)}
                    >
                      {t('settings.saveBar.save')}
                    </Button>
                  </Box>

                  <WriteFeedbackRegion outcome={feedback.outcome} />
                </Box>
              )
            }}
          </ConsoleAsyncSection>
        </Box>
      </Box>

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}
