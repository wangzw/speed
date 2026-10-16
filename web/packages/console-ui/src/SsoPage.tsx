/**
 * SsoPage: the SSO-provider administration page of the console.
 *
 * SSO providers belong to a tenant (/auth/admin/tenants/:tenant/sso/
 * providers), so the page opens with a tenant selector and keeps its
 * reads disabled until a tenant is chosen. It lists a tenant's providers
 * (name, protocol, enabled state, version), deletes one, refreshes a
 * provider's metadata, re-verifies every claimed domain, and -- for a
 * named provider -- verifies or removes one claimed domain.
 *
 * The provider view carries no domain list (the module exposes domain
 * operations but not the domains themselves on the read), so the domain
 * controls name the provider from the loaded list and the domain by
 * hand: the operator types the domain to verify or remove. Creating and
 * editing a provider are held to a request body the platform's OpenAPI
 * fragment does not yet declare, so those two controls are not rendered;
 * see the package's design note on the missing fragment bodies.
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
  AsyncSection,
  DataTable,
  EmptyState,
  ListSkeleton,
  PageHeader,
  errorCodeOf,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { InlineError } from './internal/inline-error.js'
import { TenantScope } from './internal/tenant-scope.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleSsoProvider } from './operations.js'

export function SsoPage() {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()

  const [tenant, setTenant] = useState('')
  const [domainProvider, setDomainProvider] = useState('')
  const [domain, setDomain] = useState('')

  const providers = operations.useSsoProviders(tenant)
  const deleteProvider = operations.useDeleteSsoProvider()
  const refreshMetadata = operations.useRefreshSsoMetadata()
  const reverifyDomains = operations.useReverifySsoDomains()
  const verifyDomain = operations.useVerifySsoDomain()
  const removeDomain = operations.useRemoveSsoDomain()

  const columns: DataTableColumn<ConsoleSsoProvider>[] = [
    {
      id: 'name',
      header: t('sso.column.name'),
      cell: (provider) => <Typography variant="body2">{provider.name}</Typography>,
    },
    {
      id: 'protocol',
      header: t('sso.column.protocol'),
      cell: (provider) => provider.protocol,
    },
    {
      id: 'enabled',
      header: t('sso.column.enabled'),
      priority: 'medium',
      cell: (provider) =>
        provider.enabled ? t('sso.enabledYes') : t('sso.enabledNo'),
    },
    {
      id: 'version',
      header: t('sso.column.version'),
      priority: 'low',
      cell: (provider) => String(provider.version),
    },
    {
      id: 'actions',
      header: t('sso.column.actions'),
      cell: (provider) => (
        <Box sx={{ display: 'flex', flexWrap: 'wrap', columnGap: 1, rowGap: 1 }}>
          <Button
            type="button"
            size="small"
            variant="outlined"
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
            disabled={deleteProvider.isPending}
            onClick={() => deleteProvider.mutate({ id: provider.id, tenant })}
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
          <AsyncSection
            pending={pending}
            payload={rows}
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
          </AsyncSection>

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
                color="error"
                disabled={
                  removeDomain.isPending ||
                  domainProvider === '' ||
                  domain.trim() === ''
                }
                onClick={() =>
                  removeDomain.mutate({ id: domainProvider, tenant, domain })
                }
              >
                {t('sso.domains.remove')}
              </Button>
            </Box>
          </Box>

          <Box sx={{ mt: 2 }}>
            <InlineError code={writeError} />
          </Box>
        </>
      )}
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
