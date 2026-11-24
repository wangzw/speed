/**
 * TenantDetailPage shows one tenant in full: its profile and status in a main
 * column with the status and profile writes, and its metadata and a back link
 * in a side column. A deleted tenant is a tombstone with no write control.
 */

import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Skeleton from '@mui/material/Skeleton'
import Switch from '@mui/material/Switch'
import Typography from '@mui/material/Typography'
import { usePermission } from '@speed/auth-core'
import {
  ListSkeleton,
  SectionCard,
  useActivationGuard,
  useFormatInstant,
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
import { useSectionHeadingFocus, removedControlHeldFocus } from './internal/section-heading-focus.js'
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

const KNOWN_STATUSES = new Set<string>(['active', 'disabled', 'deleted'])

function statusLabel(status: string, t: (key: string) => string): string {
  return KNOWN_STATUSES.has(status)
    ? t(`tenants.status.${status}`)
    : t('tenants.status.other')
}

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

const RELATED_PAGES: readonly {
  readonly id: AdminTenantRelatedPage
  readonly labelKey: string
}[] = [
  { id: 'permissions', labelKey: 'tenantDetail.related.permissions' },
  { id: 'invitations', labelKey: 'tenantDetail.related.invitations' },
  { id: 'sso', labelKey: 'tenantDetail.related.sso' },
  { id: 'impersonations', labelKey: 'tenantDetail.related.impersonations' },
]

// A path already under the root is used as published; otherwise it is joined to
// the root. Without a root nothing resolves.
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

/** Props of TenantDetailPage: the tenant to read and the host's slots around it. */
export interface TenantDetailPageProps {
  /** The tenant's name: what the request surface addresses it by. */
  readonly name: string
  /** The roster's path, for the breadcrumb and the back link; absent hides them. */
  readonly parentHref?: string
  /**
   * The console's root path, used to resolve a console-relative sibling path.
   * A path already under the root is used as published; absent, no sibling
   * link resolves.
   */
  readonly rootHref?: string
  /**
   * Overrides the href a sibling link points at, keyed by declaration id. The
   * target must still be published with its features present, so an override
   * changes where a link goes, never whether it exists.
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

/**
 * TenantDetailPage reads the tenant by name and renders its summary, status
 * control, profile form, lifecycle controls and sibling links.
 */
export function TenantDetailPage({
  name,
  parentHref,
  rootHref,
  relatedHref,
  sections,
  aside,
}: TenantDetailPageProps) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const formatTime = useFormatInstant()
  const tenant = operations.useTenant({ name })
  const setStatus = operations.useSetTenantStatus()
  const statusFeedback = useWriteFeedback()
  const statusRegionRef = useFailureScroll(statusFeedback.outcome)
  // The switch stays focusable while its write is in flight: a natively disabled
  // control would drop the focus the activation gave it.
  const onChangeStatus = useActivationGuard(
    () => {
      const settled = tenant.data
      if (settled === undefined) {
        return
      }
      const disabled = settled.status === 'disabled'
      void statusFeedback.run(async () => {
        await setStatus.mutateAsync({
          name: settled.name,
          status: disabled ? 'active' : 'disabled',
        })
        return t(
          disabled
            ? 'tenantDetail.actions.enabled'
            : 'tenantDetail.actions.disabled',
          { name: settled.name },
        )
      })
    },
    { busy: statusFeedback.pending },
  )
  // A deletion withdraws the lifecycle group, so its confirmation lives at page level.
  const lifecycleFeedback = useWriteFeedback()
  // The card exists only when the group will render inside it: tenant.manage on
  // a live business organization.
  const canManageTenant = usePermission('system', 'tenant.manage')
  // A refresh remounts the profile card, withdrawing its draft and result.
  const [profileEpoch, setProfileEpoch] = useState(0)
  // A landed deletion withdraws the lifecycle card; the summary heading takes focus.
  const summaryFocus = useSectionHeadingFocus()
  const deletionLanded = useRef(false)
  const tenantStatus = tenant.data?.status
  useEffect(() => {
    if (!deletionLanded.current) {
      return
    }
    if (tenantStatus !== 'deleted') {
      return
    }
    deletionLanded.current = false
    if (removedControlHeldFocus()) {
      summaryFocus.focusHeading()
    }
  }, [tenantStatus, summaryFocus.focusHeading])

  const pageRefresh = usePageRefresh(
    [pageRefreshRead('tenant-detail', tenant)],
    () => {
      statusFeedback.reset()
      lifecycleFeedback.reset()
      setProfileEpoch((epoch) => epoch + 1)
    },
  )

  // The tenant-addressed pages read the same tenant query parameter.
  const tenantParam = `${TENANT_QUERY_PARAM}=${encodeURIComponent(name)}`
  // Each sibling is found by its stable declaration id; a declaration the host
  // narrowed away has no link, and an override only replaces the target.
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
        // An override may already carry a query, so join with '&' in that case.
        href: `${href}${href.includes('?') ? '&' : '?'}${tenantParam}`,
        label: t(page.labelKey),
      },
    ]
  })

  const tenantLabel = displayNameOf(tenant.data?.display_name ?? '', name)

  const crumbs = [
    { label: t('shell.title') },
    ...(parentHref === undefined
      ? [{ label: t('tenants.title') }]
      : [{ label: t('tenants.title'), href: parentHref }]),
    { label: t('tenantDetail.title', { name: tenantLabel }) },
  ]

  const main = (settled: AdminTenant) => {
    const disabled = settled.status === 'disabled'
    // A tombstone is terminal: no write control at all.
    const deleted = settled.status === 'deleted'
    const lifecycle = canManageTenant && !isPersonalTenant(settled.kind)
    return (
      <>
        <SectionCard
          ref={summaryFocus.sectionRef}
          title={t('tenantDetail.summary')}
          sx={{ mb: 3 }}
        >
          <Box component="dl" sx={{ margin: 0 }}>
            <DefinitionRow label={t('tenantDetail.field.name')} value={settled.name} />
            <DefinitionRow
              label={t('tenantProfile.field.displayName')}
              value={displayNameOf(settled.display_name, settled.name)}
            />
            <DefinitionRow
              label={t('tenantDetail.field.status')}
              value={statusLabel(settled.status, t)}
            />
            {disabled && (
              <DefinitionRow
                label={t('tenantDetail.field.disabledBy')}
                value={disabledByLabel(settled.disabled_by, t)}
              />
            )}
            <DefinitionRow
              label={t('tenantProfile.field.description')}
              value={settled.description || t('common.emptyValue')}
            />
            <DefinitionRow
              label={t('tenantProfile.field.contactEmail')}
              value={settled.contact_email || t('common.emptyValue')}
            />
            <DefinitionRow
              label={t('tenantProfile.field.website')}
              value={settled.website || t('common.emptyValue')}
            />
            <DefinitionRow
              label={t('tenantProfile.field.createdAt')}
              value={formatTime(settled.created_at)}
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
            <Switch
              checked={!disabled}
              slotProps={{
                input: {
                  'aria-label': disabled
                    ? t('tenantDetail.actions.enable')
                    : t('tenantDetail.actions.disable'),
                  'aria-busy': statusFeedback.pending ? 'true' : undefined,
                  'aria-disabled': statusFeedback.pending ? 'true' : undefined,
                },
              }}
              onChange={onChangeStatus}
            />
          </Box>
          <Box ref={statusRegionRef}>
            <WriteFeedbackRegion outcome={statusFeedback.outcome} />
          </Box>
        </SectionCard>
        )}

        {!deleted && lifecycle && (
          <TenantLifecycleCard
            tenant={settled}
            onSuccess={lifecycleFeedback.succeed}
            onDeleted={() => {
              deletionLanded.current = true
            }}
          />
        )}

        {!deleted && (
          <TenantProfileCard
            key={`${settled.name}:${profileEpoch}`}
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

        {sections?.(settled)}
      </>
    )
  }

  const rows = tenant.data
  const pending = tenant.isPending && rows === undefined

  return (
    <Box>
      <AdminPageHeader
        title={t('tenantDetail.title', { name: tenantLabel })}
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

      {/* A deletion withdraws the group, so its confirmation lives at page level. */}
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
          <SectionCard title={t('tenantDetail.field.name')}>
            <Typography variant="body2" sx={{ wordBreak: 'break-all' }}>
              {name}
            </Typography>
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

// Its own section, so the lifecycle result never shares a region with the status
// control; a refusal stays inside the group.
function TenantLifecycleCard({
  tenant,
  onSuccess,
  onDeleted,
}: {
  readonly tenant: AdminTenant
  readonly onSuccess: (message: string) => void
  readonly onDeleted: (name: string) => void
}) {
  const { t } = useAdminUiTranslation()
  return (
    <SectionCard
      title={t('tenantLifecycle.sectionTitle')}
      description={t('tenantLifecycle.sectionDescription')}
      sx={{ mb: 3 }}
    >
      <TenantLifecycleActions
        tenant={tenant}
        onSuccess={onSuccess}
        onDeleted={onDeleted}
      />
    </SectionCard>
  )
}

// Its own write scope, so its result never shares a region with the status control.
// A settled value that changed replaces the draft; a same-value refetch leaves an
// edit alone.
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
            await setProfile.mutateAsync({ name: tenant.name, fields: next })
            return t('tenantProfile.form.savedNamed', {
              name: displayNameOf(next.displayName, tenant.name),
            })
          })
        }}
        saving={feedback.pending}
        name={tenant.name}
      />
      <Box ref={regionRef} sx={{ mt: 2 }}>
        <WriteFeedbackRegion outcome={feedback.outcome} />
      </Box>
    </SectionCard>
  )
}
