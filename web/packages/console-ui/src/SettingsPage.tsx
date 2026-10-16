/**
 * SettingsPage: the settings read-write page of the console.
 *
 * It reads the declared settings (all namespaces, or one when the
 * namespace filter is applied), writes one item's value and removes one
 * item's override. The judgement a write lands under is layered and
 * server-side (the tenant layer in that tenant's domain, the global
 * layer on the platform) -- this page renders the answer and nothing
 * else.
 *
 * The items render as a table (key, type, current value and the row's
 * write controls), through ui-kit's DataTable; the columns shed the
 * least important first as the viewport narrows. Every read and write is
 * an operation on the console operations bundle -- the host binds the
 * generated /settings/admin operations to it -- so the page builds no
 * client and writes no query key. A sensitive item's value arrives
 * already masked by the server; the page renders the masked text as-is
 * and never masks a second time. A failed list render settles into the
 * retryable error placeholder; a failed write renders the answer's code
 * text in one banner beside the offending control, resolved from the
 * console-ui namespace's whitelist (a value outside the item's declared
 * domain reads the invalid-value text), never a raw key. A save that
 * lands clears the row's draft input so the next edit starts empty; a
 * refused write keeps the draft so it can be corrected and retried.
 *
 * The page opens with ui-kit's PageHeader: the page title as the level-one
 * heading, a breadcrumb trail from the console to this page, and a
 * refresh action that re-reads the settings. The async section's
 * placeholders render one level down, so the heading order never skips.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  AsyncSection,
  DataTable,
  ListSkeleton,
  PageHeader,
  errorCodeOf,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { InlineError } from './internal/inline-error.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { settingKeyRest, useConsoleOperations } from './operations.js'
import type { ConsoleSetting } from './operations.js'

/**
 * One row's write controls: the draft value input, save and remove. The
 * draft lives here, per row; a landed save clears it.
 */
function SettingRowActions({ setting }: { readonly setting: ConsoleSetting }) {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const setSetting = operations.useSetSetting()
  const removeSetting = operations.useRemoveSetting()
  const [value, setValue] = useState('')

  // The route addresses an item by the namespace and the rest of the key
  // under it (the full key is <namespace>.<key>); the view carries the
  // full key, so the rest is the full key with the namespace prefix
  // removed.
  const keyRest = settingKeyRest(setting.key, setting.namespace)

  const remove = () => {
    removeSetting.mutate({
      namespace: setting.namespace,
      key: keyRest,
    })
  }

  return (
    <Box
      sx={{
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'flex-end',
        columnGap: 2,
        rowGap: 1,
      }}
    >
      <TextField
        size="small"
        value={value}
        onChange={(event) => setValue(event.target.value)}
        label={t('settings.value.label')}
      />
      <Button
        type="button"
        size="small"
        variant="outlined"
        disabled={setSetting.isPending}
        onClick={() => {
          setSetting.mutate(
            {
              namespace: setting.namespace,
              key: keyRest,
              value,
            },
            // A landed write clears the draft so the next edit starts
            // empty; a refused write keeps it for a retry.
            { onSuccess: () => setValue('') },
          )
        }}
      >
        {t('settings.value.save')}
      </Button>
      <Button
        type="button"
        size="small"
        variant="outlined"
        color="error"
        disabled={removeSetting.isPending}
        onClick={remove}
      >
        {t('settings.remove')}
      </Button>
      <Box sx={{ flexBasis: '100%' }}>
        <InlineError
          code={
            setSetting.isError
              ? errorCodeOf(setSetting.error)
              : removeSetting.isError
                ? errorCodeOf(removeSetting.error)
                : null
          }
        />
      </Box>
    </Box>
  )
}

export function SettingsPage() {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  // The applied namespace filter: an empty selection reads all namespaces.
  const [namespace, setNamespace] = useState('')
  const [draft, setDraft] = useState('')
  const settings = operations.useSettings(
    namespace.trim() === '' ? undefined : namespace.trim(),
  )

  // The columns the table renders: a row's read-only fields and its write
  // controls. Type and value carry a priority, so a narrow viewport sheds
  // them before the key and the controls.
  const columns: DataTableColumn<ConsoleSetting>[] = [
    {
      id: 'key',
      header: t('settings.column.key'),
      cell: (setting) => (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'baseline',
            columnGap: 1,
            minWidth: 0,
          }}
        >
          <Typography variant="body2" sx={{ wordBreak: 'break-all' }}>
            {setting.key}
          </Typography>
          {setting.sensitive && (
            <Typography variant="caption" color="text.secondary">
              {t('settings.sensitive')}
            </Typography>
          )}
        </Box>
      ),
    },
    {
      id: 'type',
      header: t('settings.column.type'),
      priority: 'low',
      cell: (setting) => (
        <Typography variant="body2" color="text.secondary">
          {setting.type}
        </Typography>
      ),
    },
    {
      id: 'value',
      header: t('settings.column.value'),
      priority: 'medium',
      cell: (setting) => (
        <Typography variant="body2" sx={{ wordBreak: 'break-all' }}>
          {setting.value}
        </Typography>
      ),
    },
    {
      id: 'actions',
      header: t('settings.column.actions'),
      cell: (setting) => <SettingRowActions setting={setting} />,
    },
  ]

  const rows = settings.data
  const pending = settings.isPending && rows === undefined

  return (
    <Box>
      <PageHeader
        title={t('settings.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('settings.title') },
        ]}
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
        component="section"
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
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          label={t('settings.namespace.label')}
          placeholder={t('settings.namespace.placeholder')}
        />
        <Button
          type="button"
          variant="contained"
          onClick={() => setNamespace(draft)}
        >
          {t('settings.namespace.apply')}
        </Button>
      </Box>

      <AsyncSection
        pending={pending}
        payload={rows}
        empty={rows !== undefined && rows.length === 0}
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
          <DataTable
            rows={settled}
            columns={columns}
            rowKey={(setting) => `${setting.namespace}.${setting.key}`}
          />
        )}
      </AsyncSection>
    </Box>
  )
}
