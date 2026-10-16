/**
 * DashboardPage: the console's landing page.
 *
 * The console root renders a dashboard rather than a redirect: a KPI row
 * summarising the platform, and a list of the most recent audit activity.
 * The administration surfaces declare no statistics endpoint, so the
 * counts are derived from the list reads the console already has: the
 * roster read returns the whole roster (a tenant count is its length, and
 * the enabled/disabled split its statuses), and the audit query read
 * returns the newest page of records, which the recent-activity list
 * shows. Nothing here invents an aggregate the contract does not carry.
 *
 * Each surface renders through the console async section, so a refused
 * read (403) shows the permission placeholder without a retry and every
 * other failure shows the retryable error placeholder. The page opens
 * with ui-kit's PageHeader.
 */

import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Paper from '@mui/material/Paper'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import { DataTable, ListSkeleton, PageHeader } from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { formatUtc } from './internal/datetime.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleAuditRecord, ConsoleTenant } from './operations.js'

/** How many of the newest audit records the recent-activity list shows. */
const RECENT_LIMIT = 5

/** One KPI card: a label and a large count. */
function KpiCard({ label, value }: { readonly label: string; readonly value: number }) {
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

export function DashboardPage() {
  const { t, i18n } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const tenants = operations.useTenants()
  const records = operations.useAuditRecords({ limit: RECENT_LIMIT })

  const kpi = (settled: readonly ConsoleTenant[]) => {
    const active = settled.filter((tenant) => tenant.status === 'active').length
    const disabled = settled.filter((tenant) => tenant.status === 'disabled').length
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
        <KpiCard label={t('dashboard.kpi.tenants')} value={settled.length} />
        <KpiCard label={t('dashboard.kpi.active')} value={active} />
        <KpiCard label={t('dashboard.kpi.disabled')} value={disabled} />
      </Box>
    )
  }

  const recentColumns: DataTableColumn<ConsoleAuditRecord>[] = [
    {
      id: 'time',
      header: t('dashboard.recent.column.time'),
      cell: (record) => formatUtc(record.occurred_at, i18n.language) ?? '',
    },
    {
      id: 'action',
      header: t('dashboard.recent.column.action'),
      cell: (record) => record.action,
    },
    {
      id: 'outcome',
      header: t('dashboard.recent.column.outcome'),
      priority: 'medium',
      cell: (record) => record.outcome,
    },
    {
      id: 'actor',
      header: t('dashboard.recent.column.actor'),
      priority: 'low',
      cell: (record) => record.actor,
    },
  ]

  const tenantsRows = tenants.data
  const recordsRows = records.data?.entries
  const tenantsPending = tenants.isPending && tenantsRows === undefined
  const recordsPending = records.isPending && recordsRows === undefined

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
            onClick={() => {
              void tenants.refetch()
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

      <Box component="section">
        <Typography component="h2" variant="h6" sx={{ mb: 1 }}>
          {t('dashboard.recent.title')}
        </Typography>
        <ConsoleAsyncSection
          pending={recordsPending}
          payload={recordsRows}
          error={records.error}
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
