/**
 * TenantDetailPage: one tenant's administration detail.
 *
 * The roster's row-level rename and enable/disable controls live on the
 * roster page; this page is the tenant in full -- its identifier, alias
 * and status read from GET /tenant/admin/tenants/:id, with the same two
 * writes (rename by PATCH, enable/disable by the status route) gathered
 * into the main column. It is a detail page: a main column carrying the
 * subject and its controls, beside a fixed-width side column carrying the
 * tenant's metadata and a way back to the roster.
 *
 * The read is an operation on the console operations bundle, so the page
 * builds no client and writes no query key. The status renders from the
 * roster's own closed spelling (active / disabled); a value outside it
 * renders the generic label, never a raw value. A refused read settles
 * into a permission placeholder (403) or the retryable error placeholder
 * (any other failure); a refused write renders the answer's code text in
 * one banner, resolved from the console-ui whitelist. The rename input
 * marks itself invalid while the alias breaks the identifier domain and
 * holds its submit disabled then, so an inadmissible alias is never sent.
 * When given the console's root path, the page also links to the
 * tenant-addressed administration pages (permissions, invitations and
 * members, SSO providers, impersonation records), carrying this tenant in
 * the `tenant` query parameter those pages seed their tenant selection
 * from -- so the detail page is not a dead end and the link does not drop
 * the tenant context. The page opens with ui-kit's PageHeader, so the
 * heading order never skips.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Paper from '@mui/material/Paper'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  ListSkeleton,
  PageHeader,
  errorCodeOf,
} from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { InlineError } from './internal/inline-error.js'
import { ConsoleLink } from './internal/navigation.js'
import { DefinitionRow, DetailLayout } from './internal/layout.js'
import { isRejectedIdentifier } from './internal/identifier.js'
import { RefreshButton } from './internal/refresh-button.js'
import { TENANT_QUERY_PARAM } from './internal/tenant-scope.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleTenant } from './operations.js'

/** The roster's closed status set; a value outside it renders the generic label. */
const KNOWN_STATUSES = new Set<string>(['active', 'disabled'])

function statusLabel(status: string, t: (key: string) => string): string {
  return KNOWN_STATUSES.has(status)
    ? t(`tenants.status.${status}`)
    : t('tenants.status.other')
}

export interface TenantDetailPageProps {
  /** The tenant to show. */
  readonly id: string
  /** The roster's path, for the breadcrumb and the back link; absent hides them. */
  readonly parentHref?: string
  /**
   * The console's root path, for the tenant-addressed pages' links; absent
   * hides the related-page section (the page has no other way to build the
   * sibling paths).
   */
  readonly rootHref?: string
}

export function TenantDetailPage({ id, parentHref, rootHref }: TenantDetailPageProps) {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const tenant = operations.useTenant({ id })
  const renameTenant = operations.useSetTenantAlias()
  const setStatus = operations.useSetTenantStatus()
  const [alias, setAlias] = useState('')

  const aliasRejected = alias !== '' && isRejectedIdentifier(alias)

  // The tenant-addressed pages all read the tenant from the same query
  // parameter (tenantFromLocation seeds their selector), so a link carries
  // this tenant into them instead of landing on an empty selection.
  const tenantParam = `${TENANT_QUERY_PARAM}=${encodeURIComponent(id)}`
  const related =
    rootHref === undefined
      ? []
      : [
          {
            href: `${rootHref}/permissions?${tenantParam}`,
            label: t('tenantDetail.related.permissions'),
          },
          {
            href: `${rootHref}/invitations?${tenantParam}`,
            label: t('tenantDetail.related.invitations'),
          },
          {
            href: `${rootHref}/sso?${tenantParam}`,
            label: t('tenantDetail.related.sso'),
          },
          {
            href: `${rootHref}/impersonations?${tenantParam}`,
            label: t('tenantDetail.related.impersonations'),
          },
        ]

  const crumbs = [
    { label: t('shell.title') },
    ...(parentHref === undefined
      ? [{ label: t('tenants.title') }]
      : [{ label: t('tenants.title'), href: parentHref }]),
    { label: t('tenantDetail.title', { id }) },
  ]

  const main = (settled: ConsoleTenant) => {
    const disabled = settled.status === 'disabled'
    return (
      <>
        <Paper component="section" variant="outlined" sx={{ p: 2, mb: 3 }}>
          <Typography component="h2" variant="h6" sx={{ mb: 1 }}>
            {t('tenantDetail.summary')}
          </Typography>
          <Box component="dl" sx={{ margin: 0 }}>
            <DefinitionRow label={t('tenantDetail.field.id')} value={settled.id} />
            <DefinitionRow label={t('tenantDetail.field.alias')} value={settled.alias} />
            <DefinitionRow
              label={t('tenantDetail.field.status')}
              value={statusLabel(settled.status, t)}
            />
          </Box>
        </Paper>

        <Paper component="section" variant="outlined" sx={{ p: 2, mb: 3 }}>
          <Typography component="h2" variant="h6" sx={{ mb: 1 }}>
            {t('tenantDetail.actions.title')}
          </Typography>
          <Box
            sx={{
              display: 'flex',
              flexWrap: 'wrap',
              alignItems: 'flex-start',
              columnGap: 2,
              rowGap: 1,
            }}
          >
            <TextField
              size="small"
              value={alias}
              onChange={(event) => setAlias(event.target.value)}
              label={t('tenantDetail.actions.renameLabel')}
              error={aliasRejected}
              helperText={t('tenants.identifierHelper')}
            />
            <Button
              type="button"
              variant="contained"
              disabled={renameTenant.isPending || aliasRejected}
              onClick={() => {
                renameTenant.mutate(
                  { id: settled.id, alias },
                  { onSuccess: () => setAlias('') },
                )
              }}
            >
              {t('tenantDetail.actions.renameSubmit')}
            </Button>
            <Button
              type="button"
              variant="outlined"
              disabled={setStatus.isPending}
              onClick={() => {
                setStatus.mutate({
                  id: settled.id,
                  status: disabled ? 'active' : 'disabled',
                })
              }}
            >
              {disabled
                ? t('tenantDetail.actions.enable')
                : t('tenantDetail.actions.disable')}
            </Button>
            <Box sx={{ flexBasis: '100%' }}>
              <InlineError
                code={
                  renameTenant.isError
                    ? errorCodeOf(renameTenant.error)
                    : setStatus.isError
                      ? errorCodeOf(setStatus.error)
                      : null
                }
              />
            </Box>
          </Box>
        </Paper>

        {related.length > 0 && (
          <Paper component="section" variant="outlined" sx={{ p: 2 }}>
            <Typography component="h2" variant="h6" sx={{ mb: 1 }}>
              {t('tenantDetail.related.title')}
            </Typography>
            <Box
              component="ul"
              sx={{
                listStyle: 'none',
                margin: 0,
                padding: 0,
                display: 'flex',
                flexDirection: 'column',
                gap: 1,
              }}
            >
              {related.map((link) => (
                <Box component="li" key={link.href}>
                  <ConsoleLink href={link.href}>{link.label}</ConsoleLink>
                </Box>
              ))}
            </Box>
          </Paper>
        )}
      </>
    )
  }

  const rows = tenant.data
  const pending = tenant.isPending && rows === undefined

  return (
    <Box>
      <PageHeader
        title={t('tenantDetail.title', { id })}
        breadcrumbs={crumbs}
        actions={
          <RefreshButton
            busy={tenant.isFetching}
            onClick={() => void tenant.refetch()}
          />
        }
        sx={{ mb: 2 }}
      />

      <DetailLayout
        main={
          <ConsoleAsyncSection
            pending={pending}
            payload={rows}
            error={tenant.error}
            refreshing={tenant.isFetching}
            empty={false}
            headingLevel="h2"
            loading={
              <ListSkeleton
                label={t('tenantDetail.loading')}
                rows={[
                  <Skeleton key="a" variant="text" width="56%" />,
                  <Skeleton key="b" variant="text" width="44%" />,
                  <Skeleton key="c" variant="text" width="38%" />,
                ]}
              />
            }
            errorState={{
              title: t('tenantDetail.error.title'),
              description: t('tenantDetail.error.description'),
              retryLabel: t('tenantDetail.retry'),
              onRetry: () => void tenant.refetch(),
            }}
            notFoundState={{
              title: t('detail.notFound.title'),
              description: t('detail.notFound.description'),
              retryLabel: t('tenantDetail.retry'),
              onRetry: () => void tenant.refetch(),
            }}
          >
            {main}
          </ConsoleAsyncSection>
        }
        side={
          <Paper component="section" variant="outlined" sx={{ p: 2 }}>
            <Typography component="h2" variant="h6" sx={{ mb: 1 }}>
              {t('tenantDetail.field.id')}
            </Typography>
            <Typography variant="body2" sx={{ wordBreak: 'break-all' }}>
              {id}
            </Typography>
            {parentHref !== undefined && (
              <Box sx={{ mt: 2 }}>
                <ConsoleLink href={parentHref}>{t('tenantDetail.back')}</ConsoleLink>
              </Box>
            )}
          </Paper>
        }
      />
    </Box>
  )
}
