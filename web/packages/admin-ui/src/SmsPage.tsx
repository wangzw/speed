/**
 * SmsPage: the SMS-template administration page of the console.
 *
 * It renders the runtime SMS template set over /api/v1/system/sms: it lists the
 * templates (name, source, enabled state, languages, per-language
 * approval state), takes up a template whose vendor code the operator
 * already holds, hands a template to the vendor for review, turns a
 * template's sending on or off, and drops a runtime template.
 *
 * The SMS administration surface is mounted only where the deployment
 * configures an SMS carrier: the surface has no meaning without one. A
 * process without it has no route to answer at all, so the read is
 * answered 404 -- or, on a deployment that serves this console from the
 * same endpoint as the API, with the front-end entry document, which the
 * transport refuses as `client.html`. Both are one more failed read: the
 * page renders its generic failure surface, the retryable placeholder that
 * keeps the retry, and keeps both write forms in place, because a write may
 * still land there. The page says so in its own words, so an operator who
 * reaches it on a carrier-less process knows why it is empty.
 *
 * Every read and write is an operation on the console operations bundle,
 * so the page builds no client and writes no query key.
 *
 * Each titled section is its own feedback scope: registering reports under
 * the register form, offering for review under the offer form, and the
 * template table's enable/remove actions under the table. A refused row
 * action marks that row inline with the same resolved text the region
 * shows and brings the region into view; a success names the template it
 * changed. The header's refresh re-reads the template list and drops the
 * three scopes and the table's row marker first, because a refresh changes
 * the context those results were written under. The page opens with
 * ui-kit's PageHeader.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  DataTable,
  ListSkeleton,
  PRIMARY_COLUMN_MAX_WIDTH,
  SectionCard,
  pageRefreshRead,
  usePageRefresh,
  useWriteFeedback,
} from '@speed/ui-kit'
import { AdminAsyncSection } from './internal/async-section.js'
import type { DataTableColumn } from '@speed/ui-kit'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { useAdminUiErrorText } from './internal/error-text.js'
import { RefreshButton } from '@speed/ui-kit'
import { ADMIN_UI_NAMESPACE } from './resources.js'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  failureTextOf,
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { useAdminOperations } from './operations.js'
import type { AdminSmsTemplate } from './operations.js'
import { AdminPageHeader } from './internal/page-header.js'

/** The template sources the registry names. */
const SMS_SOURCES = new Set<string>(['config', 'runtime'])

/** The vendor review states a template can carry. */
const APPROVAL_STATES = new Set<string>([
  'pending',
  'approved',
  'rejected',
  'withdrawn',
])

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
  const { t, i18n } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const confirm = usePendingConfirm()

  const templates = operations.useSmsTemplates()
  const registerTemplate = operations.useRegisterSmsTemplate()
  const offerTemplate = operations.useOfferSmsTemplate()
  const setEnabled = operations.useSetSmsTemplateEnabled()
  const removeTemplate = operations.useRemoveSmsTemplate()

  // One scope per titled section: register, offer, and the template table.
  const registerFeedback = useWriteFeedback()
  const offerFeedback = useWriteFeedback()
  const tableFeedback = useWriteFeedback()
  const registerRegionRef = useFailureScroll(registerFeedback.outcome)
  const offerRegionRef = useFailureScroll(offerFeedback.outcome)
  const tableRegionRef = useFailureScroll(tableFeedback.outcome)

  // The table row the scope's failure names, and which of its two writes
  // was refused, so the inline marker sits on that control.
  const [failedTemplate, setFailedTemplate] = useState<{
    readonly name: string
    readonly action: 'enabled' | 'remove'
  } | null>(null)
  // The refresh action re-reads the page's one body read. A refresh changes
  // the context every result on the page was written under, so it drops the
  // three section scopes and the table's row marker before re-reading.
  const pageRefresh = usePageRefresh(
    [pageRefreshRead('sms-templates', templates)],
    () => {
      registerFeedback.reset()
      offerFeedback.reset()
      tableFeedback.reset()
      setFailedTemplate(null)
    },
  )
  const resolveErrorText = useAdminUiErrorText()
  const tableFailureText = failureTextOf(
    tableFeedback.outcome,
    resolveErrorText,
  )

  const register = () => {
    const templateName = name
    void registerFeedback.run(async () => {
      await registerTemplate.mutateAsync({
        name: templateName,
        params: parseList(params),
        codes: parseCodes(codes),
      })
      setName('')
      setParams('')
      setCodes('')
      return t('sms.registered', { name: templateName })
    })
  }

  const offer = () => {
    const templateName = offerName
    void offerFeedback.run(async () => {
      await offerTemplate.mutateAsync({
        name: templateName,
        content: offerContent,
        kind: offerKind,
        locale: offerLocale,
        params: parseList(offerParams),
        remark: offerRemark,
      })
      setOfferName('')
      setOfferContent('')
      setOfferKind('')
      setOfferLocale('')
      setOfferParams('')
      setOfferRemark('')
      return t('sms.offered', { name: templateName })
    })
  }

  const toggleEnabled = (template: AdminSmsTemplate) => {
    const enabled = !template.enabled
    setFailedTemplate(null)
    void tableFeedback.run(async () => {
      try {
        await setEnabled.mutateAsync({ name: template.name, enabled })
        return t(enabled ? 'sms.enabledToast' : 'sms.disabledToast', {
          name: template.name,
        })
      } catch (error) {
        setFailedTemplate({ name: template.name, action: 'enabled' })
        throw error
      }
    })
  }

  const requestRemove = (template: AdminSmsTemplate) => {
    confirm.ask({
      title: t('sms.removeTitle'),
      message: t('sms.removeMessage', { name: template.name }),
      confirmLabel: t('common.confirm'),
      run: () => {
        setFailedTemplate(null)
        void tableFeedback.run(async () => {
          try {
            await removeTemplate.mutateAsync({ name: template.name })
            return t('sms.removed', { name: template.name })
          } catch (error) {
            setFailedTemplate({ name: template.name, action: 'remove' })
            throw error
          }
        })
      },
    })
  }

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

  // A create submit stays disabled until every required field carries text,
  // judged after trimming: a registration needs its template name, and a
  // submission needs the template name, the content, the kind and the
  // language. The parameters and the reviewer's note may be empty.
  const registerReady = name.trim() !== ''
  const offerReady =
    offerName.trim() !== '' &&
    offerContent.trim() !== '' &&
    offerKind.trim() !== '' &&
    offerLocale.trim() !== ''

  // A closed-set source or review state renders its own label; a value the
  // set does not name is shown as the record carries it.
  const closedSetLabel = (key: string, value: string): string =>
    i18n.exists(key, { ns: ADMIN_UI_NAMESPACE }) ? t(key) : value
  const sourceLabel = (source: string): string =>
    SMS_SOURCES.has(source)
      ? closedSetLabel(`sms.source.${source}`, source)
      : source
  const approvalLabel = (state: string): string =>
    APPROVAL_STATES.has(state)
      ? closedSetLabel(`sms.approval.${state}`, state)
      : state

  const columns: DataTableColumn<AdminSmsTemplate>[] = [
    {
      id: 'name',
      header: t('sms.column.name'),
      cellWrap: 'nowrap',
      cellFullValue: (template) => template.name,
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (template) => <Typography variant="body2">{template.name}</Typography>,
    },
    {
      id: 'source',
      header: t('sms.column.source'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (template) => sourceLabel(template.source),
    },
    {
      id: 'locales',
      header: t('sms.column.locales'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (template) => template.locales.join(', '),
    },
    {
      id: 'approvals',
      header: t('sms.column.approvals'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (template) =>
        Object.entries(template.approvals)
          .map(([locale, approval]) => `${locale}: ${approvalLabel(approval.state)}`)
          .join(', '),
    },
    {
      id: 'enabled',
      header: t('sms.column.enabled'),
      priority: 'high',
      cellWrap: 'nowrap',
      cell: (template) =>
        template.enabled ? t('sms.enabledYes') : t('sms.enabledNo'),
    },
    {
      id: 'actions',
      header: t('sms.column.actions'),
      cellWrap: 'nowrap',
      cell: (template) => {
        const errorId = `sms-error-${encodeURIComponent(template.name)}`
        const toggleFailed =
          failedTemplate?.name === template.name &&
          failedTemplate.action === 'enabled'
        const removeFailed =
          failedTemplate?.name === template.name &&
          failedTemplate.action === 'remove'
        return (
          <Box>
            <Box sx={{ display: 'flex', columnGap: 1 }}>
              <Button
                type="button"
                size="small"
                variant="outlined"
                aria-label={t(
                  template.enabled ? 'sms.disableAria' : 'sms.enableAria',
                  { name: template.name },
                )}
                aria-invalid={toggleFailed}
                aria-describedby={toggleFailed ? errorId : undefined}
                disabled={tableFeedback.pending}
                loading={tableFeedback.pending}
                onClick={() => toggleEnabled(template)}
              >
                {template.enabled ? t('sms.disable') : t('sms.enable')}
              </Button>
              <Button
                type="button"
                size="small"
                variant="outlined"
                color="error"
                aria-label={t('sms.removeAria', { name: template.name })}
                aria-invalid={removeFailed}
                aria-describedby={removeFailed ? errorId : undefined}
                disabled={tableFeedback.pending}
                loading={tableFeedback.pending}
                onClick={() => requestRemove(template)}
              >
                {t('sms.remove')}
              </Button>
            </Box>
            {tableFailureText !== null &&
              failedTemplate?.name === template.name && (
                <Typography
                  id={errorId}
                  variant="caption"
                  color="error"
                  sx={{ display: 'block', mt: 0.5 }}
                >
                  {tableFailureText}
                </Typography>
              )}
          </Box>
        )
      },
    },
  ]

  const rows = templates.data?.templates
  const pending = templates.isPending && rows === undefined

  return (
    <Box>
      <AdminPageHeader
        title={t('sms.title')}
        breadcrumbs={[{ label: t('shell.title') }, { label: t('sms.title') }]}
        actions={
          <RefreshButton
            label={t('common.refresh')}
            // A refresh changes the context the page's writes settle
            // against, so the control stays inert while any of the page's
            // scopes has a write in flight; the mechanism marks it busy
            // while the re-read is in flight.
            busy={pageRefresh.busy}
            writePending={
              registerFeedback.pending ||
              offerFeedback.pending ||
              tableFeedback.pending
            }
            onClick={pageRefresh.refresh}
          />
        }
        sx={{ mb: 2 }}
      />

      <Typography variant="body2" color="text.secondary" sx={{ mb: 3 }}>
        {t('sms.carrierNote')}
      </Typography>

      <SectionCard title={t('sms.register.title')} sx={{ mb: 3 }}>
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
            disabled={registerFeedback.pending || !registerReady}
            loading={registerFeedback.pending}
            onClick={register}
          >
            {t('sms.register.submit')}
          </Button>
        </Box>
        <Box ref={registerRegionRef}>
          <WriteFeedbackRegion outcome={registerFeedback.outcome} />
        </Box>
      </SectionCard>

      <SectionCard title={t('sms.offer.title')} sx={{ mb: 3 }}>
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
            disabled={offerFeedback.pending || !offerReady}
            loading={offerFeedback.pending}
            onClick={offer}
          >
            {t('sms.offer.submit')}
          </Button>
        </Box>
        <Box ref={offerRegionRef}>
          <WriteFeedbackRegion outcome={offerFeedback.outcome} />
        </Box>
      </SectionCard>

      <SectionCard flushContent>
        <AdminAsyncSection
          pending={pending}
          payload={rows}
          error={templates.error}
          refreshing={templates.isFetching}
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
        </AdminAsyncSection>
        <Box ref={tableRegionRef}>
          <WriteFeedbackRegion outcome={tableFeedback.outcome} />
        </Box>
      </SectionCard>

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}
