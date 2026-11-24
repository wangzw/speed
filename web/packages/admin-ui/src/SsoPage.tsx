/**
 * SsoPage administers one tenant's SSO providers and their domains: create,
 * edit, delete, refresh metadata, re-verify, claim, verify and remove. Reads
 * stay disabled until a tenant is chosen.
 */

import { useCallback, useEffect, useState } from 'react'
import Box from '@mui/material/Box'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  ActionButton,
  DataTable,
  EmptyState,
  ListSkeleton,
  OneTimeValuePanel,
  PRIMARY_COLUMN_MAX_WIDTH,
  SectionCard,
  useActivationGuard,
  useInertSelectMenu,
  useWriteFeedback,
} from '@speed/ui-kit'
import type {
  ActionButtonUnavailableExplanation,
  DataTableColumn,
  WriteFeedback,
} from '@speed/ui-kit'
import type { AuthnAdminProviderWrite } from '@speed/api-sdk'
import { AdminAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { SsoIcon } from './internal/icons.js'
import { useAdminUiErrorText } from './internal/error-text.js'
import { useAdminHasFeature } from './internal/features.js'
import { ADMIN_UI_NAMESPACE } from './resources.js'
import { useAdminSession } from './internal/session.js'
import { TenantScope, tenantFromLocation } from './internal/tenant-scope.js'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  failureTextOf,
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { useAdminOperations } from './operations.js'
import type { AdminSsoProvider } from './operations.js'
import { AdminPageHeader } from './internal/page-header.js'
import { RowActionMenu } from './internal/row-action-menu.js'

// One copy of the "choose a tenant first" explanation, shared with the disabled refresh.
const TENANT_HINT_ID = 'sso-tenant-hint'

// One copy of the config parse hint, shared with the submit control.
const CONFIG_FIELD_ID = 'sso-provider-config'
const CONFIG_HINT_ID = `${CONFIG_FIELD_ID}-helper-text`

type ProviderAction = 'refresh' | 'reverify' | 'delete'

type DomainAction = 'claim' | 'verify' | 'remove'

interface ClaimToken {
  readonly token: string
  readonly tenant: string
  readonly providerName: string
  readonly domain: string
}

/** Props of SsoPage: the tenant scope, optionally controlled by the host. */
export interface SsoPageProps {
  /**
   * A controlled tenant scope: when given, the page's reads and writes name
   * this tenant and the selector is not rendered. An empty string is a
   * controlled "no tenant".
   */
  readonly tenant?: string
  /**
   * The page's initial tenant when it is not controlled. Wins over the
   * tenant query parameter.
   */
  readonly defaultTenant?: string
  /** Reports each re-selection made through the page's own selector. */
  readonly onTenantChange?: (tenant: string) => void
}

/**
 * SsoPage administers one tenant's SSO providers, their metadata and their
 * declared domains.
 */
export function SsoPage({
  tenant: controlledTenant,
  defaultTenant,
  onTenantChange,
}: SsoPageProps = {}) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const session = useAdminSession()
  const hasFeature = useAdminHasFeature()
  // Without this capability the row keeps its other actions.
  const metadataRefresh = hasFeature('authn.admin.sso.metadata')
  const editProviderMenu = useInertSelectMenu()
  const domainProviderMenu = useInertSelectMenu()

  const [innerTenant, setInnerTenant] = useState(
    () => defaultTenant ?? tenantFromLocation(),
  )
  const tenant = controlledTenant ?? innerTenant
  const tenantControlled = controlledTenant !== undefined
  const [domainProvider, setDomainProvider] = useState('')
  const [domain, setDomain] = useState('')
  const [editProvider, setEditProvider] = useState('')
  const [claimToken, setClaimToken] = useState<ClaimToken | null>(null)
  const confirm = usePendingConfirm()

  const providers = operations.useSsoProviders(tenant)
  const deleteProvider = operations.useDeleteSsoProvider()
  const refreshMetadata = operations.useRefreshSsoMetadata()
  const reverifyDomains = operations.useReverifySsoDomains()
  const verifyDomain = operations.useVerifySsoDomain()
  const removeDomain = operations.useRemoveSsoDomain()
  const claimDomain = operations.useClaimSsoDomain()

  const listFeedback = useWriteFeedback()
  const formFeedback = useWriteFeedback()
  const domainsFeedback = useWriteFeedback()
  const listRegionRef = useFailureScroll(listFeedback.outcome)
  const formRegionRef = useFailureScroll(formFeedback.outcome)
  const domainsRegionRef = useFailureScroll(domainsFeedback.outcome)

  const [failedProvider, setFailedProvider] = useState<{
    readonly id: string
    readonly action: ProviderAction
  } | null>(null)
  const [busyDomainAction, setBusyDomainAction] = useState<{
    readonly action: DomainAction
    readonly domain?: string
  } | null>(null)

  // The row's inline marker resolves through the same resolver as the region.
  const resolveErrorText = useAdminUiErrorText()
  const listFailureText = failureTextOf(listFeedback.outcome, resolveErrorText)

  const anyPending =
    listFeedback.pending || formFeedback.pending || domainsFeedback.pending

  const resetPageFeedback = useCallback(() => {
    listFeedback.reset()
    formFeedback.reset()
    domainsFeedback.reset()
    setFailedProvider(null)
  }, [listFeedback.reset, formFeedback.reset, domainsFeedback.reset])
  // The one-time token panel is page state, not a scope result, and survives a
  // tenant change: it names the tenant it was claimed under.
  useEffect(() => {
    resetPageFeedback()
    setBusyDomainAction(null)
    setEditProvider('')
    setDomainProvider('')
  }, [tenant, resetPageFeedback])

  const changeTenant = (next: string) => {
    setInnerTenant(next)
    onTenantChange?.(next)
  }

  // The SSO refusal cannot be cleared locally: ending the session returns the
  // host's session gate to its sign-in surface, where the tenant's provider signs in.
  const relogin = useCallback(() => {
    void session?.logout().catch(() => undefined)
  }, [session])

  const selectEditProvider = (next: string) => {
    setEditProvider(next)
    formFeedback.reset()
  }

  const selectDomainProvider = (next: string) => {
    setDomainProvider(next)
    domainsFeedback.reset()
  }

  const runProviderAction = (
    provider: AdminSsoProvider,
    action: ProviderAction,
    attempt: () => Promise<string>,
  ) => {
    setFailedProvider(null)
    void listFeedback.run(async () => {
      try {
        return await attempt()
      } catch (error) {
        setFailedProvider({ id: provider.id, action })
        throw error
      }
    })
  }

  const runDomainAction = (
    action: DomainAction,
    target: string | undefined,
    attempt: () => Promise<string>,
  ) => {
    setBusyDomainAction(
      target === undefined ? { action } : { action, domain: target },
    )
    void domainsFeedback.run(async () => {
      try {
        return await attempt()
      } finally {
        setBusyDomainAction(null)
      }
    })
  }

  const requestRefreshMetadata = (provider: AdminSsoProvider) => {
    runProviderAction(provider, 'refresh', async () => {
      await refreshMetadata.mutateAsync({ id: provider.id, tenant })
      return t('sso.refreshMetadataSaved', { name: provider.name })
    })
  }

  const requestReverifyDomains = (provider: AdminSsoProvider) => {
    runProviderAction(provider, 'reverify', async () => {
      await reverifyDomains.mutateAsync({ id: provider.id, tenant })
      return t('sso.reverifyDomainsSaved', { name: provider.name })
    })
  }

  const requestDelete = (provider: AdminSsoProvider) => {
    confirm.ask({
      title: t('sso.deleteTitle'),
      message: t('sso.deleteMessage', { name: provider.name }),
      confirmLabel: t('common.confirm'),
      run: () => {
        runProviderAction(provider, 'delete', async () => {
          await deleteProvider.mutateAsync({ id: provider.id, tenant })
          return t('sso.deleted', { name: provider.name })
        })
      },
    })
  }

  const providerName = (id: string): string =>
    (providers.data ?? []).find((provider) => provider.id === id)?.name ?? id

  const requestClaimDomain = () => {
    const name = providerName(domainProvider)
    // Trim to match the module's stored value, so the record name is clean.
    const claimed = domain.trim()
    runDomainAction('claim', undefined, async () => {
      const answer = await claimDomain.mutateAsync({
        id: domainProvider,
        tenant,
        domain: claimed,
      })
      setClaimToken({
        token: answer.verify_token,
        tenant,
        providerName: name,
        domain: claimed,
      })
      return t('sso.domains.claimed', { name, domain: claimed })
    })
  }

  const requestVerifyDomain = (domainToVerify: string) => {
    const name = providerName(domainProvider)
    runDomainAction('verify', domainToVerify, async () => {
      await verifyDomain.mutateAsync({
        id: domainProvider,
        tenant,
        domain: domainToVerify,
      })
      return t('sso.domains.verified', { name, domain: domainToVerify })
    })
  }

  const requestRemoveDomain = (domainToRemove: string) => {
    const name = providerName(domainProvider)
    confirm.ask({
      title: t('sso.domains.removeTitle'),
      message: t('sso.domains.removeMessage', { domain: domainToRemove }),
      confirmLabel: t('common.confirm'),
      run: () => {
        runDomainAction('remove', domainToRemove, async () => {
          await removeDomain.mutateAsync({
            id: domainProvider,
            tenant,
            domain: domainToRemove,
          })
          return t('sso.domains.removed', { name, domain: domainToRemove })
        })
      },
    })
  }

  const columns: DataTableColumn<AdminSsoProvider>[] = [
    {
      id: 'name',
      header: t('sso.column.name'),
      cellWrap: 'nowrap',
      cellFullValue: (provider) => provider.name,
      // The cap counts the copy control beside the name.
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (provider) => (
        <Typography variant="body2" noWrap>
          {provider.name}
        </Typography>
      ),
    },
    {
      id: 'protocol',
      header: t('sso.column.protocol'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (provider) => provider.protocol,
    },
    {
      id: 'enabled',
      header: t('sso.column.enabled'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (provider) =>
        provider.enabled ? t('sso.enabledYes') : t('sso.enabledNo'),
    },
    {
      id: 'version',
      header: t('sso.column.version'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (provider) => String(provider.version),
    },
    {
      id: 'actions',
      align: 'right',
      header: t('sso.column.actions'),
      cellWrap: 'nowrap',
      cell: (provider) => {
        const failed =
          failedProvider?.id === provider.id ? failedProvider.action : null
        const errorId = `sso-provider-error-${encodeURIComponent(provider.id)}`
        return (
          <Box>
            <RowActionMenu
              label={t('sso.actionsAria', { name: provider.name })}
              disabled={listFeedback.pending}
              invalid={failed !== null}
              describedBy={failed !== null ? errorId : undefined}
              actions={[
                ...(metadataRefresh
                  ? [
                      {
                        key: 'refresh',
                        label: t('sso.refreshMetadata'),
                        onSelect: () => requestRefreshMetadata(provider),
                      },
                    ]
                  : []),
                {
                  key: 'reverify',
                  label: t('sso.reverifyDomains'),
                  onSelect: () => requestReverifyDomains(provider),
                },
                {
                  key: 'delete',
                  label: t('sso.delete'),
                  onSelect: () => requestDelete(provider),
                },
              ]}
            />
            {failed !== null && listFailureText !== null && (
              <Typography
                id={errorId}
                variant="caption"
                color="error"
                sx={{ display: 'block', mt: 0.5 }}
              >
                {listFailureText}
              </Typography>
            )}
          </Box>
        )
      },
    },
  ]

  const rows = providers.data
  const pending = providers.isPending && rows === undefined
  const domainReady = domainProvider !== '' && domain.trim() !== ''
  const declaredDomains =
    (rows ?? []).find((provider) => provider.id === domainProvider)?.domains ??
    []

  return (
    <Box>
      <AdminPageHeader
        title={t('sso.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('sso.title') },
        ]}
        sx={{ mb: 2 }}
      />

      {tenantControlled ? null : (
        <SectionCard sx={{ mb: 3 }}>
          <TenantScope
            value={tenant}
            onSelect={changeTenant}
            disabled={anyPending}
          />
        </SectionCard>
      )}

      {tenant === '' ? (
        <EmptyState
          headingLevel="h2"
          title={t('sso.needTenant.title')}
          description={
            <span id={TENANT_HINT_ID}>
              {t('sso.needTenant.description')}
            </span>
          }
        />
      ) : (
        <>
          <SectionCard flushContent sx={{ mb: 3 }}>
            <AdminAsyncSection
              pending={pending}
              payload={rows}
              error={providers.error}
              refreshing={providers.isFetching}
              empty={rows !== undefined && rows.length === 0}
              headingLevel="h2"
              loading={
                <ListSkeleton
                  label={t('sso.loading')}
                  rows={[
                    <Skeleton key="a" variant="text" width="56%" />,
                    <Skeleton key="b" variant="text" width="42%" />,
                  ]}
                />
              }
              errorState={{
                title: t('sso.error.title'),
                description: t('sso.error.description'),
                retryLabel: t('sso.retry'),
                onRetry: () => void providers.refetch(),
              }}
              emptyState={{
                title: t('sso.empty.title'),
                description: t('sso.empty.description'),
              }}
              mfaRelogin={session === null ? undefined : relogin}
            >
              {(settled) => (
                <DataTable
                  rows={settled}
                  columns={columns}
                  rowKey={(provider) => provider.id}
                />
              )}
            </AdminAsyncSection>
            <Box ref={listRegionRef}>
              <WriteFeedbackRegion
                outcome={listFeedback.outcome}
                ssoRelogin={session === null ? undefined : relogin}
                mfaRelogin={session === null ? undefined : relogin}
              />
            </Box>
          </SectionCard>

          <SectionCard
            title={t('sso.provider.title')}
            icon={<SsoIcon size={24} />}
            sx={{ mb: 3 }}
          >
            {/* A selection control is natively disabled while its scope writes;
                the button-shaped-control lint rule does not judge a TextField select. */}
            <TextField
              select
              slotProps={{ select: editProviderMenu }}
              size="small"
              value={editProvider}
              disabled={formFeedback.pending}
              onChange={(event) => selectEditProvider(event.target.value)}
              label={t('sso.provider.editLabel')}
              sx={{ minWidth: 220, mb: 1 }}
            >
              <MenuItem value="">{t('sso.provider.newOption')}</MenuItem>
              {(rows ?? []).map((provider) => (
                <MenuItem key={provider.id} value={provider.id}>
                  {provider.name}
                </MenuItem>
              ))}
            </TextField>
            <SsoProviderWriteForm
              key={editProvider}
              tenant={tenant}
              provider={(rows ?? []).find((provider) => provider.id === editProvider)}
              feedback={formFeedback}
            />
            <Box ref={formRegionRef}>
              <WriteFeedbackRegion
                outcome={formFeedback.outcome}
                ssoRelogin={session === null ? undefined : relogin}
                mfaRelogin={session === null ? undefined : relogin}
              />
            </Box>
          </SectionCard>

          <SectionCard
            title={t('sso.domains.title')}
            icon={<SsoIcon size={24} />}
            sx={{ mb: 3 }}
          >
            <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
              {t('sso.domains.note')}
            </Typography>
            <TextField
              select
              slotProps={{ select: domainProviderMenu }}
              size="small"
              value={domainProvider}
              disabled={domainsFeedback.pending}
              onChange={(event) => selectDomainProvider(event.target.value)}
              label={t('sso.domains.providerLabel')}
              sx={{ minWidth: 200, mb: 2 }}
            >
              {(rows ?? []).map((provider) => (
                <MenuItem key={provider.id} value={provider.id}>
                  {provider.name}
                </MenuItem>
              ))}
            </TextField>

            {domainProvider === '' ? (
              <Typography
                variant="body2"
                color="text.secondary"
                sx={{ mb: 2 }}
              >
                {t('sso.domains.chooseProvider')}
              </Typography>
            ) : (
              <Box
                component="ul"
                role="list"
                aria-label={t('sso.domains.listLabel')}
                sx={{ listStyle: 'none', m: 0, p: 0, mb: 2 }}
              >
                {declaredDomains.length === 0 ? (
                  <Typography
                    component="li"
                    variant="body2"
                    color="text.secondary"
                    sx={{ py: 1 }}
                  >
                    {t('sso.domains.empty')}
                  </Typography>
                ) : (
                  declaredDomains.map((declared) => (
                    <Box
                      component="li"
                      key={declared.domain}
                      sx={{
                        display: 'flex',
                        flexWrap: 'wrap',
                        alignItems: 'center',
                        columnGap: 2,
                        rowGap: 1,
                        py: 1,
                        borderBottom: 1,
                        borderColor: 'divider',
                        '&:last-of-type': { borderBottom: 0 },
                      }}
                    >
                      <Box
                        sx={{
                          display: 'flex',
                          flexWrap: 'wrap',
                          alignItems: 'center',
                          columnGap: 2,
                          rowGap: 1,
                          flexGrow: 1,
                          minWidth: 0,
                        }}
                      >
                        <Typography
                          variant="body2"
                          sx={(theme) => ({
                            fontFamily: theme.typography.fontFamilyMono,
                            wordBreak: 'break-all',
                          })}
                        >
                          {declared.domain}
                        </Typography>
                        <Typography
                          variant="body2"
                          color={declared.verified ? 'success.main' : 'text.secondary'}
                        >
                          {declared.verified
                            ? t('sso.domains.state.verified')
                            : t('sso.domains.state.unverified')}
                        </Typography>
                      </Box>
                      <Box sx={{ display: 'flex', columnGap: 1, flexShrink: 0 }}>
                        <RowActionMenu
                          label={t('sso.domains.actionsAria', {
                            domain: declared.domain,
                          })}
                          disabled={domainsFeedback.pending}
                          actions={[
                            ...(declared.verified
                              ? []
                              : [
                                  {
                                    key: 'verify',
                                    label: t('sso.domains.verify'),
                                    onSelect: () =>
                                      requestVerifyDomain(declared.domain),
                                  },
                                ]),
                            {
                              key: 'remove',
                              label: t('sso.domains.remove'),
                              onSelect: () =>
                                requestRemoveDomain(declared.domain),
                            },
                          ]}
                        />
                      </Box>
                    </Box>
                  ))
                )}
              </Box>
            )}

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
                value={domain}
                onChange={(event) => setDomain(event.target.value)}
                label={t('sso.domains.domainLabel')}
              />
              <ActionButton
                type="button"
                emphasis="primary"
                busy={busyDomainAction?.action === 'claim'}
                unavailable={domainsFeedback.pending || !domainReady}
                unavailableReason={
                  !domainReady ? t('sso.domains.claimUnavailable') : undefined
                }
                onClick={requestClaimDomain}
              >
                {t('sso.domains.claim')}
              </ActionButton>
            </Box>
            {claimToken !== null && (
              <ClaimTokenNotice
                key={claimToken.token}
                notice={claimToken}
                onClose={() => setClaimToken(null)}
              />
            )}
            <Box ref={domainsRegionRef}>
              <WriteFeedbackRegion
                outcome={domainsFeedback.outcome}
                ssoRelogin={session === null ? undefined : relogin}
                mfaRelogin={session === null ? undefined : relogin}
              />
            </Box>
          </SectionCard>
        </>
      )}

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}

// The one-time verification token and the DNS TXT record that hands it over.
// The page holds its state, so no scope reset can drop the only copy.
function ClaimTokenNotice({
  notice,
  onClose,
}: {
  readonly notice: ClaimToken
  readonly onClose: () => void
}) {
  const { t, i18n } = useAdminUiTranslation()
  const copyKey = 'sso.domains.claimTokenCopy'
  const copyLabel = i18n.exists(copyKey, { ns: ADMIN_UI_NAMESPACE })
    ? t(copyKey)
    : t('invitations.issued.copy')
  const recordName = `_speed-authn.${notice.domain}`
  const recordValue = `speed-authn=${notice.token}`
  return (
    <Box sx={{ mt: 1 }}>
      <OneTimeValuePanel
        message={t('sso.domains.claimToken')}
        value={notice.token}
        copyLabel={copyLabel}
        details={[
          {
            label: t('sso.domains.claimRecordName'),
            value: recordName,
            copyLabel: t('sso.domains.claimRecordNameCopy'),
          },
          {
            label: t('sso.domains.claimRecordValue'),
            value: recordValue,
            copyLabel: t('sso.domains.claimRecordValueCopy'),
          },
        ]}
        closeLabel={t('sso.domains.claimTokenClose')}
        closeAriaLabel={t('sso.domains.claimTokenCloseAria', {
          tenant: notice.tenant,
          name: notice.providerName,
          domain: notice.domain,
        })}
        onClose={onClose}
      />
    </Box>
  )
}

// Returns the typed JSON object, or null when the text is not one. Empty means no config.
function parseConfig(text: string): Record<string, unknown> | null {
  try {
    const parsed: unknown = JSON.parse(text)
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return null
    }
    return parsed as Record<string, unknown>
  } catch {
    return null
  }
}

// An update starts from the stored configuration because the server refuses an
// empty config; a provider with no configuration starts empty.
function configTextOf(config: unknown): string {
  if (config === undefined || config === null) {
    return ''
  }
  try {
    return JSON.stringify(config, null, 2) ?? ''
  } catch {
    return ''
  }
}

export type TriState = 'unchanged' | 'true' | 'false'

// The module refuses any other username claim.
const USERNAME_CLAIMS = ['preferred_username', 'email'] as const

function isUsernameClaim(value: string): boolean {
  return (USERNAME_CLAIMS as readonly string[]).includes(value)
}

/** The fields a provider write carries, as the operator left them. */
export interface SsoProviderWriteDraft {
  readonly protocol: string
  readonly name: string
  readonly provisioning: string
  readonly usernameStrategy: string
  readonly usernameClaim: string
  readonly enabled: TriState
  readonly trustIdpMfa: TriState
  readonly allowIdpInitiated: TriState
  readonly allowSha1: TriState
  readonly mergeExisting: TriState
  readonly config: string
  readonly clientSecret: string
}

/**
 * Builds the body of a provider write. An update names both username fields
 * explicitly because the module keeps a field a body omits; an empty string
 * clears it.
 */
export function ssoProviderWriteBody(
  draft: SsoProviderWriteDraft,
  updating: boolean,
): AuthnAdminProviderWrite {
  const provisioning = draft.provisioning.trim()
  const jit = provisioning === 'jit'
  const strategy = draft.usernameStrategy.trim()
  const claim = draft.usernameClaim.trim()
  const chosenClaim = isUsernameClaim(claim) ? claim : ''
  const username = updating
    ? {
        username_strategy: jit ? strategy : '',
        username_claim: jit && strategy === 'claim' ? chosenClaim : '',
      }
    : jit
      ? {
          username_strategy: strategy,
          ...(strategy === 'claim' ? { username_claim: chosenClaim } : {}),
        }
      : {}
  const parsedConfig =
    draft.config.trim() === '' ? null : parseConfig(draft.config)
  return {
    protocol: draft.protocol,
    name: draft.name,
    provisioning,
    ...username,
    ...(draft.enabled === 'unchanged' ? {} : { enabled: draft.enabled === 'true' }),
    ...(draft.trustIdpMfa === 'unchanged'
      ? {}
      : { trust_idp_mfa: draft.trustIdpMfa === 'true' }),
    ...(draft.allowIdpInitiated === 'unchanged'
      ? {}
      : { allow_idp_initiated: draft.allowIdpInitiated === 'true' }),
    ...(draft.allowSha1 === 'unchanged'
      ? {}
      : { allow_sha1: draft.allowSha1 === 'true' }),
    ...(draft.mergeExisting === 'unchanged'
      ? {}
      : { merge_existing: draft.mergeExisting === 'true' }),
    ...(parsedConfig === null ? {} : { config: parsedConfig }),
    ...(draft.clientSecret === '' ? {} : { client_secret: draft.clientSecret }),
  }
}

/** One provider flag's three-state select; each instance owns its menu. */
function TriStateSelect({
  value,
  onChange,
  label,
}: {
  readonly value: TriState
  readonly onChange: (next: TriState) => void
  readonly label: string
}) {
  const { t } = useAdminUiTranslation()
  const menu = useInertSelectMenu()
  return (
    <TextField
      select
      slotProps={{ select: menu }}
      size="small"
      value={value}
      onChange={(event) => onChange(event.target.value as TriState)}
      label={label}
      sx={{ minWidth: 180 }}
    >
      <MenuItem value="unchanged">{t('sso.provider.tri.unchanged')}</MenuItem>
      <MenuItem value="true">{t('sso.provider.tri.true')}</MenuItem>
      <MenuItem value="false">{t('sso.provider.tri.false')}</MenuItem>
    </TextField>
  )
}

// The provider create and update control: on a create the fields start empty and
// an unset boolean takes the module's false default; on an update they start from
// the provider and an unset boolean leaves the stored flag.
function SsoProviderWriteForm({
  tenant,
  provider,
  feedback,
}: {
  readonly tenant: string
  readonly provider?: AdminSsoProvider
  readonly feedback: WriteFeedback
}) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const createProvider = operations.useCreateSsoProvider()
  const updateProvider = operations.useUpdateSsoProvider()
  const usernameStrategyMenu = useInertSelectMenu()
  const usernameClaimMenu = useInertSelectMenu()

  const [protocol, setProtocol] = useState(provider?.protocol ?? '')
  const [name, setName] = useState(provider?.name ?? '')
  const [provisioning, setProvisioning] = useState(provider?.provisioning ?? '')
  // User-confirm is the default; a stored strategy wins on an update.
  const [usernameStrategy, setUsernameStrategy] = useState(
    provider?.username_strategy !== undefined && provider.username_strategy !== ''
      ? provider.username_strategy
      : 'user-confirm',
  )
  const [usernameClaim, setUsernameClaim] = useState(provider?.username_claim ?? '')
  const [enabled, setEnabled] = useState<TriState>('unchanged')
  const [trustIdpMfa, setTrustIdpMfa] = useState<TriState>('unchanged')
  const [allowIdpInitiated, setAllowIdpInitiated] = useState<TriState>('unchanged')
  const [allowSha1, setAllowSha1] = useState<TriState>('unchanged')
  const [mergeExisting, setMergeExisting] = useState<TriState>('unchanged')
  const [config, setConfig] = useState(() => configTextOf(provider?.config))
  const [clientSecret, setClientSecret] = useState('')

  const configInvalid = config.trim() !== '' && parseConfig(config) === null
  // The username fields belong to jit only, and a claim strategy needs its claim.
  const provisioningJit = provisioning.trim() === 'jit'
  const claimStrategy = provisioningJit && usernameStrategy === 'claim'
  const usernameMissing =
    provisioningJit &&
    (usernameStrategy === '' ||
      (claimStrategy && !isUsernameClaim(usernameClaim.trim())))
  const requiredMissing =
    protocol.trim() === '' ||
    name.trim() === '' ||
    provisioning.trim() === '' ||
    usernameMissing

  const submitProviderWrite = () => {
    const body = ssoProviderWriteBody(
      {
        protocol,
        name,
        provisioning,
        usernameStrategy,
        usernameClaim,
        enabled,
        trustIdpMfa,
        allowIdpInitiated,
        allowSha1,
        mergeExisting,
        config,
        clientSecret,
      },
      provider !== undefined,
    )
    if (provider === undefined) {
      const created = name
      void feedback.run(async () => {
        await createProvider.mutateAsync({ tenant, ...body })
        // A landed create resets the form; a refusal keeps every value.
        setProtocol('')
        setName('')
        setProvisioning('')
        setUsernameStrategy('user-confirm')
        setUsernameClaim('')
        setEnabled('unchanged')
        setTrustIdpMfa('unchanged')
        setAllowIdpInitiated('unchanged')
        setAllowSha1('unchanged')
        setMergeExisting('unchanged')
        setConfig('')
        setClientSecret('')
        return t('sso.provider.created', { name: created })
      })
    } else {
      const saved = name
      void feedback.run(async () => {
        await updateProvider.mutateAsync({ id: provider.id, tenant, ...body })
        return t('sso.provider.updated', { name: saved })
      })
    }
  }

  const submitWrite = useActivationGuard(submitProviderWrite, {
    busy: feedback.pending,
    unavailable: configInvalid || requiredMissing,
  })
  const submitExplanation: ActionButtonUnavailableExplanation = requiredMissing
    ? { unavailableReason: t('sso.provider.requiredMissing') }
    : configInvalid
      ? { unavailableDescribedBy: CONFIG_HINT_ID }
      : {}

  return (
    <Box>
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
          value={protocol}
          onChange={(event) => setProtocol(event.target.value)}
          label={t('sso.provider.protocolLabel')}
        />
        <TextField
          size="small"
          value={name}
          onChange={(event) => setName(event.target.value)}
          label={t('sso.provider.nameLabel')}
        />
        <TextField
          size="small"
          value={provisioning}
          onChange={(event) => setProvisioning(event.target.value)}
          label={t('sso.provider.provisioningLabel')}
        />
        {provisioningJit && (
          <TextField
            select
            size="small"
            slotProps={{ select: usernameStrategyMenu }}
            value={usernameStrategy}
            onChange={(event) => setUsernameStrategy(event.target.value)}
            label={t('sso.provider.usernameStrategyLabel')}
            sx={{ minWidth: 180 }}
          >
            <MenuItem value="user-confirm">
              {t('sso.provider.usernameStrategy.userConfirm')}
            </MenuItem>
            <MenuItem value="claim">{t('sso.provider.usernameStrategy.claim')}</MenuItem>
          </TextField>
        )}
        {claimStrategy && (
          <TextField
            select
            size="small"
            slotProps={{ select: usernameClaimMenu }}
            value={usernameClaim}
            onChange={(event) => setUsernameClaim(event.target.value)}
            label={t('sso.provider.usernameClaimLabel')}
            sx={{ minWidth: 180 }}
          >
            <MenuItem value="preferred_username">
              {t('sso.provider.usernameClaim.preferredUsername')}
            </MenuItem>
            <MenuItem value="email">
              {t('sso.provider.usernameClaim.email')}
            </MenuItem>
          </TextField>
        )}
        <TriStateSelect
          value={enabled}
          onChange={setEnabled}
          label={t('sso.provider.enabledLabel')}
        />
        <TriStateSelect
          value={trustIdpMfa}
          onChange={setTrustIdpMfa}
          label={t('sso.provider.trustIdpMfaLabel')}
        />
        <TriStateSelect
          value={allowIdpInitiated}
          onChange={setAllowIdpInitiated}
          label={t('sso.provider.allowIdpInitiatedLabel')}
        />
        <TriStateSelect
          value={allowSha1}
          onChange={setAllowSha1}
          label={t('sso.provider.allowSha1Label')}
        />
        <TriStateSelect
          value={mergeExisting}
          onChange={setMergeExisting}
          label={t('sso.provider.mergeExistingLabel')}
        />
        <TextField
          id={CONFIG_FIELD_ID}
          size="small"
          value={config}
          onChange={(event) => setConfig(event.target.value)}
          label={t('sso.provider.configLabel')}
          error={configInvalid}
          helperText={t('sso.provider.configHelper')}
          multiline
          minRows={1}
          sx={{ minWidth: 260 }}
        />
        <TextField
          size="small"
          type="password"
          value={clientSecret}
          onChange={(event) => setClientSecret(event.target.value)}
          label={t('sso.provider.clientSecretLabel')}
        />
        <ActionButton
          type="button"
          emphasis="primary"
          busy={feedback.pending}
          unavailable={configInvalid || requiredMissing}
          {...submitExplanation}
          onClick={submitWrite}
        >
          {provider === undefined ? t('sso.provider.createSubmit') : t('sso.provider.updateSubmit')}
        </ActionButton>
      </Box>
    </Box>
  )
}
