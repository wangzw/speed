/**
 * SsoPage: the SSO-provider administration page of the console.
 *
 * SSO providers belong to a tenant (/auth/admin/tenants/:tenant/sso/
 * providers), so the page opens with a tenant selector and keeps its
 * reads disabled until a tenant is chosen. It lists a tenant's providers
 * (name, protocol, enabled state, version), creates and edits one
 * (SsoProviderWriteForm, choosing create or an existing provider),
 * deletes one, refreshes a provider's metadata, re-verifies every claimed
 * domain, and -- for a named provider -- claims, verifies or removes one
 * domain.
 *
 * The provider view carries no domain list (the module exposes domain
 * operations but not the domains themselves on the read), so the domain
 * controls name the provider from the loaded list and the domain by
 * hand: the operator types the domain to claim, verify or remove.
 *
 * The page's three titled control groups are its three feedback scopes:
 * the provider list (delete, refresh metadata, re-verify domains) reports
 * under its table; the provider write form reports under itself; the domain
 * bar (claim, verify, remove) reports under its controls. A refusal marks
 * the affected provider row inline with the same resolved text the region
 * shows and brings that region into view; a success names the provider or
 * provider-and-domain it changed. The tenant seeds every write, so changing
 * it resets all three scopes and disables the tenant selector while any of
 * them is in flight; the provider selectors follow the scope they drive and
 * are disabled only while that one is pending.
 *
 * A claimed domain's verification token is handed out once. Its display
 * panel is the page's own state, rendered beside the domain group's region
 * and outside that scope's slot, so a reset, a later failure or a context
 * change never drops the only copy; it stays until the operator closes it.
 *
 * Every read and write is an operation on the console operations bundle,
 * so the page builds no client and writes no query key. The page opens
 * with ui-kit's PageHeader.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  DataTable,
  EmptyState,
  ListSkeleton,
  PageHeader,
  useWriteFeedback,
} from '@speed/ui-kit'
import type { DataTableColumn, WriteFeedback } from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { useConsoleUiErrorText } from './internal/error-text.js'
import { TenantScope, tenantFromLocation } from './internal/tenant-scope.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import {
  failureTextOf,
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleSsoProvider } from './operations.js'

/** The provider-row action a row's inline failure marker belongs to. */
type ProviderAction = 'refresh' | 'reverify' | 'delete'

/** The domain-bar action the shared button row is running. */
type DomainAction = 'claim' | 'verify' | 'remove'

/** The one-time verification token a claim hands out, with its owner. */
interface ClaimToken {
  readonly token: string
  readonly tenant: string
  readonly providerName: string
  readonly domain: string
}

export function SsoPage() {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()

  const [tenant, setTenant] = useState(tenantFromLocation)
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

  // One scope per control group: the provider list and its row actions, the
  // provider write form, and the domain bar.
  const listFeedback = useWriteFeedback()
  const formFeedback = useWriteFeedback()
  const domainsFeedback = useWriteFeedback()
  const listRegionRef = useFailureScroll(listFeedback.outcome)
  const formRegionRef = useFailureScroll(formFeedback.outcome)
  const domainsRegionRef = useFailureScroll(domainsFeedback.outcome)

  // The row each list-scope failure names, and the row control currently
  // running, cleared when the next attempt starts.
  const [failedProvider, setFailedProvider] = useState<{
    readonly id: string
    readonly action: ProviderAction
  } | null>(null)
  const [busyProvider, setBusyProvider] = useState<{
    readonly id: string
    readonly action: ProviderAction
  } | null>(null)
  const [busyDomainAction, setBusyDomainAction] = useState<DomainAction | null>(
    null,
  )

  // The one resolver every region binds: a row's inline marker resolves the
  // same failure code through it, so the two can never carry different copy.
  const resolveErrorText = useConsoleUiErrorText()
  const listFailureText = failureTextOf(listFeedback.outcome, resolveErrorText)

  const anyPending =
    listFeedback.pending || formFeedback.pending || domainsFeedback.pending

  // The tenant is an argument of every write here: a change clears all
  // three slots and every row marker, and drops the provider selections
  // whose ids no longer name this tenant's providers. The one-time token
  // panel is page state, not a slot result, and stays: it names the tenant
  // it was claimed under.
  const changeTenant = (next: string) => {
    setTenant(next)
    listFeedback.reset()
    formFeedback.reset()
    domainsFeedback.reset()
    setFailedProvider(null)
    setBusyProvider(null)
    setBusyDomainAction(null)
    setEditProvider('')
    setDomainProvider('')
  }

  // The provider form scope follows the provider it edits: another target's
  // result would read as this one's.
  const selectEditProvider = (next: string) => {
    setEditProvider(next)
    formFeedback.reset()
  }

  // The domain scope follows the provider the bar names.
  const selectDomainProvider = (next: string) => {
    setDomainProvider(next)
    domainsFeedback.reset()
  }

  const runProviderAction = (
    provider: ConsoleSsoProvider,
    action: ProviderAction,
    attempt: () => Promise<string>,
  ) => {
    setFailedProvider(null)
    setBusyProvider({ id: provider.id, action })
    void listFeedback.run(async () => {
      try {
        return await attempt()
      } catch (error) {
        setFailedProvider({ id: provider.id, action })
        throw error
      } finally {
        setBusyProvider(null)
      }
    })
  }

  const runDomainAction = (
    action: DomainAction,
    attempt: () => Promise<string>,
  ) => {
    setBusyDomainAction(action)
    void domainsFeedback.run(async () => {
      try {
        return await attempt()
      } finally {
        setBusyDomainAction(null)
      }
    })
  }

  const requestRefreshMetadata = (provider: ConsoleSsoProvider) => {
    runProviderAction(provider, 'refresh', async () => {
      await refreshMetadata.mutateAsync({ id: provider.id, tenant })
      return t('sso.refreshMetadataSaved', { name: provider.name })
    })
  }

  const requestReverifyDomains = (provider: ConsoleSsoProvider) => {
    runProviderAction(provider, 'reverify', async () => {
      await reverifyDomains.mutateAsync({ id: provider.id, tenant })
      return t('sso.reverifyDomainsSaved', { name: provider.name })
    })
  }

  const requestDelete = (provider: ConsoleSsoProvider) => {
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
    const claimed = domain
    runDomainAction('claim', async () => {
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

  const requestVerifyDomain = () => {
    const name = providerName(domainProvider)
    const verified = domain
    runDomainAction('verify', async () => {
      await verifyDomain.mutateAsync({
        id: domainProvider,
        tenant,
        domain: verified,
      })
      return t('sso.domains.verified', { name, domain: verified })
    })
  }

  const requestRemoveDomain = () => {
    const name = providerName(domainProvider)
    const removed = domain
    confirm.ask({
      title: t('sso.domains.removeTitle'),
      message: t('sso.domains.removeMessage', { domain: removed }),
      confirmLabel: t('common.confirm'),
      run: () => {
        runDomainAction('remove', async () => {
          await removeDomain.mutateAsync({
            id: domainProvider,
            tenant,
            domain: removed,
          })
          return t('sso.domains.removed', { name, domain: removed })
        })
      },
    })
  }

  const columns: DataTableColumn<ConsoleSsoProvider>[] = [
    {
      id: 'name',
      header: t('sso.column.name'),
      cellWrap: 'nowrap',
      cellFullValue: (provider) => provider.name,
      cell: (provider) => <Typography variant="body2">{provider.name}</Typography>,
    },
    {
      id: 'protocol',
      header: t('sso.column.protocol'),
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
      header: t('sso.column.actions'),
      cellWrap: 'nowrap',
      cell: (provider) => {
        const failed =
          failedProvider?.id === provider.id ? failedProvider.action : null
        const busy =
          busyProvider?.id === provider.id ? busyProvider.action : null
        const errorId = `sso-provider-error-${encodeURIComponent(provider.id)}`
        return (
          <Box>
            <Box sx={{ display: 'flex', columnGap: 1 }}>
              <Button
                type="button"
                size="small"
                variant="outlined"
                aria-label={t('sso.refreshMetadataAria', { name: provider.name })}
                aria-invalid={failed === 'refresh'}
                aria-describedby={failed === 'refresh' ? errorId : undefined}
                disabled={listFeedback.pending}
                loading={busy === 'refresh'}
                onClick={() => requestRefreshMetadata(provider)}
              >
                {t('sso.refreshMetadata')}
              </Button>
              <Button
                type="button"
                size="small"
                variant="outlined"
                aria-label={t('sso.reverifyDomainsAria', { name: provider.name })}
                aria-invalid={failed === 'reverify'}
                aria-describedby={failed === 'reverify' ? errorId : undefined}
                disabled={listFeedback.pending}
                loading={busy === 'reverify'}
                onClick={() => requestReverifyDomains(provider)}
              >
                {t('sso.reverifyDomains')}
              </Button>
              <Button
                type="button"
                size="small"
                variant="outlined"
                color="error"
                aria-label={t('sso.deleteAria', { name: provider.name })}
                aria-invalid={failed === 'delete'}
                aria-describedby={failed === 'delete' ? errorId : undefined}
                disabled={listFeedback.pending}
                loading={busy === 'delete'}
                onClick={() => requestDelete(provider)}
              >
                {t('sso.delete')}
              </Button>
            </Box>
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

  return (
    <Box>
      <PageHeader
        title={t('sso.title')}
        breadcrumbs={[{ label: t('shell.title') }, { label: t('sso.title') }]}
        actions={
          <Button
            type="button"
            variant="outlined"
            size="small"
            disabled={providers.isFetching}
            loading={providers.isFetching}
            onClick={() => void providers.refetch()}
          >
            {t('common.refresh')}
          </Button>
        }
        sx={{ mb: 2 }}
      />

      <Box component="section" sx={{ mb: 3 }}>
        <TenantScope value={tenant} onSelect={changeTenant} disabled={anyPending} />
      </Box>

      {tenant === '' ? (
        <EmptyState
          headingLevel="h2"
          title={t('sso.needTenant.title')}
          description={t('sso.needTenant.description')}
        />
      ) : (
        <>
          <Box component="section" sx={{ mb: 3 }}>
            <ConsoleAsyncSection
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
            >
              {(settled) => (
                <DataTable
                  rows={settled}
                  columns={columns}
                  rowKey={(provider) => provider.id}
                />
              )}
            </ConsoleAsyncSection>
            <Box ref={listRegionRef}>
              <WriteFeedbackRegion outcome={listFeedback.outcome} />
            </Box>
          </Box>

          <Box component="section" sx={{ mb: 3 }}>
            <Typography component="h2" variant="h2" sx={{ fontSize: '1.25rem', mb: 1 }}>
              {t('sso.provider.title')}
            </Typography>
            <TextField
              select
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
              <WriteFeedbackRegion outcome={formFeedback.outcome} />
            </Box>
          </Box>

          <Box component="section" sx={{ mb: 3 }}>
            <Typography
              component="h2"
              variant="h2"
              sx={{ fontSize: '1.25rem', mb: 1 }}
            >
              {t('sso.domains.title')}
            </Typography>
            <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
              {t('sso.domains.note')}
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
                select
                size="small"
                value={domainProvider}
                disabled={domainsFeedback.pending}
                onChange={(event) => selectDomainProvider(event.target.value)}
                label={t('sso.domains.providerLabel')}
                sx={{ minWidth: 200 }}
              >
                {(rows ?? []).map((provider) => (
                  <MenuItem key={provider.id} value={provider.id}>
                    {provider.name}
                  </MenuItem>
                ))}
              </TextField>
              <TextField
                size="small"
                value={domain}
                onChange={(event) => setDomain(event.target.value)}
                label={t('sso.domains.domainLabel')}
              />
              <Button
                type="button"
                variant="contained"
                disabled={domainsFeedback.pending || !domainReady}
                loading={busyDomainAction === 'verify'}
                onClick={requestVerifyDomain}
              >
                {t('sso.domains.verify')}
              </Button>
              <Button
                type="button"
                variant="outlined"
                disabled={domainsFeedback.pending || !domainReady}
                loading={busyDomainAction === 'claim'}
                onClick={requestClaimDomain}
              >
                {t('sso.domains.claim')}
              </Button>
              <Button
                type="button"
                variant="outlined"
                color="error"
                disabled={domainsFeedback.pending || !domainReady}
                loading={busyDomainAction === 'remove'}
                onClick={requestRemoveDomain}
              >
                {t('sso.domains.remove')}
              </Button>
            </Box>
            {claimToken !== null && (
              <ClaimTokenNotice
                notice={claimToken}
                onClose={() => setClaimToken(null)}
              />
            )}
            <Box ref={domainsRegionRef}>
              <WriteFeedbackRegion outcome={domainsFeedback.outcome} />
            </Box>
          </Box>
        </>
      )}

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}

/**
 * ClaimTokenNotice: the display of a domain's verification token, a value
 * handed out once.
 *
 * Its state is the page's (`SsoPage` holds it), so a scope reset, a later
 * failure or a context change cannot drop the only copy. The notice is one
 * polite live region with an explicit close control whose accessible name
 * names the tenant, the provider and the domain it belongs to.
 */
function ClaimTokenNotice({
  notice,
  onClose,
}: {
  readonly notice: ClaimToken
  readonly onClose: () => void
}) {
  const { t } = useConsoleUiTranslation()
  return (
    <Box
      role="status"
      sx={{
        display: 'flex',
        alignItems: 'flex-start',
        columnGap: 2,
        mt: 1,
        p: 2,
        border: 1,
        borderColor: 'divider',
        borderRadius: 1,
      }}
    >
      <Typography variant="body2" sx={{ flex: 1, wordBreak: 'break-all' }}>
        {t('sso.domains.claimToken', { token: notice.token })}
      </Typography>
      <Button
        type="button"
        size="small"
        variant="outlined"
        aria-label={t('sso.domains.claimTokenCloseAria', {
          tenant: notice.tenant,
          name: notice.providerName,
          domain: notice.domain,
        })}
        onClick={onClose}
      >
        {t('sso.domains.claimTokenClose')}
      </Button>
    </Box>
  )
}

/**
 * Parses an optional provider configuration the operator typed as a JSON
 * object. Returns null when the text is not one, so the form can mark the
 * field and keep the submit disabled. An empty text means "no config".
 */
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

/** A three-state boolean field: left as it is, or set true or false. */
type TriState = 'unchanged' | 'true' | 'false'

/**
 * SsoProviderWriteForm: the provider create and update control. The same
 * fields render for both; on a create they start empty (an unset boolean
 * takes the module's false default) and the path tenant is the form's, and
 * on an update they start from the provider and an unset boolean leaves the
 * stored flag. The configuration is JSON and the client secret is write-only.
 *
 * The write reports through the page's provider-form scope (`feedback`), so
 * its result lands in the region the page renders directly under this form;
 * a landed create names the provider and returns the form to its initial
 * state.
 */
function SsoProviderWriteForm({
  tenant,
  provider,
  feedback,
}: {
  readonly tenant: string
  readonly provider?: ConsoleSsoProvider
  readonly feedback: WriteFeedback
}) {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const createProvider = operations.useCreateSsoProvider()
  const updateProvider = operations.useUpdateSsoProvider()

  const [protocol, setProtocol] = useState(provider?.protocol ?? '')
  const [name, setName] = useState(provider?.name ?? '')
  const [provisioning, setProvisioning] = useState(provider?.provisioning ?? '')
  const [enabled, setEnabled] = useState<TriState>('unchanged')
  const [trustIdpMfa, setTrustIdpMfa] = useState<TriState>('unchanged')
  const [allowIdpInitiated, setAllowIdpInitiated] = useState<TriState>('unchanged')
  const [allowSha1, setAllowSha1] = useState<TriState>('unchanged')
  const [mergeExisting, setMergeExisting] = useState<TriState>('unchanged')
  const [config, setConfig] = useState('')
  const [clientSecret, setClientSecret] = useState('')

  const configInvalid = config.trim() !== '' && parseConfig(config) === null
  const requiredMissing =
    protocol.trim() === '' || name.trim() === '' || provisioning.trim() === ''

  const triStateSelect = (
    value: TriState,
    onChange: (next: TriState) => void,
    label: string,
  ) => (
    <TextField
      select
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
        {triStateSelect(enabled, setEnabled, t('sso.provider.enabledLabel'))}
        {triStateSelect(trustIdpMfa, setTrustIdpMfa, t('sso.provider.trustIdpMfaLabel'))}
        {triStateSelect(
          allowIdpInitiated,
          setAllowIdpInitiated,
          t('sso.provider.allowIdpInitiatedLabel'),
        )}
        {triStateSelect(allowSha1, setAllowSha1, t('sso.provider.allowSha1Label'))}
        {triStateSelect(
          mergeExisting,
          setMergeExisting,
          t('sso.provider.mergeExistingLabel'),
        )}
        <TextField
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
        <Button
          type="button"
          variant="contained"
          disabled={feedback.pending || configInvalid || requiredMissing}
          loading={feedback.pending}
          onClick={() => {
            const parsed = config.trim() === '' ? null : parseConfig(config)
            const body = {
              protocol,
              name,
              provisioning,
              ...(enabled === 'unchanged' ? {} : { enabled: enabled === 'true' }),
              ...(trustIdpMfa === 'unchanged'
                ? {}
                : { trust_idp_mfa: trustIdpMfa === 'true' }),
              ...(allowIdpInitiated === 'unchanged'
                ? {}
                : { allow_idp_initiated: allowIdpInitiated === 'true' }),
              ...(allowSha1 === 'unchanged' ? {} : { allow_sha1: allowSha1 === 'true' }),
              ...(mergeExisting === 'unchanged'
                ? {}
                : { merge_existing: mergeExisting === 'true' }),
              ...(parsed === null ? {} : { config: parsed }),
              ...(clientSecret === '' ? {} : { client_secret: clientSecret }),
            }
            if (provider === undefined) {
              const created = name
              void feedback.run(async () => {
                await createProvider.mutateAsync({ tenant, ...body })
                // A landed create returns the form to its initial state, the
                // write-only client secret included: the provider now exists,
                // and none of what was typed belongs to the next one. A
                // refusal keeps every value for correction.
                setProtocol('')
                setName('')
                setProvisioning('')
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
          }}
        >
          {provider === undefined ? t('sso.provider.createSubmit') : t('sso.provider.updateSubmit')}
        </Button>
      </Box>
    </Box>
  )
}
