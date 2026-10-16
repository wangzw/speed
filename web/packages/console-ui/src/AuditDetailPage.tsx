/**
 * AuditDetailPage: one audit record in full.
 *
 * The audit query page lists the platform's append-only trail; a row's
 * action links here to the record itself, read from
 * GET /audit/admin/records/:id. It is a detail page: a main column
 * carrying the record's fields (when it happened, who did it, what it
 * was, how it ended and why) and a fixed-width side column carrying the
 * row's identifier and a way back to the query.
 *
 * The read is an operation on the console operations bundle, so the page
 * builds no client and writes no query key. The recorded time renders
 * through Intl in the current language and explicitly in UTC (the zone
 * the view carries), never the process-local default; a malformed
 * timestamp renders nothing. The record's own fields are journal data
 * (not a closed vocabulary the surface guards) and render as recorded; a
 * detail object renders as its key/value pairs. A refused read settles
 * into a permission placeholder (403) or the retryable error placeholder
 * (any other failure). The page opens with ui-kit's PageHeader.
 */

import { useMemo } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Paper from '@mui/material/Paper'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import { ListSkeleton, PageHeader } from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { formatUtc } from './internal/datetime.js'
import { ConsoleLink } from './internal/navigation.js'
import { DefinitionRow, DetailLayout } from './internal/layout.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleAuditRecord } from './operations.js'

export interface AuditDetailPageProps {
  /** The record to show. */
  readonly id: string
  /** The audit query's path, for the breadcrumb and the back link; absent hides them. */
  readonly parentHref?: string
}

export function AuditDetailPage({ id, parentHref }: AuditDetailPageProps) {
  const { t, i18n } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const record = operations.useAuditRecord({ id })

  // The detail map's entries, in a stable order, so the rendered list does
  // not reshuffle between renders.
  const detailEntries = useMemo(() => {
    const detail = record.data?.detail
    return detail === undefined ? [] : Object.entries(detail).sort(([a], [b]) => a.localeCompare(b))
  }, [record.data])

  const crumbs = [
    { label: t('shell.title') },
    ...(parentHref === undefined
      ? [{ label: t('audit.title') }]
      : [{ label: t('audit.title'), href: parentHref }]),
    { label: t('auditDetail.title') },
  ]

  const main = (settled: ConsoleAuditRecord) => {
    const occurred = formatUtc(settled.occurred_at, i18n.language)
    return (
      <Paper component="section" variant="outlined" sx={{ p: 2 }}>
        <Typography component="h2" variant="h6" sx={{ mb: 1 }}>
          {t('auditDetail.summary')}
        </Typography>
        <Box component="dl" sx={{ margin: 0 }}>
          <DefinitionRow label={t('auditDetail.field.id')} value={settled.id} />
          <DefinitionRow
            label={t('auditDetail.field.occurredAt')}
            value={occurred ?? ''}
          />
          <DefinitionRow label={t('auditDetail.field.actor')} value={settled.actor} />
          <DefinitionRow label={t('auditDetail.field.action')} value={settled.action} />
          <DefinitionRow label={t('auditDetail.field.object')} value={settled.object} />
          <DefinitionRow label={t('auditDetail.field.outcome')} value={settled.outcome} />
          <DefinitionRow label={t('auditDetail.field.purpose')} value={settled.purpose} />
          <DefinitionRow label={t('auditDetail.field.tenant')} value={settled.tenant} />
          <DefinitionRow label={t('auditDetail.field.source')} value={settled.source} />
        </Box>

        {detailEntries.length > 0 && (
          <Box sx={{ mt: 2 }}>
            <Typography component="h3" variant="subtitle1" sx={{ mb: 1 }}>
              {t('auditDetail.detailTitle')}
            </Typography>
            <Box component="dl" sx={{ margin: 0 }}>
              {detailEntries.map(([key, value]) => (
                <DefinitionRow key={key} label={key} value={value} />
              ))}
            </Box>
          </Box>
        )}
      </Paper>
    )
  }

  const rows = record.data
  const pending = record.isPending && rows === undefined

  return (
    <Box>
      <PageHeader
        title={t('auditDetail.title')}
        breadcrumbs={crumbs}
        actions={
          <Button
            type="button"
            variant="outlined"
            size="small"
            onClick={() => void record.refetch()}
          >
            {t('common.refresh')}
          </Button>
        }
        sx={{ mb: 2 }}
      />

      <DetailLayout
        main={
          <ConsoleAsyncSection
            pending={pending}
            payload={rows}
            error={record.error}
            empty={false}
            headingLevel="h2"
            loading={
              <ListSkeleton
                label={t('auditDetail.loading')}
                rows={[
                  <Skeleton key="a" variant="text" width="58%" />,
                  <Skeleton key="b" variant="text" width="46%" />,
                  <Skeleton key="c" variant="text" width="40%" />,
                ]}
              />
            }
            errorState={{
              title: t('auditDetail.error.title'),
              description: t('auditDetail.error.description'),
              retryLabel: t('auditDetail.retry'),
              onRetry: () => void record.refetch(),
            }}
          >
            {main}
          </ConsoleAsyncSection>
        }
        side={
          <Paper component="section" variant="outlined" sx={{ p: 2 }}>
            <Typography component="h2" variant="h6" sx={{ mb: 1 }}>
              {t('auditDetail.field.id')}
            </Typography>
            <Typography variant="body2" sx={{ wordBreak: 'break-all' }}>
              {id}
            </Typography>
            {parentHref !== undefined && (
              <Box sx={{ mt: 2 }}>
                <ConsoleLink href={parentHref}>{t('auditDetail.back')}</ConsoleLink>
              </Box>
            )}
          </Paper>
        }
      />
    </Box>
  )
}
