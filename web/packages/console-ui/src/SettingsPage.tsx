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
 * Every read and write is an operation on the console operations bundle
 * -- the host binds the generated /settings/admin operations to it -- so
 * the page builds no client and writes no query key. A sensitive item's
 * value arrives already masked by the server; the page renders the
 * masked text as-is and never masks a second time. A failed list render
 * settles into the retryable error placeholder; a failed write renders
 * the answer's code text in one banner beside the offending control,
 * resolved from the console-ui namespace's whitelist (a value outside
 * the item's declared domain reads the invalid-value text), never a raw
 * key.
 *
 * The page renders a level-one heading and the async section's
 * placeholders render one level down, so the heading order never skips.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { AsyncSection, ListSkeleton, errorCodeOf } from '@speed/ui-kit'
import { InlineError } from './internal/inline-error.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { settingKeyRest, useConsoleOperations } from './operations.js'
import type { ConsoleSetting } from './operations.js'

/** One setting row: its read-only fields and its value/remove controls. */
function SettingRow({ setting }: { readonly setting: ConsoleSetting }) {
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
        py: 1.5,
        display: 'flex',
        flexDirection: 'column',
        rowGap: 1,
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'baseline', columnGap: 2 }}>
        <Typography variant="body1" sx={{ fontWeight: 500, minWidth: 0 }}>
          {setting.key}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          {setting.type}
        </Typography>
        {setting.sensitive && (
          <Typography variant="body2" color="text.secondary">
            {t('settings.sensitive')}
          </Typography>
        )}
      </Box>
      <Typography variant="body2" color="text.secondary">
        {setting.value}
      </Typography>
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
            setSetting.mutate({
              namespace: setting.namespace,
              key: keyRest,
              value,
            })
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
      </Box>
      <Box>
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

  const rows = settings.data
  const pending = settings.isPending && rows === undefined

  return (
    <Box>
      <Typography variant="h1" sx={{ fontSize: '1.75rem', mb: 2 }}>
        {t('settings.title')}
      </Typography>

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
          <Box>
            {settled.map((setting, index) => (
              <Box
                key={`${setting.namespace}.${setting.key}`}
                sx={
                  index > 0
                    ? { borderTop: '1px solid', borderColor: 'divider' }
                    : undefined
                }
              >
                <SettingRow setting={setting} />
              </Box>
            ))}
          </Box>
        )}
      </AsyncSection>
    </Box>
  )
}
