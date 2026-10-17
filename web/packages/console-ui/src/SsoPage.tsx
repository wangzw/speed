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
  errorCodeOf,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { InlineError } from './internal/inline-error.js'
import { TenantScope, tenantFromLocation } from './internal/tenant-scope.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleSsoProvider } from './operations.js'

export function SsoPage() {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()

  const [tenant, setTenant] = useState(tenantFromLocation)
  const [domainProvider, setDomainProvider] = useState('')
  const [domain, setDomain] = useState('')
  const [editProvider, setEditProvider] = useState('')
  const [claimToken, setClaimToken] = useState<string | null>(null)
  const confirm = usePendingConfirm()

  const providers = operations.useSsoProviders(tenant)
  const deleteProvider = operations.useDeleteSsoProvider()
  const refreshMetadata = operations.useRefreshSsoMetadata()
  const reverifyDomains = operations.useReverifySsoDomains()
  const verifyDomain = operations.useVerifySsoDomain()
  const removeDomain = operations.useRemoveSsoDomain()
  const claimDomain = operations.useClaimSsoDomain()

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
      cell: (provider) => (
        <Box sx={{ display: 'flex', columnGap: 1 }}>
          <Button
            type="button"
            size="small"
            variant="outlined"
            aria-label={t('sso.refreshMetadataAria', { name: provider.name })}
            disabled={refreshMetadata.isPending}
            onClick={() =>
              refreshMetadata.mutate({ id: provider.id, tenant })
            }
          >
            {t('sso.refreshMetadata')}
          </Button>
          <Button
            type="button"
            size="small"
            variant="outlined"
            aria-label={t('sso.reverifyDomainsAria', { name: provider.name })}
            disabled={reverifyDomains.isPending}
            onClick={() => reverifyDomains.mutate({ id: provider.id, tenant })}
          >
            {t('sso.reverifyDomains')}
          </Button>
          <Button
            type="button"
            size="small"
            variant="outlined"
            color="error"
            aria-label={t('sso.deleteAria', { name: provider.name })}
            disabled={deleteProvider.isPending}
            onClick={() =>
              confirm.ask({
                title: t('sso.deleteTitle'),
                message: t('sso.deleteMessage', { name: provider.name }),
                confirmLabel: t('common.confirm'),
                run: () => deleteProvider.mutate({ id: provider.id, tenant }),
              })
            }
          >
            {t('sso.delete')}
          </Button>
        </Box>
      ),
    },
  ]

  const rows = providers.data
  const pending = providers.isPending && rows === undefined

  const writeError = firstErrorCode([
    deleteProvider.error,
    refreshMetadata.error,
    reverifyDomains.error,
    verifyDomain.error,
    removeDomain.error,
    claimDomain.error,
  ])

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
            onClick={() => void providers.refetch()}
          >
            {t('common.refresh')}
          </Button>
        }
        sx={{ mb: 2 }}
      />

      <Box component="section" sx={{ mb: 3 }}>
        <TenantScope value={tenant} onSelect={setTenant} />
      </Box>

      {tenant === '' ? (
        <EmptyState
          headingLevel="h2"
          title={t('sso.needTenant.title')}
          description={t('sso.needTenant.description')}
        />
      ) : (
        <>
          <ConsoleAsyncSection
            pending={pending}
            payload={rows}
            error={providers.error}
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

          <Box component="section" sx={{ mt: 2, mb: 3 }}>
            <Typography component="h2" variant="h2" sx={{ fontSize: '1.25rem', mb: 1 }}>
              {t('sso.provider.title')}
            </Typography>
            <TextField
              select
              size="small"
              value={editProvider}
              onChange={(event) => setEditProvider(event.target.value)}
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
            />
          </Box>

          <Box component="section" sx={{ mt: 3 }}>
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
                onChange={(event) => setDomainProvider(event.target.value)}
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
                disabled={
                  verifyDomain.isPending ||
                  domainProvider === '' ||
                  domain.trim() === ''
                }
                onClick={() =>
                  verifyDomain.mutate({
                    id: domainProvider,
                    tenant,
                    domain,
                  })
                }
              >
                {t('sso.domains.verify')}
              </Button>
              <Button
                type="button"
                variant="outlined"
                disabled={
                  claimDomain.isPending ||
                  domainProvider === '' ||
                  domain.trim() === ''
                }
                onClick={() =>
                  claimDomain.mutate(
                    { id: domainProvider, tenant, domain },
                    { onSuccess: (answer) => setClaimToken(answer.verify_token) },
                  )
                }
              >
                {t('sso.domains.claim')}
              </Button>
              <Button
                type="button"
                variant="outlined"
                color="error"
                disabled={
                  removeDomain.isPending ||
                  domainProvider === '' ||
                  domain.trim() === ''
                }
                onClick={() =>
                  confirm.ask({
                    title: t('sso.domains.removeTitle'),
                    message: t('sso.domains.removeMessage', { domain }),
                    confirmLabel: t('common.confirm'),
                    run: () =>
                      removeDomain.mutate({ id: domainProvider, tenant, domain }),
                  })
                }
              >
                {t('sso.domains.remove')}
              </Button>
            </Box>
            {claimToken !== null && (
              <Typography variant="body2" sx={{ wordBreak: 'break-all', mt: 1 }}>
                {t('sso.domains.claimToken', { token: claimToken })}
              </Typography>
            )}
          </Box>

          <Box sx={{ mt: 2 }}>
            <InlineError code={writeError} />
          </Box>
        </>
      )}

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}

/** The first defined error's code, or null when every error is absent. */
function firstErrorCode(errors: readonly unknown[]): string | null {
  for (const error of errors) {
    if (error !== null && error !== undefined) {
      return errorCodeOf(error)
    }
  }
  return null
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
 */
function SsoProviderWriteForm({
  tenant,
  provider,
}: {
  readonly tenant: string
  readonly provider?: ConsoleSsoProvider
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
  const busy = createProvider.isPending || updateProvider.isPending
  const error = createProvider.error ?? updateProvider.error
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
          disabled={busy || configInvalid || requiredMissing}
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
              createProvider.mutate({ tenant, ...body })
            } else {
              updateProvider.mutate({ id: provider.id, tenant, ...body })
            }
          }}
        >
          {provider === undefined ? t('sso.provider.createSubmit') : t('sso.provider.updateSubmit')}
        </Button>
      </Box>
      <Box sx={{ mt: 1 }}>
        {/* A mutation that has not failed carries null, not undefined;
            both mean there is no error to render. */}
        <InlineError code={error == null ? null : errorCodeOf(error)} />
      </Box>
    </Box>
  )
}
