/**
 * AuditPage: the audit-query page of the console.
 *
 * It reads the audit journal's records -- the platform's append-only
 * trail, whose rows span every tenant -- and renders them newest first as
 * a table (time, action, outcome, actor and object), through ui-kit's
 * DataTable; the columns shed the least important first as the viewport
 * narrows. The read surface carries no export: exporting is another
 * permission and another module's to carry, so this page only lists.
 *
 * The read is an operation on the console operations bundle -- the host
 * binds the generated /audit/admin operation to it -- so the page builds
 * no client and writes no query key. A read that fails settles into the
 * retryable error placeholder. The recorded time is rendered through Intl
 * in the current language and explicitly in UTC, the zone the view carries
 * it in, never the process-local default; a malformed timestamp renders no
 * time rather than throwing. The record's own fields are journal data (not
 * a closed vocabulary the surface guards): they render as recorded.
 *
 * The page opens with ui-kit's PageHeader: the page title as the level-one
 * heading, a breadcrumb trail from the console to this page, and a
 * refresh action that re-reads the journal. The async section's
 * placeholders render one level down, so the heading order never skips.
 */

import { useMemo } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import { AsyncSection, DataTable, ListSkeleton, PageHeader } from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleAuditRecord } from './operations.js'

/** Parses a server timestamp; null for an absent or malformed value so a
 * bad row can never reach Intl and throw. */
function parseDate(iso: string | undefined): Date | null {
  if (iso === undefined) {
    return null
  }
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? null : date
}

export function AuditPage() {
  const { t, i18n } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const records = operations.useAuditRecords()
  // The view carries each record's time in UTC, so it is formatted in UTC
  // explicitly -- never the engine's implicit process-local default.
  const formatTime = useMemo(
    () =>
      new Intl.DateTimeFormat(i18n.language, {
        dateStyle: 'medium',
        timeStyle: 'short',
        timeZone: 'UTC',
      }),
    [i18n.language],
  )

  // The columns the table renders, in the order the record's fields read:
  // when it happened, what it was, how it ended, who did it and to what.
  // The actor and object columns carry a priority, so a narrow viewport
  // sheds them before the action and the outcome.
  const columns: DataTableColumn<ConsoleAuditRecord>[] = [
    {
      id: 'time',
      header: t('audit.column.time'),
      cell: (record) => {
        const occurred = parseDate(record.occurred_at)
        return occurred === null ? null : formatTime.format(occurred)
      },
    },
    {
      id: 'action',
      header: t('audit.column.action'),
      cell: (record) => record.action,
    },
    {
      id: 'outcome',
      header: t('audit.column.outcome'),
      cell: (record) => record.outcome,
    },
    {
      id: 'actor',
      header: t('audit.column.actor'),
      priority: 'medium',
      cell: (record) => record.actor,
    },
    {
      id: 'object',
      header: t('audit.column.object'),
      priority: 'low',
      cell: (record) => record.object,
    },
  ]

  const rows = records.data
  const pending = records.isPending && rows === undefined

  return (
    <Box>
      <PageHeader
        title={t('audit.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('audit.title') },
        ]}
        actions={
          <Button
            type="button"
            variant="outlined"
            size="small"
            onClick={() => void records.refetch()}
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
            label={t('audit.loading')}
            rows={[
              <Skeleton key="a" variant="text" width="62%" />,
              <Skeleton key="b" variant="text" width="48%" />,
            ]}
          />
        }
        errorState={{
          title: t('audit.error.title'),
          description: t('audit.error.description'),
          retryLabel: t('audit.retry'),
          onRetry: () => void records.refetch(),
        }}
        emptyState={{
          title: t('audit.empty.title'),
          description: t('audit.empty.description'),
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
    </Box>
  )
}
