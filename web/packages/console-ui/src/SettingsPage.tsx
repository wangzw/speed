/**
 * SettingsPage: the settings read-write page of the console.
 *
 * The declared settings are grouped by namespace, and the page presents
 * them the way the layout design's settings archetype does: a secondary
 * navigation (narrower than the shell's own sidebar) listing the
 * namespaces, beside a width-limited form, with a sticky save bar at the
 * bottom. Choosing a namespace narrows the read to that namespace
 * (GET /settings/admin/settings/:namespace over the global layer); the
 * "all namespaces" entry reads the whole declared set. The namespaces in
 * the navigation come from that whole-set read, so a namespace shows up
 * as soon as any of its items is declared.
 *
 * The judgement a write lands under is layered and server-side (the
 * tenant layer in that tenant's domain, the global layer on the
 * platform); this page renders the answer and nothing else. Every read
 * and write is an operation on the console operations bundle, so the page
 * builds no client and writes no query key. A sensitive item's value
 * arrives already masked by the server; the page renders the masked text
 * as-is and never masks a second time.
 *
 * Editing an item fills its draft; the sticky save bar writes every
 * changed item (one PUT each) and stays disabled until something changed.
 * A refused write renders the answer's code text in one banner, resolved
 * from the console-ui whitelist (a value outside the item's declared
 * domain reads the invalid-value text), never a raw key; a landed write
 * clears that item's draft so the next edit starts empty. A refused read
 * settles into the permission placeholder (403) or the retryable error
 * placeholder (any other failure). The page opens with ui-kit's
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
import { ListSkeleton, PageHeader, errorCodeOf } from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { InlineError } from './internal/inline-error.js'
import { FormWidth } from './internal/layout.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { settingKeyRest, useConsoleOperations } from './operations.js'
import type { ConsoleSetting } from './operations.js'

/** The sentinel the "all namespaces" navigation entry selects. */
const ALL = ''

export function SettingsPage() {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const [selected, setSelected] = useState<string>(ALL)
  // The per-item drafts, keyed by the item's full key. An absent entry (or
  // one equal to the item's value) is unchanged.
  const [drafts, setDrafts] = useState<Record<string, string>>({})

  // The whole declared set, read for the namespace navigation (its
  // namespaces are the grouping the page offers).
  const allSettings = operations.useSettings(undefined)
  // The selected namespace's items (the whole set for the "all" entry).
  const settings = operations.useSettings(selected === ALL ? undefined : selected)
  const setSetting = operations.useSetSetting()
  const removeSetting = operations.useRemoveSetting()
  const confirm = usePendingConfirm()

  // A namespace change starts a fresh form: no draft belongs to a
  // namespace the operator has left.
  useEffect(() => {
    setDrafts({})
  }, [selected])

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
    for (const setting of changedItems(settled)) {
      const value = drafts[setting.key] ?? ''
      setSetting.mutate(
        {
          namespace: setting.namespace,
          key: settingKeyRest(setting.key, setting.namespace),
          value,
        },
        {
          // A landed write clears that item's draft; a refused one keeps it
          // so the value can be corrected and retried.
          onSuccess: () =>
            setDrafts((previous) => {
              const next = { ...previous }
              delete next[setting.key]
              return next
            }),
        },
      )
    }
  }

  const remove = (setting: ConsoleSetting) => {
    confirm.ask({
      title: t('settings.removeTitle'),
      message: t('settings.removeMessage', { key: setting.key }),
      confirmLabel: t('common.confirm'),
      run: () =>
        removeSetting.mutate({
          namespace: setting.namespace,
          key: settingKeyRest(setting.key, setting.namespace),
        }),
    })
  }

  const writeError = setSetting.isError
    ? errorCodeOf(setSetting.error)
    : removeSetting.isError
      ? errorCodeOf(removeSetting.error)
      : null

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
                  onClick={() => setSelected(ALL)}
                >
                  <ListItemText primary={t('settings.nav.all')} />
                </ListItemButton>
              </ListItem>
              {namespaces.map((namespace) => (
                <ListItem key={namespace} disablePadding>
                  <ListItemButton
                    selected={selected === namespace}
                    onClick={() => setSelected(namespace)}
                  >
                    <ListItemText primary={namespace} />
                  </ListItemButton>
                </ListItem>
              ))}
            </List>
          </Paper>
        </Box>

        <FormWidth>
          <ConsoleAsyncSection
            pending={pending}
            payload={items}
            error={settings.error}
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
              return (
                <Box>
                  <Box
                    sx={{
                      display: 'flex',
                      flexDirection: 'column',
                      gap: 2,
                      mb: 3,
                    }}
                  >
                    {settled.map((setting) => (
                      <Box key={setting.key}>
                        <TextField
                          fullWidth
                          size="small"
                          value={drafts[setting.key] ?? setting.value}
                          onChange={(event) =>
                            setDrafts((previous) => ({
                              ...previous,
                              [setting.key]: event.target.value,
                            }))
                          }
                          label={setting.key}
                          helperText={`${t('settings.field.currentValue')}: ${setting.value}${
                            setting.sensitive
                              ? ` · ${t('settings.field.sensitive')}`
                              : ''
                          }`}
                        />
                        <Box sx={{ mt: 0.5 }}>
                          <Button
                            type="button"
                            size="small"
                            variant="text"
                            color="error"
                            disabled={removeSetting.isPending}
                            onClick={() => remove(setting)}
                          >
                            {t('settings.remove')}
                          </Button>
                        </Box>
                      </Box>
                    ))}
                  </Box>

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
                      disabled={changed.length === 0 || setSetting.isPending}
                      onClick={() => save(settled)}
                    >
                      {t('settings.saveBar.save')}
                    </Button>
                  </Box>

                  <InlineError code={writeError} />
                </Box>
              )
            }}
          </ConsoleAsyncSection>
        </FormWidth>
      </Box>

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}
