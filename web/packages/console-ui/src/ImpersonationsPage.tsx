/**
 * ImpersonationsPage: the impersonation-record administration page of the
 * console.
 *
 * It lists the platform's impersonation records -- who is (or was)
 * impersonating whom, in which tenant, and for how long -- and terminates
 * one still in force. The read is GET /auth/admin/impersonations and the
 * termination is DELETE /auth/admin/impersonations/:id; both are
 * operations on the console operations bundle, so the page builds no
 * client and writes no query key.
 *
 * A record is in force while its `ended_at` is the zero instant (the
 * sentinel the view carries); the state column says so and the terminate
 * control is offered only then. The timestamps render in UTC through
 * Intl. The page opens with ui-kit's PageHeader.
 */

import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import {
  AsyncSection,
  DataTable,
  ListSkeleton,
  PageHeader,
  errorCodeOf,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { formatUtc, isZeroInstant } from './internal/datetime.js'
import { InlineError } from './internal/inline-error.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleImpersonation } from './operations.js'

export function ImpersonationsPage() {
  const { t, i18n } = useConsoleUiTranslation()
  const operations = useConsoleOperations()

  const impersonations = operations.useImpersonations()
  const revokeImpersonation = operations.useRevokeImpersonation()

  const columns: DataTableColumn<ConsoleImpersonation>[] = [
    {
      id: 'actor',
      header: t('impersonations.column.actor'),
      cell: (record) => record.actor,
    },
    {
      id: 'target',
      header: t('impersonations.column.target'),
      cell: (record) => record.target,
    },
    {
      id: 'tenant',
      header: t('impersonations.column.tenant'),
      priority: 'medium',
      cell: (record) => record.tenant,
    },
    {
      id: 'started',
      header: t('impersonations.column.started'),
      cell: (record) => formatUtc(record.started_at, i18n.language),
    },
    {
      id: 'ends',
      header: t('impersonations.column.ends'),
      priority: 'low',
      cell: (record) => formatUtc(record.ends_at, i18n.language),
    },
    {
      id: 'state',
      header: t('impersonations.column.state'),
      priority: 'medium',
      cell: (record) =>
        isZeroInstant(record.ended_at) ? (
          <Typography variant="body2" color="warning.main">
            {t('impersonations.inForce')}
          </Typography>
        ) : (
          <Typography variant="body2" color="text.secondary">
            {t('impersonations.ended', { reason: record.end_reason ?? '' })}
          </Typography>
        ),
    },
    {
      id: 'actions',
      header: t('impersonations.column.actions'),
      cell: (record) => (
        <Button
          type="button"
          size="small"
          variant="outlined"
          color="error"
          disabled={!isZeroInstant(record.ended_at) || revokeImpersonation.isPending}
          onClick={() => revokeImpersonation.mutate({ id: record.id })}
        >
          {t('impersonations.terminate')}
        </Button>
      ),
    },
  ]

  const rows = impersonations.data
  const pending = impersonations.isPending && rows === undefined

  return (
    <Box>
      <PageHeader
        title={t('impersonations.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('impersonations.title') },
        ]}
        actions={
          <Button
            type="button"
            variant="outlined"
            size="small"
            onClick={() => void impersonations.refetch()}
          >
            {t('common.refresh')}
          </Button>
        }
        sx={{ mb: 2 }}
      />

      <AsyncSection
        pending={pending}
        payload={rows}
        empty={rows !== undefined && rows.length === 0}
        headingLevel="h2"
        loading={
          <ListSkeleton
            label={t('impersonations.loading')}
            rows={[
              <Skeleton key="a" variant="text" width="60%" />,
              <Skeleton key="b" variant="text" width="44%" />,
            ]}
          />
        }
        errorState={{
          title: t('impersonations.error.title'),
          description: t('impersonations.error.description'),
          retryLabel: t('impersonations.retry'),
          onRetry: () => void impersonations.refetch(),
        }}
        emptyState={{
          title: t('impersonations.empty.title'),
          description: t('impersonations.empty.description'),
        }}
      >
        {(settled) => (
          <DataTable
            rows={settled}
            columns={columns}
            rowKey={(record) => record.id}
          />
        )}
      </AsyncSection>

      <Box sx={{ mt: 2 }}>
        <InlineError
          code={
            revokeImpersonation.isError
              ? errorCodeOf(revokeImpersonation.error)
              : null
          }
        />
      </Box>
    </Box>
  )
}
