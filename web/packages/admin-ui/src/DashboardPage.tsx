/**
 * DashboardPage: the console's landing page.
 *
 * The console root renders a dashboard rather than a redirect: a KPI row
 * summarising the roster, a platform-health row, and a list of the most
 * recent audit activity. The administration surfaces declare no statistics
 * endpoint, so every figure is derived from a list read the console already
 * has. The roster read returns the whole roster (a tenant count is its
 * length, and the enabled/disabled split its statuses). The platform-health
 * row is derived from the audit query read, the one read that spans the
 * whole platform: it counts the records of the trailing 24 hours, and the
 * successful sign-ins among them (the audit action authn.login). Those
 * counts come from the newest page of a windowed read, so a page that
 * carries a next cursor reports its count as a lower bound ("N+"). The
 * window start moves forward on the page's refresh action and at the
 * query client's automatic re-read moments -- the tab becoming visible
 * and the connection coming back -- and the numbers already on screen
 * stay until the new window settles. The recent-activity list shows the
 * newest page of records outright, through
 * the same column set and priorities as the audit query page, and the same
 * account identity unit: the account's display name where the lookup
 * answers, the recorded value with an unresolved note where it is
 * refused. When the page is given the console root path, each recent row's
 * action links to that record's audit detail page, carrying the record id
 * the detail route takes. Nothing here invents an aggregate the contract
 * does not carry.
 *
 * The audit-backed half -- the platform-health row and the recent-activity
 * list -- exists only where the process serves audit.admin. A deployment
 * without it serves no audit query, so the page mounts neither section and
 * issues no audit read at all: the gated half is a child component that is
 * only mounted when the feature is present, rather than a set of reads the
 * page skips, so the reads do not exist on that surface. The roster row and
 * any host content still render.
 *
 * Each surface renders through the console async section, so a refused
 * read (403) shows the permission placeholder without a retry and every
 * other failure shows the retryable error placeholder. The page opens
 * with ui-kit's PageHeader.
 */

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
} from '@speed/ui-kit'
import type { DataTableColumn, PageRefresh } from '@speed/ui-kit'
import { AdminAsyncSection } from './internal/async-section.js'
import {
  AccountIdentity,
  AuditObjectIdentity,
  isAccountIdentifierShaped,
} from './internal/account-identity.js'
import { AUDIT_RECORD_COLUMN_PRIORITY } from './internal/audit-record-columns.js'
import { formatInstant } from '@speed/i18n'
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

/** How many of the newest audit records the recent-activity list shows. */
const RECENT_LIMIT = 5

/** The audit action a successful sign-in records (pkg/authn/login). */
const LOGIN_ACTION = 'authn.login'

/** The platform-health window: the trailing 24 hours. */
const HEALTH_WINDOW_MS = 24 * 60 * 60 * 1000

/** The page size of the platform-health reads; a full page reports as a bound. */
const HEALTH_LIMIT = 100

/**
 * The segment the audit pages sit under, below the console root the
 * dashboard was given: a recent-activity row links to
 * `${root}/audit/${record id}`, the path the audit detail declaration
 * matches.
 */
const AUDIT_SEGMENT = 'audit'

/**
 * The behaviour of the platform-health reads: the previous answer stays as
 * placeholder data while the read under a moved window is in flight, and the
 * query client's automatic re-reads of the old key are off because the moved
 * window is what re-reads the row at those moments.
 */
const HEALTH_READ_OPTIONS = {
  keepPrevious: true,
  refetchOnWindowFocus: false,
  refetchOnReconnect: false,
} as const

/**
 * The platform-health window start, as the `since` parameter its reads
 * carry. It moves forward on the page's refresh action and on the two
 * moments the query client re-reads on its own -- the tab becoming visible
 * and the connection coming back. Those signals come from the client's own
 * focus and online managers, the same ones its automatic re-reads listen
 * to, so the page installs no listener of its own. Each move is strictly
 * later than the last, so a signal arriving within the same millisecond as
 * the previous one still changes the reads' key and issues a new read.
 */
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

/**
 * One KPI tile: a label and a large count on a bordered inset. It is not
 * a second surface: a card here would stack another translucent layer
 * inside the section's own, and in a glass theme the inner tile would go
 * almost opaque. The divider border and the shared radius keep the tile
 * distinct without introducing a surface.
 */
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
        // The tile shares the card's own rim, so a theme that gives its
        // containers a visible border (glass does, per mode) gives the
        // tiles the same edge.
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

/**
 * The KPI row: a card grid that answers to its own container width, the
 * layout design's component-level response for card grids. It is one
 * column below `sm`, two columns from `sm`, and `columns` columns from
 * `md` -- the tenant overview's four cards therefore read 2x2 at a 768px
 * overview and one row on a wide console, while the two-card
 * platform-health row is always two-up once the container clears `sm`.
 * The outer box is the container (an element cannot query itself); the
 * list keeps its `ul`/`li` semantics inside it.
 */
export function KpiRow({
  columns = 4,
  children,
}: {
  /** How many cards share a line from the container's `md` tier up. */
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

/**
 * A windowed count label: the plain count, or the count with a trailing "+"
 * when the read answered with a next cursor, so a page-bounded count is shown
 * as the lower bound it is rather than as a total.
 */
function countLabel(count: number, bounded: boolean): string {
  return bounded ? `${count}+` : String(count)
}

/**
 * A sibling path below the console root the page was given. The console may
 * be mounted at the origin, where the matched root path is '/', so that root
 * contributes no prefix; a trailing slash never doubles. Undefined when the
 * page has no root path, which leaves the link out.
 */
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

/** The roster's KPI row, derived from the whole roster read. */
function KpiSection({
  tenants,
}: {
  readonly tenants: UseQueryResult<readonly AdminTenant[], unknown>
}) {
  const { t } = useAdminUiTranslation()
  const rows = tenants.data
  // A load with no data yet -- the first fetch in flight, or one parked by
  // react-query's default networkMode 'online' while offline -- keeps the
  // loading branch: isPending alone tracks "no answer yet".
  const pending = tenants.isPending && rows === undefined

  const kpi = (settled: readonly AdminTenant[]) => {
    const active = settled.filter((tenant) => tenant.status === 'active').length
    const disabled = settled.filter(
      (tenant) => tenant.status === 'disabled',
    ).length
    // A tombstone is part of the roster read: the three state counts partition
    // it, so the deleted count is what keeps them adding up to the total.
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

/**
 * The frame both dashboard variants share: the page header with the refresh
 * action, the roster KPI section, whatever sections the variant adds, and
 * the host's children last.
 */
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

/**
 * The dashboard where the process does not serve audit.admin: only the
 * roster row, so no audit read is issued and neither audit-backed section
 * exists.
 */
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

/**
 * The dashboard where the process serves audit.admin: the roster row plus
 * the platform-health row and the recent-activity list, whose reads live
 * here so they mount only with the feature.
 */
function DashboardWithAudit({
  rootHref,
  children,
}: {
  readonly rootHref?: string
  readonly children?: ReactNode
}) {
  const { t, i18n } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const tenants = operations.useTenants()
  const records = operations.useAuditRecords({ limit: RECENT_LIMIT })

  // The platform-health reads: the trailing 24 hours of audit records, and the
  // successful sign-ins among them. The window start is state, so its query
  // key changes only when the window moves, not every render. The previous
  // answer stays as placeholder data while the read under a new window is in
  // flight, so the numbers on screen do not flicker to a placeholder.
  const healthWindow = useHealthWindow()
  const healthActivity = operations.useAuditRecords(
    { since: healthWindow.since, limit: HEALTH_LIMIT },
    HEALTH_READ_OPTIONS,
  )
  const healthLogins = operations.useAuditRecords(
    { since: healthWindow.since, action: [LOGIN_ACTION], limit: HEALTH_LIMIT },
    HEALTH_READ_OPTIONS,
  )

  // Every read that describes the page's body is part of the refresh. The
  // health reads re-read by moving their key with the window, so the
  // mechanism counts them but issues no refetch of the old key; the roster
  // and the recent-activity list re-read in place.
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

  // The audit detail page sits below the console root under the audit
  // segment; without a root the action stays plain text.
  const auditDetailBase = joinRootHref(rootHref, AUDIT_SEGMENT)

  // The same columns and priorities as the audit query page: the action
  // and its object carry the record's identity, while the actor and the
  // outcome stay visible together at the `md` tier so a failed record is
  // distinguishable on a laptop-width container.
  const recentColumns: DataTableColumn<AdminAuditRecord>[] = [
    {
      id: 'time',
      header: t('dashboard.recent.column.time'),
      cellWrap: 'nowrap',
      cell: (record) =>
        formatInstant(record.occurred_at, i18n.language, { timeZone: 'UTC' }),
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
      // An object shaped like an account id resolves through the same rule as
      // the audit query, and its copy value is withheld; anything else
      // renders as recorded.
      cellFullValue: (record) =>
        record.object === '' || isAccountIdentifierShaped(record.object)
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
      // The actor's identity comes from the account read, so no copy control
      // hands over the opaque id.
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
        // Moving the window changes the health reads' key, which is what
        // re-reads them; the roster and the recent-activity list re-read in
        // place.
        healthWindow.advance()
        pageRefresh.refresh()
      }}
      sections={sections}
    >
      {children}
    </DashboardFrame>
  )
}

export interface DashboardPageProps {
  /**
   * The console root path the dashboard declaration matched. The recent
   * activity's rows link to each record's audit detail page below it; the
   * console may be mounted at the origin, where that root is '/', so it is
   * normalised before the detail path is joined. Absent, the rows stay plain
   * text.
   */
  readonly rootHref?: string
  /** Host content rendered after the page's own sections. */
  readonly children?: ReactNode
}

/**
 * The console landing. It renders the roster row in every deployment; the
 * platform-health row and the recent-activity list exist only where the
 * process serves audit.admin, so the page mounts no audit read otherwise.
 */
export function DashboardPage({ rootHref, children }: DashboardPageProps) {
  const hasFeature = useAdminHasFeature()
  return hasFeature('audit.admin') ? (
    <DashboardWithAudit rootHref={rootHref}>{children}</DashboardWithAudit>
  ) : (
    <DashboardWithoutAudit>{children}</DashboardWithoutAudit>
  )
}
