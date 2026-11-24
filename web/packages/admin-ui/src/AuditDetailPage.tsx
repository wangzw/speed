import { useMemo } from 'react'
import Box from '@mui/material/Box'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import {
  ListSkeleton,
  SectionCard,
  useFormatInstant,
} from '@speed/ui-kit'
import { AdminAsyncSection } from './internal/async-section.js'
import {
  AccountIdentity,
  auditDetailNamesAccount,
  AuditObjectIdentity,
} from './internal/account-identity.js'
import { AdminLink } from './internal/navigation.js'
import { DefinitionRow, DetailLayout } from './internal/layout.js'
import { RefreshButton } from '@speed/ui-kit'
import { useAdminUiTranslation } from './internal/translation.js'
import { useAdminOperations } from './operations.js'
import type { AdminAuditRecord } from './operations.js'
import { AdminPageHeader } from './internal/page-header.js'

/** Props for AuditDetailPage: the record id and the optional audit query path. */
export interface AuditDetailPageProps {
  /** The record to show. */
  readonly id: string
  /** The audit query's path, for the breadcrumb and the back link; absent hides them. */
  readonly parentHref?: string
}

/** AuditDetailPage renders one audit record, its detail entries and a back link. */
export function AuditDetailPage({ id, parentHref }: AuditDetailPageProps) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const formatTime = useFormatInstant()
  const record = operations.useAuditRecord({ id })

  const detailEntries = useMemo(() => {
    const detail = record.data?.detail
    return detail === undefined ? [] : Object.entries(detail).sort(([a], [b]) => a.localeCompare(b))
  }, [record.data])

  const shown = (value: string): string =>
    value === '' ? t('common.emptyValue') : value

  const crumbs = [
    { label: t('shell.title') },
    ...(parentHref === undefined
      ? [{ label: t('audit.title') }]
      : [{ label: t('audit.title'), href: parentHref }]),
    { label: t('auditDetail.crumb', { id }) },
  ]

  const main = (settled: AdminAuditRecord) => {
    const occurred = formatTime(settled.occurred_at)
    return (
      <SectionCard title={t('auditDetail.summary')}>
        <Box component="dl" sx={{ margin: 0 }}>
          <DefinitionRow label={t('auditDetail.field.id')} value={settled.id} />
          <DefinitionRow
            label={t('auditDetail.field.occurredAt')}
            value={shown(occurred)}
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
          <DefinitionRow
            label={t('auditDetail.field.object')}
            value={
              settled.object === '' ? (
                t('common.emptyValue')
              ) : (
                <AuditObjectIdentity object={settled.object} />
              )
            }
          />
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
                <DefinitionRow
                  key={key}
                  label={key}
                  value={
                    value === ''
                      ? t('common.emptyValue')
                      : auditDetailNamesAccount(settled.action, key)
                        ? <AccountIdentity account={value} />
                        : value
                  }
                />
              ))}
            </Box>
          </Box>
        )}
      </SectionCard>
    )
  }

  const rows = record.data
  const pending = record.isPending && rows === undefined

  return (
    <Box>
      <AdminPageHeader
        title={t('auditDetail.title')}
        breadcrumbs={crumbs}
        actions={
          <RefreshButton
            label={t('common.refresh')}
            busy={record.isFetching}
            onClick={() => void record.refetch()}
          />
        }
        sx={{ mb: 2 }}
      />

      <DetailLayout
        main={
          <AdminAsyncSection
            pending={pending}
            payload={rows}
            error={record.error}
            refreshing={record.isFetching}
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
            notFoundState={{
              title: t('detail.notFound.title'),
              description: t('detail.notFound.description'),
              retryLabel: t('auditDetail.retry'),
              onRetry: () => void record.refetch(),
            }}
            notFoundStatuses={[400]}
          >
            {main}
          </AdminAsyncSection>
        }
        side={
          <SectionCard title={t('auditDetail.field.id')}>
            <Typography variant="body2" sx={{ wordBreak: 'break-all' }}>
              {id}
            </Typography>
            {parentHref !== undefined && (
              <Box sx={{ mt: 2 }}>
                <AdminLink href={parentHref}>{t('auditDetail.back')}</AdminLink>
              </Box>
            )}
          </SectionCard>
        }
      />
    </Box>
  )
}
