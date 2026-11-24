/**
 * SettingsPage is the console's settings read-write page over the global
 * layer: a namespace navigation beside a paged table of the declared settings
 * and a sticky save bar that writes every changed item in one batch.
 */

import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import List from '@mui/material/List'
import ListItem from '@mui/material/ListItem'
import ListItemButton from '@mui/material/ListItemButton'
import ListItemText from '@mui/material/ListItemText'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  ActionButton,
  DataTable,
  ListSkeleton,
  SectionCard,
  primaryColumnMaxWidthSx,
  useActivationGuard,
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
import { AdminPageHeader } from './internal/page-header.js'
import type {
  AdminOperations,
  AdminRemoveSettingInput,
  AdminSetSettingInput,
  AdminSetting,
} from './operations.js'
import type { UseMutationResult, UseQueryResult } from '@tanstack/react-query'

const ALL = ''

const DEFAULT_ROWS_PER_PAGE = 10

type SettingState = 'default' | 'overridden'

// State comes from `has_row`, not the text: a written empty string is also
// empty, and a sensitive item shows the mask.
function settingState(setting: AdminSetting): SettingState {
  return setting.has_row ? 'overridden' : 'default'
}

function removalLandingKey(
  settled: readonly AdminSetting[],
  key: string,
): string | null {
  const index = settled.findIndex((setting) => setting.key === key)
  if (index === -1) {
    return null
  }
  for (let next = index + 1; next < settled.length; next += 1) {
    const candidate = settled[next]
    if (candidate !== undefined && candidate.has_row) {
      return candidate.key
    }
  }
  for (let previous = index - 1; previous >= 0; previous -= 1) {
    const candidate = settled[previous]
    if (candidate !== undefined && candidate.has_row) {
      return candidate.key
    }
  }
  return null
}

type SettingsRow = AdminSetting

interface SettingsOperations {
  readonly useList: () => UseQueryResult<readonly SettingsRow[], unknown>
  readonly useNamespace: (
    namespace: string | undefined,
  ) => UseQueryResult<readonly SettingsRow[], unknown>
  readonly useSetSetting: () => UseMutationResult<
    void,
    unknown,
    AdminSetSettingInput
  >
  readonly useRemoveSetting: () => UseMutationResult<
    void,
    unknown,
    AdminRemoveSettingInput
  >
}

function usePlatformSettingsOperations(
  operations: AdminOperations,
): SettingsOperations {
  return {
    useList: () => operations.useSettings(undefined),
    useNamespace: (namespace) => operations.useSettings(namespace),
    useSetSetting: () => operations.useSetSetting(),
    useRemoveSetting: () => operations.useRemoveSetting(),
  }
}

/**
 * SettingsPage renders the platform surface's settings over the global layer.
 * The organization surface mounts `OrganizationSecurityPage` instead.
 */
export function SettingsPage() {
  const operations = useAdminOperations()
  const data = usePlatformSettingsOperations(operations)
  return <SettingsPageBody operations={data} />
}

function SettingsPageBody({
  operations,
}: {
  readonly operations: SettingsOperations
}) {
  const { t } = useAdminUiTranslation()
  const [selected, setSelected] = useState<string>(ALL)
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const [page, setPage] = useState(0)
  const [rowsPerPage, setRowsPerPage] = useState(DEFAULT_ROWS_PER_PAGE)
  // The settlement pages by the current size, not one captured when the save
  // started.
  const rowsPerPageRef = useRef(rowsPerPage)
  useEffect(() => {
    rowsPerPageRef.current = rowsPerPage
  }, [rowsPerPage])
  const [failedKey, setFailedKey] = useState<string | null>(null)
  const feedback = useWriteFeedback()
  const feedbackRef = useRef<HTMLDivElement | null>(null)
  // The failed row's hint and the region resolve the one code through the same
  // whitelist, so they never carry different copy.
  const resolveErrorText = useAdminUiErrorText()
  const failureCode =
    feedback.outcome !== null && feedback.outcome.kind === 'failure'
      ? feedback.outcome.code
      : null
  const failureText = failureCode === null ? null : resolveErrorText(failureCode)

  const allSettings = operations.useList()
  const settings = operations.useNamespace(selected === ALL ? undefined : selected)
  const setSetting = operations.useSetSetting()
  const removeSetting = operations.useRemoveSetting()
  const confirm = usePendingConfirm()
  // Row removals share the batch's scope; this names the row whose own removal
  // is in flight.
  const [removingKey, setRemovingKey] = useState<string | null>(null)
  const removeButtons = useRef(new Map<string, HTMLButtonElement>())
  const tableContainer = useRef<HTMLDivElement | null>(null)
  const pendingRemoval = useRef<{
    readonly key: string
    readonly fallbackKey: string | null
  } | null>(null)

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

  useEffect(() => {
    setDrafts({})
    setPage(0)
    setFailedKey(null)
    feedback.reset()
  }, [selected, feedback.reset])

  // A settled failure scrolls its region into view: block 'center' clears the
  // sticky save bar, and reduced motion jumps instead of animating.
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

  // A landed removal takes its row's control away: focus moves to the
  // replacement control, or the table container, in the same commit.
  useLayoutEffect(() => {
    const pending = pendingRemoval.current
    if (pending === null || removeButtons.current.has(pending.key)) {
      return
    }
    pendingRemoval.current = null
    const active = document.activeElement
    if (active !== null && active !== document.body) {
      return
    }
    const fallback =
      pending.fallbackKey === null
        ? undefined
        : removeButtons.current.get(pending.fallbackKey)
    const target = fallback ?? tableContainer.current
    target?.focus()
  }, [settings.data])

  const namespaces = useMemo(() => {
    const set = new Set<string>()
    for (const setting of allSettings.data ?? []) {
      set.add(setting.namespace)
    }
    return [...set].sort((a, b) => a.localeCompare(b))
  }, [allSettings.data])

  const items = settings.data
  const pending = settings.isPending && items === undefined
  // A refusal replaces the content, so a cached payload must not leave the save
  // bar standing beside the placeholder.
  const registerItems =
    items !== undefined && items.length > 0 && !isForbidden(settings.error)
      ? items
      : undefined
  const changedItems = (settled: readonly SettingsRow[]): readonly SettingsRow[] =>
    settled.filter(
      (setting) =>
        drafts[setting.key] !== undefined && drafts[setting.key] !== setting.value,
    )
  const changedRows =
    registerItems === undefined ? [] : changedItems(registerItems)
  // The slice is taken here so a removal's landing place is chosen among the
  // controls rendered with it.
  const pageRows =
    registerItems === undefined
      ? []
      : registerItems.slice(
          page * rowsPerPage,
          page * rowsPerPage + rowsPerPage,
        )
  // While a write is in flight the namespace buttons stay focusable but inert.
  const navigate = useActivationGuard(
    (namespace: string) => setSelected(namespace),
    { busy: false, unavailable: feedback.pending },
  )

  const save = (settled: readonly SettingsRow[]) => {
    const changed = changedItems(settled)
    if (changed.length === 0) {
      return
    }
    setFailedKey(null)
    // One attempt for the whole batch: allSettled keeps submission order, so the
    // first rejected entry is the first failure.
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
      const failedSetting = changed[failedIndex]
      if (failedSetting !== undefined) {
        setFailedKey(failedSetting.key)
        const rowIndex = settled.indexOf(failedSetting)
        setPage(Math.max(0, Math.floor(rowIndex / rowsPerPageRef.current)))
      }
      throw (results[failedIndex] as PromiseRejectedResult).reason
    })
  }

  const remove = (setting: SettingsRow) => {
    confirm.ask({
      title: t('settings.removeTitle'),
      message: t('settings.removeMessage', { key: setting.key }),
      confirmLabel: t('common.confirm'),
      run: () => {
        setFailedKey(null)
        pendingRemoval.current = {
          key: setting.key,
          fallbackKey: removalLandingKey(pageRows, setting.key),
        }
        setRemovingKey(setting.key)
        void feedback.run(async () => {
          try {
            await removeSetting.mutateAsync({
              namespace: setting.namespace,
              key: settingKeyRest(setting.key, setting.namespace),
            })
          } catch (error) {
            pendingRemoval.current = null
            throw error
          } finally {
            setRemovingKey(null)
          }
          return t('settings.removed', { key: setting.key })
        })
      },
    })
  }

  const columns: DataTableColumn<SettingsRow>[] = [
    {
      id: 'key',
      header: t('settings.column.key'),
      cellWrap: 'nowrap',
      cellFullValue: (setting) => setting.key,
      // The key line keeps the shared cap; the declaration is prose and wraps,
      // so the column sets no contentMaxWidth, which would bound the whole cell.
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
        // MUI's helper text wires aria-invalid and aria-describedby, the form
        // family's error contract.
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
      align: 'right',
      header: t('settings.column.actions'),
      cellWrap: 'nowrap',
      // A declared default has no row to take back, so no remove control.
      cell: (setting) =>
        setting.has_row ? (
          <ActionButton
            ref={(element) => {
              if (element === null) {
                removeButtons.current.delete(setting.key)
              } else {
                removeButtons.current.set(setting.key, element)
              }
            }}
            type="button"
            size="small"
            tone="danger"
            aria-label={t('settings.removeAria', { key: setting.key })}
            busy={removingKey === setting.key}
            unavailable={removingKey !== setting.key && feedback.pending}
            onClick={() => remove(setting)}
          >
            {t('settings.remove')}
          </ActionButton>
        ) : null,
    },
  ]

  return (
    <Box>
      <AdminPageHeader
        title={t('settings.title')}
        breadcrumbs={[{ label: t('shell.title') }, { label: t('settings.title') }]}
        actions={
          <RefreshButton
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
          // Below md the column must stretch, or the table scrolls inside a
          // min-content sliver.
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
          <SectionCard flushContent>
            <List dense>
              <ListItem disablePadding>
                <ListItemButton
                  selected={selected === ALL}
                  aria-disabled={feedback.pending || undefined}
                  onClick={() => navigate(ALL)}
                >
                  <ListItemText primary={t('settings.nav.all')} />
                </ListItemButton>
              </ListItem>
              {namespaces.map((namespace) => (
                <ListItem key={namespace} disablePadding>
                  <ListItemButton
                    selected={selected === namespace}
                    aria-disabled={feedback.pending || undefined}
                    onClick={() => navigate(namespace)}
                  >
                    <ListItemText primary={namespace} />
                  </ListItemButton>
                </ListItem>
              ))}
            </List>
          </SectionCard>
        </Box>

        {/* The sticky save bar sits under the section, not inside it, so the
            section's clipping does not pin it to the card. */}
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
              {(settled) => (
                // Programmatically focusable, not tabbable; a removal with no
                // target lands focus here.
                <Box
                  ref={tableContainer}
                  tabIndex={-1}
                  role="group"
                  aria-label={t('settings.tableLabel')}
                >
                  <DataTable
                    rows={pageRows}
                    columns={columns}
                    rowKey={(setting) => `${setting.namespace}.${setting.key}`}
                    rowVerticalAlign="top"
                    pagination={{
                      page,
                      rowsPerPage,
                      count: settled.length,
                      // Paging and sizing change the slice the batch's
                      // result is read against. The handler checks stay as
                      // the layer under the pager's unavailable state.
                      unavailable: feedback.pending,
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
                </Box>
              )}
            </AdminAsyncSection>
          </SectionCard>

          {registerItems !== undefined && (
            <>
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
                <ActionButton
                  type="button"
                  emphasis="primary"
                  // A row removal shares this scope but must not borrow the save
                  // action's progress.
                  busy={feedback.pending && removingKey === null}
                  unavailable={feedback.pending || changedRows.length === 0}
                  unavailableReason={
                    feedback.pending
                      ? undefined
                      : t('settings.saveBar.unchanged')
                  }
                  onClick={() => save(registerItems)}
                >
                  {t('settings.saveBar.save')}
                </ActionButton>
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
