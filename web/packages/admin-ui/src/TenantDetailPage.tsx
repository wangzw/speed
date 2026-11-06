/**
 * TenantDetailPage: one tenant's administration detail.
 *
 * The roster's row-level enable/disable control lives on the roster page; this
 * page is the tenant in full -- its identifier, alias, organization profile
 * and status read from GET /api/v1/system/tenants/:id, with the same
 * enable/disable write gathered into the main column. It is a detail page: a
 * main column carrying the subject and its controls, beside a fixed-width
 * side column carrying the tenant's metadata and a way back to the roster.
 * An alias is immutable after creation, so it is read-only and there is no
 * rename control. The summary shows the profile's four descriptive fields
 * and the creation time; a profile card below the status control writes
 * those four fields through the same shared form the organization surface
 * uses. The status and profile are two control groups with two feedback
 * scopes, each region directly under its own control. The lifecycle group
 * below them renders only when it has a control to show -- the caller holds
 * tenant.manage and the subject is a live business organization -- so it
 * never leaves an empty card behind; its landed-write confirmation is a
 * page-level region, because a deletion turns the read into a tombstone and
 * withdraws the group the confirmation would otherwise sit in.
 *
 * The read is an operation on the console operations bundle, so the page
 * builds no client and writes no query key. The status renders from the
 * roster's own closed spelling (active / disabled / deleted); a value
 * outside it renders the generic label, never a raw value. A disabled
 * tenant also shows who disabled it (an operator or the account's own
 * state). A deleted tenant is a tombstone: its state is terminal, so the
 * page renders no write control at all. A refused read settles
 * into a permission placeholder (403) or the retryable error placeholder
 * (any other failure). The enable/disable reports through a feedback scope
 * whose region sits directly under its control: each landed write confirms
 * and names this tenant, and a refusal resolves the answer's code through
 * the admin-ui whitelist.
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
 * disabled while the write is in flight. A host may append
 * its own sections to the main column and its own content to the side
 * column, both of which receive the loaded tenant.
 */

import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import { usePermission } from '@speed/auth-core'
import { formatInstant } from '@speed/i18n'
import {
  ListSkeleton,
  SectionCard,
  useWriteFeedback,
} from '@speed/ui-kit'
import { AdminAsyncSection } from './internal/async-section.js'
import { AdminLink } from './internal/navigation.js'
import { DefinitionRow, DetailLayout } from './internal/layout.js'
import { RefreshButton } from '@speed/ui-kit'
import { useAdminHasFeature } from './internal/features.js'
import {
  adminRouteById,
  useAdminRouteTable,
} from './internal/route-table.js'
import { TENANT_QUERY_PARAM } from './internal/tenant-scope.js'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  pageRefreshRead,
  usePageRefresh,
} from '@speed/ui-kit'
import {
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { useAdminOperations } from './operations.js'
import type { AdminTenant } from './operations.js'
import { AdminPageHeader } from './internal/page-header.js'
import {
  isPersonalTenant,
  TenantLifecycleActions,
} from './internal/tenant-lifecycle.js'
import { TenantProfileForm } from './TenantProfileForm.js'
import {
  displayNameOf,
  profileFieldsOf,
} from './TenantProfileFields.js'
import type { TenantProfileFields } from './TenantProfileFields.js'

/** The roster's closed status set; a value outside it renders the generic label. */
const KNOWN_STATUSES = new Set<string>(['active', 'disabled', 'deleted'])

function statusLabel(status: string, t: (key: string) => string): string {
  return KNOWN_STATUSES.has(status)
    ? t(`tenants.status.${status}`)
    : t('tenants.status.other')
}

/** Who disabled the tenant, from the roster's closed source set. Only called
 * for a disabled tenant: the source is absent on every other state. */
function disabledByLabel(
  source: string | undefined,
  t: (key: string) => string,
): string {
  return source === 'account'
    ? t('tenants.disabledBy.account')
    : t('tenants.disabledBy.operator')
}

/** The sibling administration pages this detail page links to, by declaration id. */
export type AdminTenantRelatedPage =
  | 'permissions'
  | 'invitations'
  | 'sso'
  | 'impersonations'

/** The sibling links in the order the related-page section lists them. */
const RELATED_PAGES: readonly {
  readonly id: AdminTenantRelatedPage
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
  readonly relatedHref?: Partial<Record<AdminTenantRelatedPage, string>>
  /**
   * Host sections appended to the main column after the page's own
   * sections, rendered with the loaded tenant.
   */
  readonly sections?: (tenant: AdminTenant) => ReactNode
  /**
   * Host content for the side column, rendered with the loaded tenant and
   * above the back link.
   */
  readonly aside?: (tenant: AdminTenant) => ReactNode
}

export function TenantDetailPage({
  id,
  parentHref,
  rootHref,
  relatedHref,
  sections,
  aside,
}: TenantDetailPageProps) {
  const { t, i18n } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const tenant = operations.useTenant({ id })
  const setStatus = operations.useSetTenantStatus()
  // The status control reports through its own scope and region. The profile
  // card is a second control group with its own scope, held inside
  // TenantProfileCard so the two regions never sit in one component.
  const statusFeedback = useWriteFeedback()
  const statusRegionRef = useFailureScroll(statusFeedback.outcome)
  // The lifecycle group reports its landed writes to a page-level scope:
  // a deletion turns this page into a tombstone and withdraws the control
  // group, so its confirmation cannot live inside the group.
  const lifecycleFeedback = useWriteFeedback()
  // The lifecycle group renders no control without tenant.manage, and never
  // on a personal organization or a tombstone. The page decides with the
  // same rule, so a group with nothing to show leaves no empty card behind.
  const canManageTenant = usePermission('system', 'tenant.manage')
  // A refresh remounts the profile card through this key: its draft and its
  // own feedback are withdrawn together with the re-read.
  const [profileEpoch, setProfileEpoch] = useState(0)

  // The refresh action re-reads the tenant -- the page's one body read --
  // and resets the status and lifecycle scopes first: a result written
  // before the re-read would describe the page as it stood then.
  const pageRefresh = usePageRefresh(
    [pageRefreshRead('tenant-detail', tenant)],
    () => {
      statusFeedback.reset()
      lifecycleFeedback.reset()
      setProfileEpoch((epoch) => epoch + 1)
    },
  )

  // The tenant-addressed pages all read the tenant from the same query
  // parameter (tenantFromLocation seeds their selector), so a link carries
  // this tenant into them instead of landing on an empty selection.
  const tenantParam = `${TENANT_QUERY_PARAM}=${encodeURIComponent(id)}`
  // Each sibling is found by its stable declaration id in the table the
  // shell published: the declaration, not a guessed segment, says where the
  // page lives. A sibling the host narrowed away -- its declaration absent,
  // or one of its features absent -- has no link; an override only replaces
  // the resolved target.
  const routes = useAdminRouteTable()
  const hasFeature = useAdminHasFeature()
  const related = RELATED_PAGES.flatMap((page) => {
    const target = adminRouteById(routes, page.id)
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

  const main = (settled: AdminTenant) => {
    const disabled = settled.status === 'disabled'
    // A tombstone is read-only: deletion is terminal, so the page renders
    // the tenant and no write control at all.
    const deleted = settled.status === 'deleted'
    // The lifecycle group renders its entries only for a caller holding
    // tenant.manage on a live business organization. The page asks the same
    // question the group does, so the card and its region exist only when
    // the group will render inside them.
    const lifecycle = canManageTenant && !isPersonalTenant(settled.id)
    return (
      <>
        <SectionCard title={t('tenantDetail.summary')} sx={{ mb: 3 }}>
          <Box component="dl" sx={{ margin: 0 }}>
            <DefinitionRow label={t('tenantDetail.field.id')} value={settled.id} />
            <DefinitionRow label={t('tenantDetail.field.alias')} value={settled.alias} />
            <DefinitionRow
              label={t('tenantProfile.field.displayName')}
              value={displayNameOf(settled.displayName, settled.alias)}
            />
            <DefinitionRow
              label={t('tenantDetail.field.status')}
              value={statusLabel(settled.status, t)}
            />
            {disabled && (
              <DefinitionRow
                label={t('tenantDetail.field.disabledBy')}
                value={disabledByLabel(settled.disabledBy, t)}
              />
            )}
            <DefinitionRow
              label={t('tenantProfile.field.description')}
              value={settled.description || t('common.emptyValue')}
            />
            <DefinitionRow
              label={t('tenantProfile.field.contactEmail')}
              value={settled.contactEmail || t('common.emptyValue')}
            />
            <DefinitionRow
              label={t('tenantProfile.field.website')}
              value={settled.website || t('common.emptyValue')}
            />
            <DefinitionRow
              label={t('tenantProfile.field.createdAt')}
              value={formatInstant(settled.createdAt, i18n.language, {
                timeZone: 'UTC',
              })}
            />
          </Box>
        </SectionCard>

        {!deleted && (
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
            <Button
              type="button"
              variant="outlined"
              disabled={statusFeedback.pending}
              loading={statusFeedback.pending}
              onClick={() => {
                void statusFeedback.run(async () => {
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
          {/* The actions section's one region, directly under the control
              whose write reports through it. */}
          <Box ref={statusRegionRef}>
            <WriteFeedbackRegion outcome={statusFeedback.outcome} />
          </Box>
        </SectionCard>
        )}

        {!deleted && lifecycle && (
          <TenantLifecycleCard
            tenant={settled}
            onSuccess={lifecycleFeedback.succeed}
          />
        )}

        {!deleted && (
          <TenantProfileCard
            key={`${settled.id}:${profileEpoch}`}
            tenant={settled}
          />
        )}

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
                  <AdminLink href={link.href}>{link.label}</AdminLink>
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
      <AdminPageHeader
        title={t('tenantDetail.title', { id })}
        breadcrumbs={crumbs}
        actions={
          <RefreshButton
            busy={pageRefresh.busy}
            writePending={statusFeedback.pending}
            label={t('common.refresh')}
            onClick={pageRefresh.refresh}
          />
        }
        sx={{ mb: 2 }}
      />

      {/* The lifecycle group's landed-write confirmation lives at page
          level: a deletion withdraws the group with the write's own effect,
          so a result rendered inside it would flash and vanish. Its refusals
          stay beside the controls, inside the group. */}
      <WriteFeedbackRegion outcome={lifecycleFeedback.outcome} />

      <DetailLayout
        main={
          <AdminAsyncSection
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
          </AdminAsyncSection>
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
                <AdminLink href={parentHref}>{t('tenantDetail.back')}</AdminLink>
              </Box>
            )}
          </SectionCard>
        }
      />
    </Box>
  )
}

/**
 * The platform detail's lifecycle card: the transfer and delete control
 * group in its own section. It is a component of its own so this control
 * group's result never shares a region with the status control above it.
 *
 * The page renders the card only when the group has entries to show, and it
 * owns the success region (pushed up through onSuccess) because a deletion
 * withdraws this card; a refusal stays inside the group, beside the controls
 * that were refused.
 */
function TenantLifecycleCard({
  tenant,
  onSuccess,
}: {
  readonly tenant: AdminTenant
  readonly onSuccess: (message: string) => void
}) {
  const { t } = useAdminUiTranslation()
  return (
    <SectionCard
      title={t('tenantLifecycle.sectionTitle')}
      description={t('tenantLifecycle.sectionDescription')}
      sx={{ mb: 3 }}
    >
      <TenantLifecycleActions tenant={tenant} onSuccess={onSuccess} />
    </SectionCard>
  )
}

/**
 * The platform detail's profile card: the four descriptive fields, with its
 * own write scope and its region directly under the form. The card is a
 * component of its own so this control group's result never shares a region
 * with the status control above it.
 *
 * The draft starts from the settled tenant. A save invalidation re-reads the
 * tenant; when the answer's values changed the draft adopts them, while a
 * background refetch carrying the same values leaves an edit in progress
 * alone. The host remounts the card on a page refresh, so a refresh withdraws
 * both the draft and its result.
 */
function TenantProfileCard({ tenant }: { readonly tenant: AdminTenant }) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const setProfile = operations.useSetTenantProfile()
  const feedback = useWriteFeedback()
  const regionRef = useFailureScroll(feedback.outcome)
  const [draft, setDraft] = useState<TenantProfileFields>(() =>
    profileFieldsOf(tenant),
  )
  const appliedSignature = useRef(JSON.stringify(profileFieldsOf(tenant)))
  useEffect(() => {
    const signature = JSON.stringify(profileFieldsOf(tenant))
    if (appliedSignature.current === signature) {
      return
    }
    appliedSignature.current = signature
    setDraft(profileFieldsOf(tenant))
  }, [tenant])

  return (
    <SectionCard
      title={t('tenantProfile.form.title')}
      description={t('tenantProfile.form.description')}
      sx={{ mb: 3 }}
    >
      <TenantProfileForm
        value={draft}
        onChange={setDraft}
        onSubmit={(next) => {
          void feedback.run(async () => {
            await setProfile.mutateAsync({ id: tenant.id, fields: next })
            return t('tenantProfile.form.savedNamed', {
              name: displayNameOf(next.displayName, tenant.alias),
            })
          })
        }}
        saving={feedback.pending}
        alias={tenant.alias}
      />
      <Box ref={regionRef} sx={{ mt: 2 }}>
        <WriteFeedbackRegion outcome={feedback.outcome} />
      </Box>
    </SectionCard>
  )
}
