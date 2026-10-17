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
 * builds no client and writes no query key. The actor renders through the
 * shared account identity unit -- the account's display name where the
 * lookup answers, the recorded value with an unresolved note where it is
 * refused -- so a record reads the same here as in the audit query list.
 * The recorded time renders through Intl in the current language and
 * explicitly in UTC (the zone the view carries), never the process-local
 * default; a malformed timestamp renders the empty-value placeholder, not
 * a guess. The record's own fields are journal data
 * (not a closed vocabulary the surface guards) and render as recorded; a
 * detail object renders as its key/value pairs. An empty field renders
 * the em-dash placeholder rather than a label with no value: a journal
 * field being empty is itself part of the record (a platform-level event
 * has no tenant, an action may carry no purpose), and dropping the row
 * would hide that the record carries an empty value. A refused read
 * settles into a permission placeholder (403) or the retryable error
 * placeholder (any other failure). The page opens with ui-kit's
 * PageHeader.
 */

import { useMemo } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Paper from '@mui/material/Paper'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import { ListSkeleton, PageHeader } from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { AccountIdentity } from './internal/account-identity.js'
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

  // An empty journal field is a value like any other here: the placeholder
  // shows that the record carries it empty, instead of a label with nothing
  // beside it (or a row silently missing from the record's shape).
  const shown = (value: string): string =>
    value === '' ? t('common.emptyValue') : value

  const crumbs = [
    { label: t('shell.title') },
    ...(parentHref === undefined
      ? [{ label: t('audit.title') }]
      : [{ label: t('audit.title'), href: parentHref }]),
    // The trail's last step names the record, so it does not repeat the
    // query page's own label ("审计记录 / 审计记录") above the title.
    { label: t('auditDetail.crumb', { id }) },
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
            value={shown(occurred ?? '')}
          />
          <DefinitionRow
            label={t('auditDetail.field.actor')}
            value={
              settled.actor === '' ? (
                t('common.emptyValue')
              ) : (
                <AccountIdentity account={settled.actor} />
              )
            }
          />
          <DefinitionRow label={t('auditDetail.field.action')} value={shown(settled.action)} />
          <DefinitionRow label={t('auditDetail.field.object')} value={shown(settled.object)} />
          <DefinitionRow label={t('auditDetail.field.outcome')} value={shown(settled.outcome)} />
          <DefinitionRow label={t('auditDetail.field.purpose')} value={shown(settled.purpose)} />
          <DefinitionRow label={t('auditDetail.field.tenant')} value={shown(settled.tenant)} />
          <DefinitionRow label={t('auditDetail.field.source')} value={shown(settled.source)} />
        </Box>

        {detailEntries.length > 0 && (
          <Box sx={{ mt: 2 }}>
            <Typography component="h3" variant="subtitle1" sx={{ mb: 1 }}>
              {t('auditDetail.detailTitle')}
            </Typography>
            <Box component="dl" sx={{ margin: 0 }}>
              {detailEntries.map(([key, value]) => (
                <DefinitionRow key={key} label={key} value={shown(value)} />
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
