import { useCallback, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { focusManager, onlineManager } from '@tanstack/react-query'
import type { UseQueryResult } from '@tanstack/react-query'
import Box from '@mui/material/Box'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import {
  DataTable,
  ListSkeleton,
  LONG_TEXT_COLUMN_MAX_WIDTH,
  PRIMARY_COLUMN_MAX_WIDTH,
  SectionCard,
  useFormatInstant,
} from '@speed/ui-kit'
import type { DataTableColumn, PageRefresh } from '@speed/ui-kit'
import { AdminAsyncSection } from './internal/async-section.js'
import {
  AccountIdentity,
  AuditObjectIdentity,
  isAccountObject,
} from './internal/account-identity.js'
import { AUDIT_RECORD_COLUMN_PRIORITY } from './internal/audit-record-columns.js'
import { RefreshButton } from '@speed/ui-kit'
import { RefreshStatus } from './internal/refresh-status.js'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  pageRefreshRead,
  usePageRefresh,
} from '@speed/ui-kit'
import { useAdminHasFeature } from './internal/features.js'
import { AdminLink } from './internal/navigation.js'
import { useAdminOperations } from './operations.js'
import type { AdminAuditRecord, AdminTenant } from './operations.js'
import { AdminPageHeader } from './internal/page-header.js'

const RECENT_LIMIT = 5

/** The audit action a successful sign-in records (pkg/authn/login). */
const LOGIN_ACTION = 'authn.login'

const HEALTH_WINDOW_MS = 24 * 60 * 60 * 1000

const HEALTH_LIMIT = 100

const AUDIT_SEGMENT = 'audit'

const HEALTH_READ_OPTIONS = {
  keepPrevious: true,
  refetchOnWindowFocus: false,
  refetchOnReconnect: false,
} as const

function useHealthWindow(): {
  readonly since: string
  readonly advance: () => void
} {
  const [start, setStart] = useState(() => Date.now())
  const advance = useCallback(() => {
    setStart((previous) => {
      const now = Date.now()
      return now > previous ? now : previous + 1
    })
  }, [])
  useEffect(() => {
    const stopFocus = focusManager.subscribe((focused) => {
      if (focused) {
        advance()
      }
    })
    const stopOnline = onlineManager.subscribe((online) => {
      if (online) {
        advance()
      }
    })
    return () => {
      stopFocus()
      stopOnline()
    }
  }, [advance])
  const since = useMemo(
    () => new Date(start - HEALTH_WINDOW_MS).toISOString(),
    [start],
  )
  return { since, advance }
}

/** KpiCard renders one KPI tile: a label above a large value. */
export function KpiCard({
  label,
  value,
}: {
  readonly label: string
  readonly value: ReactNode
}) {
  return (
    <Box
      component="li"
      sx={{
        p: 2,
        flex: '1 1 12rem',
        minWidth: 0,
        border: 1,
        borderColor: (theme) => theme.surface.container.borderColor,
        borderRadius: 1,
      }}
    >
      <Typography variant="body2" color="text.secondary" sx={{ mb: 0.5 }}>
        {label}
      </Typography>
      <Typography variant="h4" component="p" sx={{ margin: 0 }}>
        {value}
      </Typography>
    </Box>
  )
}

/** KpiRow lays out cards on a container-driven grid: one column below `sm`,
 * two from `sm`, then `columns` from `md`. */
export function KpiRow({
  columns = 4,
  children,
}: {
  readonly columns?: 2 | 4
  readonly children: ReactNode
}) {
  return (
    <Box sx={{ width: '100%', containerType: 'inline-size' }}>
      <Box
        component="ul"
        sx={{
          listStyle: 'none',
          margin: 0,
          padding: 0,
          display: 'grid',
          gridTemplateColumns: {
            '@': 'minmax(0, 1fr)',
            '@sm': 'repeat(2, minmax(0, 1fr))',
            '@md': `repeat(${columns}, minmax(0, 1fr))`,
          },
          gap: 2,
        }}
      >
        {children}
      </Box>
    </Box>
  )
}

function countLabel(count: number, bounded: boolean): string {
  return bounded ? `${count}+` : String(count)
}

function joinRootHref(
  rootHref: string | undefined,
  path: string,
): string | undefined {
  if (rootHref === undefined) {
    return undefined
  }
  const root = rootHref === '/' ? '' : rootHref.replace(/\/+$/, '')
  return `${root}/${path.replace(/^\/+/, '')}`
}

function KpiSection({
  tenants,
}: {
  readonly tenants: UseQueryResult<readonly AdminTenant[], unknown>
}) {
  const { t } = useAdminUiTranslation()
  const rows = tenants.data
  // isPending alone means "no answer yet": a fetch parked offline by
  // networkMode 'online' must still show the loading branch.
  const pending = tenants.isPending && rows === undefined

  const kpi = (settled: readonly AdminTenant[]) => {
    const active = settled.filter((tenant) => tenant.status === 'active').length
    const disabled = settled.filter(
      (tenant) => tenant.status === 'disabled',
    ).length
    const deleted = settled.filter(
      (tenant) => tenant.status === 'deleted',
    ).length
    return (
      <KpiRow>
        <KpiCard label={t('dashboard.kpi.tenants')} value={settled.length} />
        <KpiCard label={t('dashboard.kpi.active')} value={active} />
        <KpiCard label={t('dashboard.kpi.disabled')} value={disabled} />
        <KpiCard label={t('dashboard.kpi.deleted')} value={deleted} />
      </KpiRow>
    )
  }

  return (
    <AdminAsyncSection
      pending={pending}
      payload={rows}
      error={tenants.error}
      refreshing={tenants.isFetching}
      empty={false}
      headingLevel="h2"
      loading={
        <ListSkeleton
          label={t('dashboard.loading')}
          rows={[<Skeleton key="a" variant="text" width="52%" />]}
        />
      }
      errorState={{
        title: t('dashboard.error.title'),
        description: t('dashboard.error.description'),
        retryLabel: t('dashboard.retry'),
        onRetry: () => void tenants.refetch(),
      }}
    >
      {kpi}
    </AdminAsyncSection>
  )
}

function DashboardFrame({
  tenants,
  pageRefresh,
  onRefresh,
  sections,
  children,
}: {
  readonly tenants: UseQueryResult<readonly AdminTenant[], unknown>
  readonly pageRefresh: PageRefresh
  readonly onRefresh: () => void
  readonly sections?: ReactNode
  readonly children?: ReactNode
}) {
  const { t } = useAdminUiTranslation()
  return (
    <Box>
      <AdminPageHeader
        title={t('dashboard.title')}
        breadcrumbs={[{ label: t('shell.title') }, { label: t('dashboard.title') }]}
        actions={
          <>
            <RefreshStatus
              updatedAt={pageRefresh.updatedAt}
              announcements={pageRefresh.announcements}
            />
            <RefreshButton
              label={t('common.refresh')}
              busy={pageRefresh.busy}
              onClick={onRefresh}
            />
          </>
        }
        sx={{ mb: 2 }}
      />

      <SectionCard sx={{ mb: 3 }}>
        <KpiSection tenants={tenants} />
      </SectionCard>

      {sections}

      {children}
    </Box>
  )
}

function DashboardWithoutAudit({ children }: { readonly children?: ReactNode }) {
  const operations = useAdminOperations()
  const tenants = operations.useTenants()
  const pageRefresh = usePageRefresh(
    [pageRefreshRead('dashboard:tenants', tenants)],
    () => undefined,
  )
  return (
    <DashboardFrame
      tenants={tenants}
      pageRefresh={pageRefresh}
      onRefresh={pageRefresh.refresh}
    >
      {children}
    </DashboardFrame>
  )
}

function DashboardWithAudit({
  rootHref,
  children,
}: {
  readonly rootHref?: string
  readonly children?: ReactNode
}) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const formatTime = useFormatInstant()
  const tenants = operations.useTenants()
  const records = operations.useAuditRecords({ limit: RECENT_LIMIT })

  const healthWindow = useHealthWindow()
  const healthActivity = operations.useAuditRecords(
    { since: healthWindow.since, limit: HEALTH_LIMIT },
    HEALTH_READ_OPTIONS,
  )
  const healthLogins = operations.useAuditRecords(
    { since: healthWindow.since, action: [LOGIN_ACTION], limit: HEALTH_LIMIT },
    HEALTH_READ_OPTIONS,
  )

  const pageRefresh = usePageRefresh(
    [
      pageRefreshRead('dashboard:tenants', tenants),
      pageRefreshRead('dashboard:recent', records),
      pageRefreshRead('dashboard:health-activity', healthActivity, {
        keyDriven: true,
      }),
      pageRefreshRead('dashboard:health-logins', healthLogins, {
        keyDriven: true,
      }),
    ],
    () => undefined,
  )

  const auditDetailBase = joinRootHref(rootHref, AUDIT_SEGMENT)

  const recentColumns: DataTableColumn<AdminAuditRecord>[] = [
    {
      id: 'time',
      header: t('dashboard.recent.column.time'),
      cellWrap: 'nowrap',
      cell: (record) => formatTime(record.occurred_at),
    },
    {
      id: 'action',
      header: t('dashboard.recent.column.action'),
      cellWrap: 'nowrap',
      cellFullValue: (record) => record.action,
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (record) => (
        <Typography variant="body2" noWrap>
          {auditDetailBase === undefined ? (
            record.action
          ) : (
            <AdminLink
              href={`${auditDetailBase}/${encodeURIComponent(record.id)}`}
            >
              {record.action}
            </AdminLink>
          )}
        </Typography>
      ),
    },
    {
      id: 'object',
      header: t('dashboard.recent.column.object'),
      priority: AUDIT_RECORD_COLUMN_PRIORITY.object,
      cellWrap: 'nowrap',
      cellFullValue: (record) =>
        record.object === '' || isAccountObject(record.object)
          ? undefined
          : record.object,
      contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
      cell: (record) => (
        <Typography variant="body2" noWrap>
          <AuditObjectIdentity object={record.object} />
        </Typography>
      ),
    },
    {
      id: 'actor',
      header: t('dashboard.recent.column.actor'),
      priority: AUDIT_RECORD_COLUMN_PRIORITY.actor,
      cellWrap: 'nowrap',
      contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
      cell: (record) => <AccountIdentity account={record.actor} />,
    },
    {
      id: 'outcome',
      header: t('dashboard.recent.column.outcome'),
      priority: AUDIT_RECORD_COLUMN_PRIORITY.outcome,
      cellWrap: 'nowrap',
      cell: (record) => record.outcome,
    },
  ]

  const recordsRows = records.data?.entries
  const recordsPending = records.isPending && recordsRows === undefined

  const activityEntries = healthActivity.data?.entries
  const loginEntries = healthLogins.data?.entries
  const healthPending =
    (healthActivity.isPending && activityEntries === undefined) ||
    (healthLogins.isPending && loginEntries === undefined)
  const healthError = healthActivity.error ?? healthLogins.error
  const health =
    activityEntries !== undefined && loginEntries !== undefined
      ? {
          activity: countLabel(
            activityEntries.length,
            (healthActivity.data?.next ?? '') !== '',
          ),
          logins: countLabel(
            loginEntries.length,
            (healthLogins.data?.next ?? '') !== '',
          ),
        }
      : undefined

  const sections = (
    <>
      <SectionCard title={t('dashboard.health.title')} sx={{ mb: 3 }}>
        <AdminAsyncSection
          pending={healthPending}
          payload={health}
          error={healthError}
          refreshing={healthActivity.isFetching || healthLogins.isFetching}
          empty={false}
          headingLevel="h3"
          loading={
            <ListSkeleton
              label={t('dashboard.health.loading')}
              rows={[<Skeleton key="a" variant="text" width="52%" />]}
            />
          }
          errorState={{
            title: t('dashboard.health.error.title'),
            description: t('dashboard.health.error.description'),
            retryLabel: t('dashboard.retry'),
            onRetry: () => {
              void healthActivity.refetch()
              void healthLogins.refetch()
            },
          }}
        >
          {(settled) => (
            <>
              <KpiRow columns={2}>
                <KpiCard
                  label={t('dashboard.health.logins')}
                  value={settled.logins}
                />
                <KpiCard
                  label={t('dashboard.health.activity')}
                  value={settled.activity}
                />
              </KpiRow>
              <Typography
                variant="body2"
                color="text.secondary"
                sx={{ mt: 1 }}
              >
                {t('dashboard.health.note')}
              </Typography>
            </>
          )}
        </AdminAsyncSection>
      </SectionCard>

      <SectionCard title={t('dashboard.recent.title')} flushContent>
        <AdminAsyncSection
          pending={recordsPending}
          payload={recordsRows}
          error={records.error}
          refreshing={records.isFetching}
          empty={recordsRows !== undefined && recordsRows.length === 0}
          headingLevel="h3"
          loading={
            <ListSkeleton
              label={t('dashboard.recent.loading')}
              rows={[
                <Skeleton key="a" variant="text" width="58%" />,
                <Skeleton key="b" variant="text" width="48%" />,
              ]}
            />
          }
          errorState={{
            title: t('dashboard.recent.error.title'),
            description: t('dashboard.recent.error.description'),
            retryLabel: t('dashboard.retry'),
            onRetry: () => void records.refetch(),
          }}
          emptyState={{
            title: t('dashboard.recent.empty.title'),
            description: t('dashboard.recent.empty.description'),
          }}
        >
          {(settled) => (
            <DataTable
              rows={settled}
              columns={recentColumns}
              rowKey={(record) => record.id}
            />
          )}
        </AdminAsyncSection>
      </SectionCard>
    </>
  )

  return (
    <DashboardFrame
      tenants={tenants}
      pageRefresh={pageRefresh}
      onRefresh={() => {
        healthWindow.advance()
        pageRefresh.refresh()
      }}
      sections={sections}
    >
      {children}
    </DashboardFrame>
  )
}

/** Props for DashboardPage: the console root path and host content. */
export interface DashboardPageProps {
  /** The console root the declaration matched; recent activity rows link to
   * each record's audit detail page below it, an origin root '/' is
   * normalised first, and absent leaves those rows as plain text. */
  readonly rootHref?: string
  /** Host content rendered after the page's own sections. */
  readonly children?: ReactNode
}

/** DashboardPage renders the roster KPIs and, where audit.admin is present,
 * the platform-health row and recent activity. */
export function DashboardPage({ rootHref, children }: DashboardPageProps) {
  const hasFeature = useAdminHasFeature()
  return hasFeature('audit.admin') ? (
    <DashboardWithAudit rootHref={rootHref}>{children}</DashboardWithAudit>
  ) : (
    <DashboardWithoutAudit>{children}</DashboardWithoutAudit>
  )
}
