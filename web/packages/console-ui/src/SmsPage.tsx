/**
 * SmsPage: the SMS-template administration page of the console.
 *
 * It renders the runtime SMS template set over /sms/admin: it lists the
 * templates (name, source, enabled state, languages, per-language
 * approval state), takes up a template whose vendor code the operator
 * already holds, hands a template to the vendor for review, turns a
 * template's sending on or off, and drops a runtime template.
 *
 * The SMS administration surface is mounted only where the deployment
 * configures an SMS carrier: the surface has no meaning without one, so a
 * console that reaches a process without it answers 404/503 and every
 * read settles into the retryable error placeholder rather than a broken
 * page. The page says so in its own words, so an operator who reaches it
 * on a carrier-less process knows why it is empty.
 *
 * Every read and write is an operation on the console operations bundle,
 * so the page builds no client and writes no query key. The page opens
 * with ui-kit's PageHeader.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  AsyncSection,
  DataTable,
  ListSkeleton,
  PageHeader,
  errorCodeOf,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { InlineError } from './internal/inline-error.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleSmsTemplate } from './operations.js'

/** Splits a comma- or whitespace-separated list into its non-empty parts. */
function parseList(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((part) => part.trim())
    .filter((part) => part !== '')
}

/**
 * Parses the codes draft into a language-to-code map: entries separated by
 * commas or whitespace, each a `locale:code` (or `locale=code`) pair.
 */
function parseCodes(text: string): Record<string, string> {
  const codes: Record<string, string> = {}
  for (const entry of parseList(text)) {
    const match = /^([^:=]+)[:=](.+)$/.exec(entry)
    if (match !== null) {
      codes[match[1]!.trim()] = match[2]!.trim()
    }
  }
  return codes
}

export function SmsPage() {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const confirm = usePendingConfirm()

  const templates = operations.useSmsTemplates()
  const registerTemplate = operations.useRegisterSmsTemplate()
  const offerTemplate = operations.useOfferSmsTemplate()
  const setEnabled = operations.useSetSmsTemplateEnabled()
  const removeTemplate = operations.useRemoveSmsTemplate()

  // Register draft.
  const [name, setName] = useState('')
  const [params, setParams] = useState('')
  const [codes, setCodes] = useState('')

  // Offer draft.
  const [offerName, setOfferName] = useState('')
  const [offerContent, setOfferContent] = useState('')
  const [offerKind, setOfferKind] = useState('')
  const [offerLocale, setOfferLocale] = useState('')
  const [offerParams, setOfferParams] = useState('')
  const [offerRemark, setOfferRemark] = useState('')

  const columns: DataTableColumn<ConsoleSmsTemplate>[] = [
    {
      id: 'name',
      header: t('sms.column.name'),
      cell: (template) => <Typography variant="body2">{template.name}</Typography>,
    },
    {
      id: 'source',
      header: t('sms.column.source'),
      priority: 'medium',
      cell: (template) => template.source,
    },
    {
      id: 'locales',
      header: t('sms.column.locales'),
      priority: 'low',
      cell: (template) => template.locales.join(', '),
    },
    {
      id: 'approvals',
      header: t('sms.column.approvals'),
      priority: 'low',
      cell: (template) =>
        Object.entries(template.approvals)
          .map(([locale, approval]) => `${locale}: ${approval.state}`)
          .join(', '),
    },
    {
      id: 'enabled',
      header: t('sms.column.enabled'),
      cell: (template) =>
        template.enabled ? t('sms.enabledYes') : t('sms.enabledNo'),
    },
    {
      id: 'actions',
      header: t('sms.column.actions'),
      cell: (template) => (
        <Box sx={{ display: 'flex', flexWrap: 'wrap', columnGap: 1, rowGap: 1 }}>
          <Button
            type="button"
            size="small"
            variant="outlined"
            disabled={setEnabled.isPending}
            onClick={() =>
              setEnabled.mutate({ name: template.name, enabled: !template.enabled })
            }
          >
            {template.enabled ? t('sms.disable') : t('sms.enable')}
          </Button>
          <Button
            type="button"
            size="small"
            variant="outlined"
            color="error"
            disabled={removeTemplate.isPending}
            onClick={() =>
              confirm.ask({
                title: t('sms.removeTitle'),
                message: t('sms.removeMessage', { name: template.name }),
                confirmLabel: t('common.confirm'),
                run: () => removeTemplate.mutate({ name: template.name }),
              })
            }
          >
            {t('sms.remove')}
          </Button>
        </Box>
      ),
    },
  ]

  const rows = templates.data?.templates
  const pending = templates.isPending && rows === undefined

  const writeError = firstErrorCode([
    registerTemplate.error,
    offerTemplate.error,
    setEnabled.error,
    removeTemplate.error,
  ])

  return (
    <Box>
      <PageHeader
        title={t('sms.title')}
        breadcrumbs={[{ label: t('shell.title') }, { label: t('sms.title') }]}
        actions={
          <Button
            type="button"
            variant="outlined"
            size="small"
            onClick={() => void templates.refetch()}
          >
            {t('common.refresh')}
          </Button>
        }
        sx={{ mb: 2 }}
      />

      <Typography variant="body2" color="text.secondary" sx={{ mb: 3 }}>
        {t('sms.carrierNote')}
      </Typography>

      <Box component="section" sx={{ mb: 3 }}>
        <Typography component="h2" variant="h2" sx={{ fontSize: '1.25rem', mb: 1 }}>
          {t('sms.register.title')}
        </Typography>
        <Box
          sx={{
            display: 'flex',
            flexWrap: 'wrap',
            alignItems: 'flex-end',
            columnGap: 2,
            rowGap: 1,
          }}
        >
          <TextField
            size="small"
            value={name}
            onChange={(event) => setName(event.target.value)}
            label={t('sms.register.nameLabel')}
          />
          <TextField
            size="small"
            value={params}
            onChange={(event) => setParams(event.target.value)}
            label={t('sms.register.paramsLabel')}
          />
          <TextField
            size="small"
            value={codes}
            onChange={(event) => setCodes(event.target.value)}
            label={t('sms.register.codesLabel')}
            placeholder={t('sms.register.codesPlaceholder')}
          />
          <Button
            type="button"
            variant="contained"
            disabled={registerTemplate.isPending}
            onClick={() => {
              registerTemplate.mutate(
                { name, params: parseList(params), codes: parseCodes(codes) },
                {
                  onSuccess: () => {
                    setName('')
                    setParams('')
                    setCodes('')
                  },
                },
              )
            }}
          >
            {t('sms.register.submit')}
          </Button>
        </Box>
      </Box>

      <Box component="section" sx={{ mb: 3 }}>
        <Typography component="h2" variant="h2" sx={{ fontSize: '1.25rem', mb: 1 }}>
          {t('sms.offer.title')}
        </Typography>
        <Box
          sx={{
            display: 'flex',
            flexWrap: 'wrap',
            alignItems: 'flex-end',
            columnGap: 2,
            rowGap: 1,
          }}
        >
          <TextField
            size="small"
            value={offerName}
            onChange={(event) => setOfferName(event.target.value)}
            label={t('sms.offer.nameLabel')}
          />
          <TextField
            size="small"
            value={offerKind}
            onChange={(event) => setOfferKind(event.target.value)}
            label={t('sms.offer.kindLabel')}
          />
          <TextField
            size="small"
            value={offerLocale}
            onChange={(event) => setOfferLocale(event.target.value)}
            label={t('sms.offer.localeLabel')}
          />
          <TextField
            size="small"
            value={offerParams}
            onChange={(event) => setOfferParams(event.target.value)}
            label={t('sms.offer.paramsLabel')}
          />
          <TextField
            size="small"
            value={offerRemark}
            onChange={(event) => setOfferRemark(event.target.value)}
            label={t('sms.offer.remarkLabel')}
          />
          <TextField
            size="small"
            value={offerContent}
            onChange={(event) => setOfferContent(event.target.value)}
            label={t('sms.offer.contentLabel')}
            sx={{ minWidth: 220 }}
          />
          <Button
            type="button"
            variant="contained"
            disabled={offerTemplate.isPending}
            onClick={() => {
              offerTemplate.mutate(
                {
                  name: offerName,
                  content: offerContent,
                  kind: offerKind,
                  locale: offerLocale,
                  params: parseList(offerParams),
                  remark: offerRemark,
                },
                {
                  onSuccess: () => {
                    setOfferName('')
                    setOfferContent('')
                    setOfferKind('')
                    setOfferLocale('')
                    setOfferParams('')
                    setOfferRemark('')
                  },
                },
              )
            }}
          >
            {t('sms.offer.submit')}
          </Button>
        </Box>
      </Box>

      <AsyncSection
        pending={pending}
        payload={rows}
        empty={rows !== undefined && rows.length === 0}
        headingLevel="h2"
        loading={
          <ListSkeleton
            label={t('sms.loading')}
            rows={[
              <Skeleton key="a" variant="text" width="56%" />,
              <Skeleton key="b" variant="text" width="42%" />,
            ]}
          />
        }
        errorState={{
          title: t('sms.error.title'),
          description: t('sms.error.description'),
          retryLabel: t('sms.retry'),
          onRetry: () => void templates.refetch(),
        }}
        emptyState={{
          title: t('sms.empty.title'),
          description: t('sms.empty.description'),
        }}
      >
        {(settled) => (
          <DataTable
            rows={settled}
            columns={columns}
            rowKey={(template) => template.name}
          />
        )}
      </AsyncSection>

      <Box sx={{ mt: 2 }}>
        <InlineError code={writeError} />
      </Box>

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}

/** The first defined error's code, or null when every error is absent. */
function firstErrorCode(errors: readonly unknown[]): string | null {
  for (const error of errors) {
    if (error !== null && error !== undefined) {
      return errorCodeOf(error)
    }
  }
  return null
}
