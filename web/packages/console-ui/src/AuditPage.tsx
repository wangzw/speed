/**
 * AuditPage: the audit-query page of the console.
 *
 * It reads the audit journal's records -- the platform's append-only
 * trail, whose rows span every tenant -- and renders them newest first.
 * The read surface carries no export: exporting is another permission and
 * another module's to carry, so this page only lists.
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
 * The page renders a level-one heading and the async section's
 * placeholders render one level down, so the heading order never skips.
 */

import { useMemo } from 'react'
import Box from '@mui/material/Box'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import { AsyncSection, ListSkeleton } from '@speed/ui-kit'
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

  const rows = records.data
  const pending = records.isPending && rows === undefined

  return (
    <Box>
      <Typography variant="h1" sx={{ fontSize: '1.75rem', mb: 2 }}>
        {t('audit.title')}
      </Typography>

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
          <Box>
            {settled.map((record, index) => (
              <AuditRow
                key={record.id}
                record={record}
                first={index === 0}
                formatTime={(date) => formatTime.format(date)}
              />
            ))}
          </Box>
        )}
      </AsyncSection>
    </Box>
  )
}

function AuditRow({
  record,
  first,
  formatTime,
}: {
  readonly record: ConsoleAuditRecord
  readonly first: boolean
  readonly formatTime: (date: Date) => string
}) {
  const occurred = parseDate(record.occurred_at)
  return (
    <Box
      sx={{
        py: 1.5,
        minWidth: 0,
        ...(first
          ? {}
          : { borderTop: '1px solid', borderColor: 'divider' }),
      }}
    >
      <Box
        sx={{
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'baseline',
          columnGap: 2,
          rowGap: 0.5,
        }}
      >
        <Typography variant="body1" sx={{ fontWeight: 500 }}>
          {record.action}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          {record.outcome}
        </Typography>
        {occurred !== null && (
          <Typography
            variant="body2"
            color="text.secondary"
            sx={{ marginLeft: 'auto', flexShrink: 0 }}
          >
            {formatTime(occurred)}
          </Typography>
        )}
      </Box>
      <Box
        sx={{
          display: 'flex',
          flexWrap: 'wrap',
          columnGap: 2.5,
          rowGap: 0.5,
          mt: 0.5,
        }}
      >
        <Typography variant="body2" color="text.secondary">
          {record.actor}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          {record.object}
        </Typography>
      </Box>
    </Box>
  )
}
