/**
 * The organization surface's security and access page: a tenant's admission
 * policies rendered as switches.
 */

import { useCallback, useEffect, useId, useRef, useState } from 'react'
import type { ChangeEvent } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Chip from '@mui/material/Chip'
import Divider from '@mui/material/Divider'
import Skeleton from '@mui/material/Skeleton'
import Switch from '@mui/material/Switch'
import Typography from '@mui/material/Typography'
import {
  ActionButton,
  ListSkeleton,
  SectionCard,
  errorCodeOf,
  pageRefreshRead,
  useActivationGuard,
  usePageRefresh,
} from '@speed/ui-kit'
import { AdminAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { SettingsIcon } from './internal/icons.js'
import { useAdminUiErrorText } from './internal/error-text.js'
import { AdminPageHeader } from './internal/page-header.js'
import { useAdminHasFeature } from './internal/features.js'
import { useAdminSession } from './internal/session.js'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  useOrganizationAdminOperations,
  type AdminTenantSetting,
} from './organization-operations.js'

export interface OrganizationSecurityPageProps {
  /** The organization the page's reads and writes address. */
  readonly tenant: string
}

type RowFeedback =
  | { readonly kind: 'saved' }
  | { readonly kind: 'failure'; readonly text: string }

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

// Both admission policies effect at each member's next sign-in; mapping by key
// keeps the sentence localized instead of echoing the raw value.
function itemEffect({
  setting,
  t,
}: {
  readonly setting: AdminTenantSetting
  readonly t: (key: string) => string
}): string {
  if (
    setting.key.endsWith('.require-mfa') ||
    setting.key.endsWith('.require-sso')
  ) {
    return t('organization.security.effect.nextSignIn')
  }
  return setting.effect === 'immediate'
    ? t('organization.security.effect.immediate')
    : setting.effect
}

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

// Splits a full key at its last dot: the item segment carries no dot of its own.
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
  readonly blocked?: boolean
  readonly blockedReason?: string
  readonly onToggle: (next: boolean) => void
}

function SettingSwitchRow({
  setting,
  checked,
  saving,
  readOnly,
  feedback,
  blocked,
  blockedReason,
  onToggle,
}: SettingSwitchRowProps) {
  const { t } = useAdminUiTranslation()
  const platformLockedId = useId()
  const blockedReasonId = useId()
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
  const effect = itemEffect({ setting, t })
  // The switch saves on its own and stays focusable while busy or blocked: a
  // natively disabled control would drop the focus the activation gave it.
  const unavailable = setting.locked_by_platform || blocked === true
  const onChange = useActivationGuard(
    (event: ChangeEvent<HTMLInputElement>) => {
      onToggle(event.target.checked)
    },
    { busy: saving, unavailable },
  )
  const describedBy =
    [
      setting.locked_by_platform ? platformLockedId : undefined,
      blockedReason !== undefined ? blockedReasonId : undefined,
    ]
      .filter((value): value is string => value !== undefined)
      .join(' ') || undefined

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
            id={platformLockedId}
            variant="body2"
            color="warning.main"
            sx={{ mt: 1 }}
          >
            {t('organization.security.platformLocked')}
          </Typography>
        )}
        {blockedReason !== undefined && (
          <Typography
            id={blockedReasonId}
            variant="body2"
            color="warning.main"
            sx={{ mt: 1 }}
          >
            {blockedReason}
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
            slotProps={{
              input: {
                'aria-label': t('organization.security.switchAria', { title }),
                'aria-busy': saving ? 'true' : undefined,
                'aria-disabled':
                  saving || unavailable ? 'true' : undefined,
                'aria-describedby': describedBy,
              },
            }}
            onChange={onChange}
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

/**
 * OrganizationSecurityPage renders the tenant's admission policies as switches
 * in one section, each saving on its own. A platform-held policy and a
 * host-protected tenant are read-only.
 */
export function OrganizationSecurityPage({
  tenant,
}: OrganizationSecurityPageProps) {
  const { t } = useAdminUiTranslation()
  const resolveErrorText = useAdminUiErrorText()
  const operations = useOrganizationAdminOperations()
  const settings = operations.useTenantSettingsList(tenant)
  const write = operations.useTenantSettingsWrite()
  const confirm = usePendingConfirm()
  const session = useAdminSession()
  // The MFA refusal cannot be cleared by any retry: ending the session returns
  // the host's session gate to its sign-in surface.
  const relogin = useCallback(() => {
    void session?.logout().catch(() => undefined)
  }, [session])
  // Requiring SSO with no enabled provider locks every member out, so the switch
  // stays off until a provider is known. A policy already on is never blocked,
  // and a pending or failed provider read counts as not ready.
  const hasFeature = useAdminHasFeature()
  const ssoServed = hasFeature('authn.admin.sso')
  const ssoProviders = operations.useSsoProviders(tenant)
  const ssoProviderState: 'pending' | 'ready' | 'none' | 'unknown' =
    ssoProviders.isPending
      ? 'pending'
      : !ssoProviders.isSuccess
        ? 'unknown'
        : (ssoProviders.data ?? []).some((provider) => provider.enabled)
          ? 'ready'
          : 'none'

  const [optimistic, setOptimistic] = useState<Record<string, boolean>>({})
  // A single key is not enough: switches save independently and can overlap, so
  // a row stays busy while any write for it is in flight.
  const [savingKeys, setSavingKeys] = useState<ReadonlySet<string>>(
    () => new Set(),
  )
  const [feedback, setFeedback] = useState<Record<string, RowFeedback>>({})
  const savedTimers = useRef(new Map<string, ReturnType<typeof setTimeout>>())

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

  // The settled answer is the truth: an optimistic value it agrees with is dropped.
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
      // Any declared danger other than "critical" gets the single-step
      // confirmation, never treated as harmless.
      if (setting.danger === '') {
        void apply(setting, false)
        return
      }
      confirm.ask({
        title: t('organization.security.confirm.title', {
          title: itemTitle({ setting, t }),
        }),
        message: t('organization.security.confirm.message', {
          title: itemTitle({ setting, t }),
          impact: itemImpact({ setting, t }),
        }),
        confirmLabel: t('common.confirm'),
        doubleConfirm: setting.danger === 'critical',
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
  // An unnamed non-empty reason counts as the durable one: a refusal must stand
  // if the client and server drift, not invite a retry.
  const readOnly = data !== undefined && !data.writable
  const readOnlyReason: 'protected' | 'unavailable' =
    data?.read_only_reason === 'unavailable' ? 'unavailable' : 'protected'

  return (
    <Box>
      <AdminPageHeader
        title={t('organization.nav.settings')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('organization.nav.settings') },
        ]}
        sx={{ mb: 2 }}
      />

      {readOnly && (
        <Alert
          severity={readOnlyReason === 'unavailable' ? 'warning' : 'info'}
          variant="outlined"
          sx={{ mb: 2 }}
        >
          {/* Not a heading: it would follow the h1 without intervening levels. */}
          <Typography variant="subtitle2" component="p">
            {t(`organization.security.readOnly.${readOnlyReason}.title`)}
          </Typography>
          <Typography variant="body2">
            {t(`organization.security.readOnly.${readOnlyReason}.description`)}
          </Typography>
          {readOnlyReason === 'unavailable' && (
            <ActionButton
              size="small"
              sx={{ mt: 1 }}
              busy={refresh.busy}
              onClick={refresh.refresh}
            >
              {t('organization.security.retry')}
            </ActionButton>
          )}
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
        mfaRelogin={session === null ? undefined : relogin}
      >
        {(settled) => (
          <>
            <SectionCard
              title={t('organization.security.section.title')}
              icon={<SettingsIcon size={24} />}
              description={t('organization.security.section.description')}
            >
              {settled.settings
                .filter(
                  (setting) =>
                    ssoServed || !setting.key.endsWith('.require-sso'),
                )
                .map((setting, index) => {
                  const checked =
                    optimistic[setting.key] ??
                    setting.effective_value === 'true'
                  const readOnly =
                    !settled.writable ||
                    setting.tenant_access !== 'read-write'
                  const ssoBlocked =
                    setting.key.endsWith('.require-sso') &&
                    !checked &&
                    ssoProviderState !== 'ready'
                  const ssoBlockedReason =
                    !ssoBlocked
                      ? undefined
                      : ssoProviderState === 'none'
                        ? t('organization.security.sso.noProvider')
                        : ssoProviderState === 'unknown'
                          ? t(
                              'organization.security.sso.providersUnreadable',
                            )
                          : t('organization.security.sso.providersPending')
                return (
                  <Box key={setting.key}>
                    {index > 0 && <Divider />}
                    <SettingSwitchRow
                      setting={setting}
                      checked={checked}
                      saving={savingKeys.has(setting.key)}
                      readOnly={readOnly}
                      feedback={feedback[setting.key] ?? null}
                      blocked={ssoBlocked}
                      blockedReason={ssoBlockedReason}
                      onToggle={(next) => toggle(setting, next)}
                    />
                  </Box>
                )
              })}
            </SectionCard>
          </>
        )}
      </AdminAsyncSection>

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}
