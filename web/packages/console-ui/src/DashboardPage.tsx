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
 * recent-activity list shows the newest page of records outright, through
 * the same column set and priorities as the audit query page, and the same
 * account identity unit: the account's display name where the lookup
 * answers, the recorded value with an unresolved note where it is
 * refused. Nothing here invents an aggregate the contract does not carry.
 *
 * Each surface renders through the console async section, so a refused
 * read (403) shows the permission placeholder without a retry and every
 * other failure shows the retryable error placeholder. The page opens
 * with ui-kit's PageHeader.
 */

import { useMemo } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Paper from '@mui/material/Paper'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import {
  DataTable,
  ListSkeleton,
  PageHeader,
  PRIMARY_COLUMN_CONTENT_MAX_WIDTH,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { AccountIdentity } from './internal/account-identity.js'
import { AUDIT_RECORD_COLUMN_PRIORITY } from './internal/audit-record-columns.js'
import { formatUtc } from './internal/datetime.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleAuditRecord, ConsoleTenant } from './operations.js'

/** How many of the newest audit records the recent-activity list shows. */
const RECENT_LIMIT = 5

/** The audit action a successful sign-in records (pkg/authn/login). */
const LOGIN_ACTION = 'authn.login'

/** The platform-health window: the trailing 24 hours. */
const HEALTH_WINDOW_MS = 24 * 60 * 60 * 1000

/** The page size of the platform-health reads; a full page reports as a bound. */
const HEALTH_LIMIT = 100

/** One KPI card: a label and a large count. */
function KpiCard({ label, value }: { readonly label: string; readonly value: ReactNode }) {
  return (
    <Paper
      component="li"
      variant="outlined"
      sx={{ p: 2, flex: '1 1 12rem', minWidth: 0 }}
    >
      <Typography variant="body2" color="text.secondary" sx={{ mb: 0.5 }}>
        {label}
      </Typography>
      <Typography variant="h4" component="p" sx={{ margin: 0 }}>
        {value}
      </Typography>
    </Paper>
  )
}

/**
 * The KPI row: the list of cards, wrapping when the row itself runs out of
 * width. The wrap is intrinsic -- each card's `flex-basis` is a fixed
 * length and flex lines break against the row's own available width -- so
 * the row follows its container without a breakpoint or a container query.
 */
function KpiRow({ children }: { readonly children: ReactNode }) {
  return (
    <Box
      component="ul"
      sx={{
        listStyle: 'none',
        margin: 0,
        padding: 0,
        display: 'flex',
        flexWrap: 'wrap',
        gap: 2,
      }}
    >
      {children}
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

export function DashboardPage() {
  const { t, i18n } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const tenants = operations.useTenants()
  const records = operations.useAuditRecords({ limit: RECENT_LIMIT })

  // The platform-health reads: the trailing 24 hours of audit records, and the
  // successful sign-ins among them. The window instant is computed once per
  // mount, so the query key (which carries it) does not change every render.
  const since = useMemo(
    () => new Date(Date.now() - HEALTH_WINDOW_MS).toISOString(),
    [],
  )
  const healthActivity = operations.useAuditRecords({
    since,
    limit: HEALTH_LIMIT,
  })
  const healthLogins = operations.useAuditRecords({
    since,
    action: [LOGIN_ACTION],
    limit: HEALTH_LIMIT,
  })

  const kpi = (settled: readonly ConsoleTenant[]) => {
    const active = settled.filter((tenant) => tenant.status === 'active').length
    const disabled = settled.filter((tenant) => tenant.status === 'disabled').length
    return (
      <KpiRow>
        <KpiCard label={t('dashboard.kpi.tenants')} value={settled.length} />
        <KpiCard label={t('dashboard.kpi.active')} value={active} />
        <KpiCard label={t('dashboard.kpi.disabled')} value={disabled} />
      </KpiRow>
    )
  }

  // The same columns and priorities as the audit query page: the action
  // and its object carry the record's identity, while the actor and the
  // outcome stay visible together at the `md` tier so a failed record is
  // distinguishable on a laptop-width container.
  const recentColumns: DataTableColumn<ConsoleAuditRecord>[] = [
    {
      id: 'time',
      header: t('dashboard.recent.column.time'),
      cellWrap: 'nowrap',
      cell: (record) => formatUtc(record.occurred_at, i18n.language) ?? '',
    },
    {
      id: 'action',
      header: t('dashboard.recent.column.action'),
      cellWrap: 'nowrap',
      cellFullValue: (record) => record.action,
      contentMaxWidth: PRIMARY_COLUMN_CONTENT_MAX_WIDTH,
      cell: (record) => (
        <Typography variant="body2" noWrap>
          {record.action}
        </Typography>
      ),
    },
    {
      id: 'object',
      header: t('dashboard.recent.column.object'),
      priority: AUDIT_RECORD_COLUMN_PRIORITY.object,
      cellWrap: 'nowrap',
      cellFullValue: (record) =>
        record.object === '' ? undefined : record.object,
      contentMaxWidth: PRIMARY_COLUMN_CONTENT_MAX_WIDTH,
      cell: (record) => (
        <Typography variant="body2" noWrap>
          {record.object}
        </Typography>
      ),
    },
    {
      id: 'actor',
      header: t('dashboard.recent.column.actor'),
      priority: AUDIT_RECORD_COLUMN_PRIORITY.actor,
      cellWrap: 'nowrap',
      cellFullValue: (record) =>
        record.actor === '' ? undefined : record.actor,
      contentMaxWidth: PRIMARY_COLUMN_CONTENT_MAX_WIDTH,
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

  const tenantsRows = tenants.data
  const recordsRows = records.data?.entries
  const tenantsPending = tenants.isPending && tenantsRows === undefined
  const recordsPending = records.isPending && recordsRows === undefined

  const activityEntries = healthActivity.data?.entries
  const loginEntries = healthLogins.data?.entries
  const refreshing =
    tenants.isFetching ||
    healthActivity.isFetching ||
    healthLogins.isFetching ||
    records.isFetching
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

  return (
    <Box>
      <PageHeader
        title={t('dashboard.title')}
        breadcrumbs={[{ label: t('shell.title') }, { label: t('dashboard.title') }]}
        actions={
          <Button
            type="button"
            variant="outlined"
            size="small"
            disabled={refreshing}
            loading={refreshing}
            onClick={() => {
              void tenants.refetch()
              void healthActivity.refetch()
              void healthLogins.refetch()
              void records.refetch()
            }}
          >
            {t('common.refresh')}
          </Button>
        }
        sx={{ mb: 2 }}
      />

      <Box component="section" sx={{ mb: 3 }}>
        <ConsoleAsyncSection
          pending={tenantsPending}
          payload={tenantsRows}
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
        </ConsoleAsyncSection>
      </Box>

      <Box component="section" sx={{ mb: 3 }}>
        <Typography component="h2" variant="h6" sx={{ mb: 1 }}>
          {t('dashboard.health.title')}
        </Typography>
        <ConsoleAsyncSection
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
              <KpiRow>
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
        </ConsoleAsyncSection>
      </Box>

      <Box component="section">
        <Typography component="h2" variant="h6" sx={{ mb: 1 }}>
          {t('dashboard.recent.title')}
        </Typography>
        <ConsoleAsyncSection
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
        </ConsoleAsyncSection>
      </Box>
    </Box>
  )
}
