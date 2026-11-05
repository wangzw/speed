/**
 * OrganizationSecurityPage: the organization surface's security and access
 * page.
 *
 * The tenant surface carries only the settings a tenant genuinely owns --
 * the admission policies that decide how its members sign in. The page
 * renders them as two switches in one "sign-in policies" section, never as
 * a key/value table: the declarations, their titles and descriptions come
 * from the server, and the page adds the status, the effect and the cost of
 * turning each policy off.
 *
 * A policy the platform holds on is read-only and drawn as an enabled,
 * disabled switch: the platform value OR the tenant value decides the
 * effective one, so a tenant can never be looser than the platform floor.
 * Turning a policy off asks through the danger dialog's two-step
 * confirmation; turning one on saves immediately. A switch saves on its own
 * and reports the result beside itself (a short "saved" note, or the
 * resolved refusal), so the page has no save bar and no batch.
 *
 * A tenant the host's settings write seam protects is read-only as a whole.
 * The page says so above the section and renders no switch at all; the
 * refresh action stays available because reading is still allowed.
 *
 * Every read and write goes through the organization operations bundle, so
 * the page builds no client and writes no query key.
 */

import { useEffect, useRef, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Chip from '@mui/material/Chip'
import Divider from '@mui/material/Divider'
import Skeleton from '@mui/material/Skeleton'
import Switch from '@mui/material/Switch'
import Typography from '@mui/material/Typography'
import {
  ListSkeleton,
  RefreshButton,
  SectionCard,
  errorCodeOf,
  pageRefreshRead,
  usePageRefresh,
} from '@speed/ui-kit'
import { AdminAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { useAdminUiErrorText } from './internal/error-text.js'
import { AdminPageHeader } from './internal/page-header.js'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  useOrganizationAdminOperations,
  type AdminTenantSetting,
} from './organization-operations.js'

export interface OrganizationSecurityPageProps {
  /** The organization the page's reads and writes address. */
  readonly tenant: string
}

/** What a row says about its own last write. */
type RowFeedback =
  | { readonly kind: 'saved' }
  | { readonly kind: 'failure'; readonly text: string }

/** The item's title, preferring the server's localized text. */
function itemTitle({
  setting,
  t,
}: {
  readonly setting: AdminTenantSetting
  readonly t: (key: string) => string
}): string {
  if (setting.title !== '') {
    return setting.title
  }
  if (setting.key.endsWith('.require-mfa')) {
    return t('organization.security.mfa.title')
  }
  if (setting.key.endsWith('.require-sso')) {
    return t('organization.security.sso.title')
  }
  return setting.key
}

/** The item's description, preferring the server's localized text. */
function itemDescription({
  setting,
  t,
}: {
  readonly setting: AdminTenantSetting
  readonly t: (key: string) => string
}): string {
  if (setting.description !== '') {
    return setting.description
  }
  if (setting.key.endsWith('.require-mfa')) {
    return t('organization.security.mfa.description')
  }
  if (setting.key.endsWith('.require-sso')) {
    return t('organization.security.sso.description')
  }
  return ''
}

/** The sentence this policy's own loss means when it is turned off. */
function itemImpact({
  setting,
  t,
}: {
  readonly setting: AdminTenantSetting
  readonly t: (key: string) => string
}): string {
  if (setting.key.endsWith('.require-mfa')) {
    return t('organization.security.mfa.impact')
  }
  if (setting.key.endsWith('.require-sso')) {
    return t('organization.security.sso.impact')
  }
  return t('organization.security.impact.generic')
}

/**
 * Splits the view's full key into the route's namespace and its own key
 * segment. The declaration joins the two with a dot, and the item segment
 * carries no dot of its own, so the last dot is the boundary.
 */
function settingTarget(fullKey: string): {
  readonly namespace: string
  readonly key: string
} {
  const boundary = fullKey.lastIndexOf('.')
  if (boundary <= 0 || boundary === fullKey.length - 1) {
    return { namespace: fullKey, key: '' }
  }
  return {
    namespace: fullKey.slice(0, boundary),
    key: fullKey.slice(boundary + 1),
  }
}

interface SettingSwitchRowProps {
  readonly setting: AdminTenantSetting
  readonly checked: boolean
  readonly saving: boolean
  readonly readOnly: boolean
  readonly feedback: RowFeedback | null
  readonly onToggle: (next: boolean) => void
}

/** One policy: identity, status, what it does and the switch that sets it. */
function SettingSwitchRow({
  setting,
  checked,
  saving,
  readOnly,
  feedback,
  onToggle,
}: SettingSwitchRowProps) {
  const { t } = useAdminUiTranslation()
  const title = itemTitle({ setting, t })
  const status = setting.locked_by_platform
    ? t('organization.security.status.platformEnforced')
    : checked
      ? t('organization.security.status.enabled')
      : t('organization.security.status.disabled')
  const statusColor = setting.locked_by_platform
    ? 'warning'
    : checked
      ? 'success'
      : 'default'
  const effect =
    setting.effect === 'immediate'
      ? t('organization.security.effect.immediate')
      : setting.effect

  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: { xs: 'column', sm: 'row' },
        alignItems: { xs: 'stretch', sm: 'flex-start' },
        justifyContent: 'space-between',
        columnGap: 3,
        rowGap: 2,
        py: 2.5,
      }}
    >
      <Box sx={{ minWidth: 0 }}>
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            flexWrap: 'wrap',
            columnGap: 1,
            rowGap: 0.5,
            mb: 0.5,
          }}
        >
          <Typography component="h3" variant="subtitle1">
            {title}
          </Typography>
          <Chip
            size="small"
            variant="outlined"
            color={statusColor}
            label={status}
          />
        </Box>
        <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
          {itemDescription({ setting, t })}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          {t('organization.security.field.line', {
            label: t('organization.security.field.effect'),
            value: effect,
          })}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          {t('organization.security.field.line', {
            label: t('organization.security.field.impact'),
            value: itemImpact({ setting, t }),
          })}
        </Typography>
        {setting.locked_by_platform && (
          <Typography
            variant="body2"
            color="warning.main"
            sx={{ mt: 1 }}
          >
            {t('organization.security.platformLocked')}
          </Typography>
        )}
      </Box>
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          columnGap: 1,
          minHeight: 32,
          flexShrink: 0,
        }}
      >
        {readOnly ? null : (
          <Switch
            checked={checked}
            disabled={saving || setting.locked_by_platform}
            slotProps={{
              input: {
                'aria-label': t('organization.security.switchAria', { title }),
                'aria-busy': saving ? 'true' : undefined,
              },
            }}
            onChange={(event) => onToggle(event.target.checked)}
          />
        )}
        {feedback?.kind === 'saved' && (
          <Typography
            role="status"
            variant="body2"
            color="success.main"
            sx={{ whiteSpace: 'nowrap' }}
          >
            {t('organization.security.saved')}
          </Typography>
        )}
        {feedback?.kind === 'failure' && (
          <Typography
            role="alert"
            variant="body2"
            color="error"
            sx={{ maxWidth: '24rem' }}
          >
            {feedback.text}
          </Typography>
        )}
      </Box>
    </Box>
  )
}

/** The organization surface's security and access page. */
export function OrganizationSecurityPage({
  tenant,
}: OrganizationSecurityPageProps) {
  const { t } = useAdminUiTranslation()
  const resolveErrorText = useAdminUiErrorText()
  const operations = useOrganizationAdminOperations()
  const settings = operations.useTenantSettingsList(tenant)
  const write = operations.useTenantSettingsWrite()
  const confirm = usePendingConfirm()

  // The switch value a write put on screen before the server answered. The
  // entry is dropped when the settled read reports the same value, or when
  // the write is refused.
  const [optimistic, setOptimistic] = useState<Record<string, boolean>>({})
  // Every key with a write in flight. A single key is not enough: each switch
  // saves on its own, so two writes can overlap, and the first one's row must
  // stay disabled and busy while the second is in flight rather than becoming
  // writable again and accepting a second, out-of-order write of its own key.
  const [savingKeys, setSavingKeys] = useState<ReadonlySet<string>>(
    () => new Set(),
  )
  const [feedback, setFeedback] = useState<Record<string, RowFeedback>>({})
  const savedTimers = useRef(new Map<string, ReturnType<typeof setTimeout>>())

  // The read is the page's only body read; the refresh re-reads it and
  // clears the results that described the previous answer.
  const refresh = usePageRefresh(
    [pageRefreshRead('tenant-security', settings)],
    () => {
      setOptimistic({})
      setSavingKeys(new Set())
      setFeedback({})
      confirm.cancel()
    },
  )

  const clearSavedTimer = (key: string): void => {
    const timer = savedTimers.current.get(key)
    if (timer !== undefined) {
      clearTimeout(timer)
      savedTimers.current.delete(key)
    }
  }

  useEffect(
    () => () => {
      for (const timer of savedTimers.current.values()) {
        clearTimeout(timer)
      }
      savedTimers.current.clear()
    },
    [],
  )

  // The settled answer is the truth: an optimistic value it now agrees with
  // leaves the page, so the switch follows the server without a second
  // render of the old value.
  useEffect(() => {
    const settled = settings.data
    if (settled === undefined) {
      return
    }
    setOptimistic((previous) => {
      const next = { ...previous }
      let changed = false
      for (const setting of settled.settings) {
        const serverValue = setting.effective_value === 'true'
        if (next[setting.key] === serverValue) {
          delete next[setting.key]
          changed = true
        }
      }
      return changed ? next : previous
    })
  }, [settings.data])

  const apply = async (
    setting: AdminTenantSetting,
    next: boolean,
  ): Promise<void> => {
    const { namespace, key } = settingTarget(setting.key)
    clearSavedTimer(setting.key)
    setFeedback((previous) => {
      const nextFeedback = { ...previous }
      delete nextFeedback[setting.key]
      return nextFeedback
    })
    setOptimistic((previous) => ({ ...previous, [setting.key]: next }))
    setSavingKeys((previous) => {
      const next = new Set(previous)
      next.add(setting.key)
      return next
    })
    try {
      await write.mutateAsync({
        tenant,
        namespace,
        key,
        value: next ? 'true' : 'false',
      })
      setFeedback((previous) => ({
        ...previous,
        [setting.key]: { kind: 'saved' },
      }))
      savedTimers.current.set(
        setting.key,
        setTimeout(() => {
          savedTimers.current.delete(setting.key)
          setFeedback((previous) => {
            if (previous[setting.key]?.kind !== 'saved') {
              return previous
            }
            const left = { ...previous }
            delete left[setting.key]
            return left
          })
        }, 3000),
      )
    } catch (error) {
      // The failure withdrew the optimistic value: the switch is back where
      // the settled read says it was, and the row names the refusal.
      setOptimistic((previous) => {
        const left = { ...previous }
        delete left[setting.key]
        return left
      })
      setFeedback((previous) => ({
        ...previous,
        [setting.key]: {
          kind: 'failure',
          text: resolveErrorText(errorCodeOf(error)),
        },
      }))
    } finally {
      setSavingKeys((previous) => {
        if (!previous.has(setting.key)) {
          return previous
        }
        const left = new Set(previous)
        left.delete(setting.key)
        return left
      })
    }
  }

  const toggle = (setting: AdminTenantSetting, next: boolean): void => {
    if (!next) {
      confirm.ask({
        title: t('organization.security.confirm.title', {
          title: itemTitle({ setting, t }),
        }),
        message: t('organization.security.confirm.message', {
          title: itemTitle({ setting, t }),
          impact: itemImpact({ setting, t }),
        }),
        confirmLabel: t('common.confirm'),
        doubleConfirm: true,
        run: () => {
          void apply(setting, false)
        },
      })
      return
    }
    void apply(setting, true)
  }

  const data = settings.data
  const pending = settings.isPending && data === undefined

  return (
    <Box>
      <AdminPageHeader
        title={t('organization.nav.settings')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('organization.nav.settings') },
        ]}
        actions={
          <RefreshButton
            busy={refresh.busy}
            writePending={savingKeys.size > 0}
            label={t('common.refresh')}
            onClick={refresh.refresh}
          />
        }
        sx={{ mb: 2 }}
      />

      {data !== undefined && !data.writable && (
        <Alert severity="info" variant="outlined" sx={{ mb: 2 }}>
          <Typography variant="subtitle2">
            {t('organization.security.readOnly.title')}
          </Typography>
          <Typography variant="body2">
            {t('organization.security.readOnly.description')}
          </Typography>
        </Alert>
      )}

      <AdminAsyncSection
        pending={pending}
        payload={data}
        error={settings.error}
        refreshing={settings.isFetching}
        empty={data !== undefined && data.settings.length === 0}
        headingLevel="h2"
        loading={
          <ListSkeleton
            label={t('organization.security.loading')}
            rows={[
              <Skeleton key="a" variant="text" width="44%" />,
              <Skeleton key="b" variant="text" width="64%" />,
            ]}
          />
        }
        errorState={{
          title: t('organization.security.error.title'),
          description: t('organization.security.error.description'),
          retryLabel: t('organization.security.retry'),
          onRetry: () => void settings.refetch(),
        }}
        emptyState={{
          title: t('organization.security.empty.title'),
          description: t('organization.security.empty.description'),
        }}
      >
        {(settled) => (
          <>
            <SectionCard
              title={t('organization.security.section.title')}
              description={t('organization.security.section.description')}
            >
              {settled.settings.map((setting, index) => {
                const checked =
                  optimistic[setting.key] ??
                  setting.effective_value === 'true'
                const readOnly =
                  !settled.writable ||
                  setting.tenant_access !== 'read-write'
                return (
                  <Box key={setting.key}>
                    {index > 0 && <Divider />}
                    <SettingSwitchRow
                      setting={setting}
                      checked={checked}
                      saving={savingKeys.has(setting.key)}
                      readOnly={readOnly}
                      feedback={feedback[setting.key] ?? null}
                      onToggle={(next) => toggle(setting, next)}
                    />
                  </Box>
                )
              })}
            </SectionCard>
            <Typography
              variant="body2"
              color="text.secondary"
              sx={{ mt: 2 }}
            >
              {t('organization.security.auditNote')}
            </Typography>
          </>
        )}
      </AdminAsyncSection>

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}
