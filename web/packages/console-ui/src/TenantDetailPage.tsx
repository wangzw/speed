/**
 * TenantDetailPage: one tenant's administration detail.
 *
 * The roster's row-level rename and enable/disable controls live on the
 * roster page; this page is the tenant in full -- its identifier, alias
 * and status read from GET /api/v1/system/tenants/:id, with the same two
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
 * (any other failure). The rename and the enable/disable are one control
 * group, so they report through one feedback scope whose region sits
 * directly under their controls: each landed write confirms and names this
 * tenant, and a refusal resolves the answer's code through the console-ui
 * whitelist. The rename input
 * marks itself invalid while the alias breaks the identifier domain and
 * holds its submit disabled then, so an inadmissible alias is never sent.
 * The page also links to the tenant-addressed administration pages
 * (permissions, invitations and members, SSO providers, impersonation
 * records), carrying this tenant in the `tenant` query parameter those
 * pages seed their tenant selection from -- so the detail page is not a
 * dead end and the link does not drop the tenant context. Those links are
 * resolved through the route table the shell publishes: each sibling is
 * looked up by its stable declaration id, because the console may be
 * mounted under any prefix and the declaration is the authority on where
 * the page lives. A sibling whose declaration is absent, or whose
 * declaration needs a feature this process does not serve, renders no
 * link; `relatedHref` overrides a resolved target. The page opens with
 * ui-kit's PageHeader, so the heading order never skips; its refresh action
 * resets the actions scope and re-reads the tenant, and is natively
 * disabled while either of the two writes is in flight. A host may append
 * its own sections to the main column and its own content to the side
 * column, both of which receive the loaded tenant.
 */

import { useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  ListSkeleton,
  PageHeader,
  SectionCard,
  useWriteFeedback,
} from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { ConsoleLink } from './internal/navigation.js'
import { DefinitionRow, DetailLayout } from './internal/layout.js'
import { isRejectedIdentifier } from './internal/identifier.js'
import { RefreshButton } from '@speed/ui-kit'
import { useConsoleHasFeature } from './internal/features.js'
import {
  consoleRouteById,
  useConsoleRouteTable,
} from './internal/route-table.js'
import { TENANT_QUERY_PARAM } from './internal/tenant-scope.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import {
  pageRefreshRead,
  usePageRefresh,
} from '@speed/ui-kit'
import {
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleTenant } from './operations.js'

/** The roster's closed status set; a value outside it renders the generic label. */
const KNOWN_STATUSES = new Set<string>(['active', 'disabled'])

function statusLabel(status: string, t: (key: string) => string): string {
  return KNOWN_STATUSES.has(status)
    ? t(`tenants.status.${status}`)
    : t('tenants.status.other')
}

/** The sibling administration pages this detail page links to, by declaration id. */
export type ConsoleTenantRelatedPage =
  | 'permissions'
  | 'invitations'
  | 'sso'
  | 'impersonations'

/** The sibling links in the order the related-page section lists them. */
const RELATED_PAGES: readonly {
  readonly id: ConsoleTenantRelatedPage
  readonly labelKey: string
}[] = [
  { id: 'permissions', labelKey: 'tenantDetail.related.permissions' },
  { id: 'invitations', labelKey: 'tenantDetail.related.invitations' },
  { id: 'sso', labelKey: 'tenantDetail.related.sso' },
  { id: 'impersonations', labelKey: 'tenantDetail.related.impersonations' },
]

/**
 * One sibling declaration's published path as a link target. A path that
 * already lies under the console root -- the host mounted the declaration
 * onto its prefixed URL -- is used as published; any other path is a
 * console-relative declaration pattern and is resolved against the root.
 * Without a root nothing resolves: this package's own declarations are
 * console-relative ('/permissions'), so a leading slash alone does not say
 * the path is already at the origin. A path that still has to be joined
 * gives no target, which leaves the link out.
 */
function siblingHref(
  path: string,
  rootHref: string | undefined,
): string | undefined {
  if (rootHref === undefined) {
    return undefined
  }
  const root = rootHref === '/' ? '' : rootHref.replace(/\/+$/, '')
  if (path === root || path.startsWith(`${root}/`)) {
    return path
  }
  return `${root}/${path.replace(/^\/+/, '')}`
}

export interface TenantDetailPageProps {
  /** The tenant to show. */
  readonly id: string
  /** The roster's path, for the breadcrumb and the back link; absent hides them. */
  readonly parentHref?: string
  /**
   * The console's root path, used to resolve a sibling declaration whose
   * published path is console-relative. A path the host already mounted
   * under the root is used as published, so a host that prefixes its
   * declarations costs nothing here. Absent, no sibling link resolves: a
   * console-relative path cannot be told apart from an origin path.
   */
  readonly rootHref?: string
  /**
   * Overrides the href a sibling link points at, keyed by the target's
   * declaration id. The target declaration must still be published with its
   * features present, so an override changes where a link goes, never
   * whether it exists.
   */
  readonly relatedHref?: Partial<Record<ConsoleTenantRelatedPage, string>>
  /**
   * Host sections appended to the main column after the page's own
   * sections, rendered with the loaded tenant.
   */
  readonly sections?: (tenant: ConsoleTenant) => ReactNode
  /**
   * Host content for the side column, rendered with the loaded tenant and
   * above the back link.
   */
  readonly aside?: (tenant: ConsoleTenant) => ReactNode
}

export function TenantDetailPage({
  id,
  parentHref,
  rootHref,
  relatedHref,
  sections,
  aside,
}: TenantDetailPageProps) {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const tenant = operations.useTenant({ id })
  const renameTenant = operations.useSetTenantAlias()
  const setStatus = operations.useSetTenantStatus()
  const [alias, setAlias] = useState('')
  // The actions section is one control group, so its rename and its
  // enable/disable share one scope and one region under the controls. Each
  // landed write confirms and names this tenant; a refusal resolves through
  // the console whitelist.
  const feedback = useWriteFeedback()
  const regionRef = useFailureScroll(feedback.outcome)

  // The refresh action re-reads the tenant -- the page's one body read --
  // and resets the actions scope first: a result written before the re-read
  // would describe the page as it stood then. The typed alias draft is the
  // operator's and stays.
  const pageRefresh = usePageRefresh(
    [pageRefreshRead('tenant-detail', tenant)],
    feedback.reset,
  )

  const aliasRejected = alias !== '' && isRejectedIdentifier(alias)

  // The tenant-addressed pages all read the tenant from the same query
  // parameter (tenantFromLocation seeds their selector), so a link carries
  // this tenant into them instead of landing on an empty selection.
  const tenantParam = `${TENANT_QUERY_PARAM}=${encodeURIComponent(id)}`
  // Each sibling is found by its stable declaration id in the table the
  // shell published: the declaration, not a guessed segment, says where the
  // page lives. A sibling the host narrowed away -- its declaration absent,
  // or one of its features absent -- has no link; an override only replaces
  // the resolved target.
  const routes = useConsoleRouteTable()
  const hasFeature = useConsoleHasFeature()
  const related = RELATED_PAGES.flatMap((page) => {
    const target = consoleRouteById(routes, page.id)
    if (target === undefined) {
      return []
    }
    if (target.requires?.some((feature) => !hasFeature(feature)) === true) {
      return []
    }
    const href = relatedHref?.[page.id] ?? siblingHref(target.path, rootHref)
    if (href === undefined) {
      return []
    }
    return [
      {
        // An override is an arbitrary href and may already carry a query, so
        // the tenant joins with '&' instead of starting a second '?'.
        href: `${href}${href.includes('?') ? '&' : '?'}${tenantParam}`,
        label: t(page.labelKey),
      },
    ]
  })

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
        <SectionCard title={t('tenantDetail.summary')} sx={{ mb: 3 }}>
          <Box component="dl" sx={{ margin: 0 }}>
            <DefinitionRow label={t('tenantDetail.field.id')} value={settled.id} />
            <DefinitionRow label={t('tenantDetail.field.alias')} value={settled.alias} />
            <DefinitionRow
              label={t('tenantDetail.field.status')}
              value={statusLabel(settled.status, t)}
            />
          </Box>
        </SectionCard>

        <SectionCard title={t('tenantDetail.actions.title')} sx={{ mb: 3 }}>
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
              disabled={feedback.pending || aliasRejected}
              loading={feedback.pending}
              onClick={() => {
                void feedback.run(async () => {
                  await renameTenant.mutateAsync({ id: settled.id, alias })
                  // A landed rename clears the draft; a refused one keeps
                  // it so the alias can be corrected.
                  setAlias('')
                  return t('tenantDetail.actions.renamed', { id: settled.id })
                })
              }}
            >
              {t('tenantDetail.actions.renameSubmit')}
            </Button>
            <Button
              type="button"
              variant="outlined"
              disabled={feedback.pending}
              loading={feedback.pending}
              onClick={() => {
                void feedback.run(async () => {
                  await setStatus.mutateAsync({
                    id: settled.id,
                    status: disabled ? 'active' : 'disabled',
                  })
                  return t(
                    disabled
                      ? 'tenantDetail.actions.enabled'
                      : 'tenantDetail.actions.disabled',
                    { id: settled.id },
                  )
                })
              }}
            >
              {disabled
                ? t('tenantDetail.actions.enable')
                : t('tenantDetail.actions.disable')}
            </Button>
          </Box>
          {/* The actions section's one region, directly under the controls
              whose writes report through it. */}
          <Box ref={regionRef}>
            <WriteFeedbackRegion outcome={feedback.outcome} />
          </Box>
        </SectionCard>

        {related.length > 0 && (
          <SectionCard title={t('tenantDetail.related.title')}>
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
          </SectionCard>
        )}

        {/* Host sections follow the page's own, with the loaded tenant. */}
        {sections?.(settled)}
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
            busy={pageRefresh.busy}
            writePending={feedback.pending}
            label={t('common.refresh')}
            onClick={pageRefresh.refresh}
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
          <SectionCard title={t('tenantDetail.field.id')}>
            <Typography variant="body2" sx={{ wordBreak: 'break-all' }}>
              {id}
            </Typography>
            {/* Host side content sits above the back link, once the tenant
                has loaded so it can render the subject it was given. */}
            {rows !== undefined && aside?.(rows)}
            {parentHref !== undefined && (
              <Box sx={{ mt: 2 }}>
                <ConsoleLink href={parentHref}>{t('tenantDetail.back')}</ConsoleLink>
              </Box>
            )}
          </SectionCard>
        }
      />
    </Box>
  )
}
