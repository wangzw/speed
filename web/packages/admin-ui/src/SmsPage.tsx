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
 *
 * The template switch saves on change. While a table write is in flight the
 * switch and the row's remove action stay focusable, report the unavailable
 * state, and ignore activation. The row whose own removal is in flight is
 * the one remove action that reports the busy state; every other control in
 * the scope, and every remove action while a toggle runs, reports the
 * unavailable state alone. A landed removal takes its row out of the
 * document on the re-read that follows; focus moves to the same remove
 * action in the next row, else the previous row, else the table's own
 * container.
 */

import { useLayoutEffect, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Skeleton from '@mui/material/Skeleton'
import Switch from '@mui/material/Switch'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  ActionButton,
  DataTable,
  ListSkeleton,
  PRIMARY_COLUMN_MAX_WIDTH,
  SectionCard,
  pageRefreshRead,
  useActivationGuard,
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
import type { AdminSmsTemplate, AdminSmsTemplatePage } from './operations.js'
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

/** Which of a row's two writes the table scope's failure names. */
type SmsRowFailedAction = 'enabled' | 'remove'

type SmsRowActionsProps = {
  readonly template: AdminSmsTemplate
  /** The write the table scope's failure names, or null for another row. */
  readonly failedAction: SmsRowFailedAction | null
  readonly failureText: string | null
  /** This row's own removal is in flight. */
  readonly removalBusy: boolean
  /** A write in the table scope is in flight (this row's or another's). */
  readonly writePending: boolean
  readonly onToggle: (template: AdminSmsTemplate) => void
  readonly onRemove: (template: AdminSmsTemplate) => void
  readonly registerRemoveButton: (
    name: string,
    node: HTMLButtonElement | null,
  ) => void
}

/**
 * One row's save-on-change switch and remove action. It is a component of
 * its own because the switch's change handler goes through a hook, which
 * cannot be called from the column's cell callback.
 */
function SmsRowActions({
  template,
  failedAction,
  failureText,
  removalBusy,
  writePending,
  onToggle,
  onRemove,
  registerRemoveButton,
}: SmsRowActionsProps) {
  const { t } = useAdminUiTranslation()
  const errorId = `sms-error-${encodeURIComponent(template.name)}`
  const toggleFailed = failedAction === 'enabled'
  const removeFailed = failedAction === 'remove'
  // Saving on change: while the scope's write is in flight the switch stays
  // focusable and reports the unavailable state on its input, and the
  // change is swallowed instead of reaching the handler.
  const guardedToggle = useActivationGuard(() => onToggle(template), {
    busy: writePending,
  })
  return (
    <Box>
      <Box sx={{ display: 'flex', columnGap: 1 }}>
        <Switch
          checked={template.enabled}
          slotProps={{
            input: {
              'aria-label': t(
                template.enabled ? 'sms.disableAria' : 'sms.enableAria',
                { name: template.name },
              ),
              'aria-invalid': toggleFailed,
              'aria-describedby': toggleFailed ? errorId : undefined,
              'aria-disabled': writePending ? 'true' : undefined,
            },
          }}
          onChange={guardedToggle}
        />
        <ActionButton
          type="button"
          size="small"
          tone="danger"
          aria-label={t('sms.removeAria', { name: template.name })}
          aria-invalid={removeFailed}
          aria-describedby={removeFailed ? errorId : undefined}
          busy={removalBusy}
          unavailable={writePending && !removalBusy}
          ref={(node) => registerRemoveButton(template.name, node)}
          onClick={() => onRemove(template)}
        >
          {t('sms.remove')}
        </ActionButton>
      </Box>
      {failureText !== null && failedAction !== null && (
        <Typography
          id={errorId}
          variant="caption"
          color="error"
          sx={{ display: 'block', mt: 0.5 }}
        >
          {failureText}
        </Typography>
      )}
    </Box>
  )
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
  // Which row owns the table scope's in-flight write, and which of its two
  // writes it is. The scope's `pending` says a write runs; this says whose,
  // so only that row's remove action reports the busy state and every other
  // control reports the shared scope as unavailable.
  const [pendingRow, setPendingRow] = useState<{
    readonly name: string
    readonly action: SmsRowFailedAction
  } | null>(null)
  // Where a landed removal sends focus. The row leaves the document on the
  // re-read the write invalidates, so the target is recorded against the
  // list the write ran over and resolved once that list is replaced.
  const landingAfterRemoval = useRef<{
    readonly name: string
    readonly data: AdminSmsTemplatePage | undefined
  } | null>(null)
  const previousNames = useRef<readonly string[]>([])
  const removeButtons = useRef(new Map<string, HTMLButtonElement>())
  const tableContainerRef = useRef<HTMLDivElement>(null)
  const registerRemoveButton = (
    name: string,
    node: HTMLButtonElement | null,
  ): void => {
    if (node === null) {
      removeButtons.current.delete(name)
    } else {
      removeButtons.current.set(name, node)
    }
  }

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
    setPendingRow({ name: template.name, action: 'enabled' })
    void tableFeedback.run(async () => {
      try {
        await setEnabled.mutateAsync({ name: template.name, enabled })
        return t(enabled ? 'sms.enabledToast' : 'sms.disabledToast', {
          name: template.name,
        })
      } catch (error) {
        setFailedTemplate({ name: template.name, action: 'enabled' })
        throw error
      } finally {
        setPendingRow(null)
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
        setPendingRow({ name: template.name, action: 'remove' })
        void tableFeedback.run(async () => {
          try {
            await removeTemplate.mutateAsync({ name: template.name })
            landingAfterRemoval.current = {
              name: template.name,
              data: templates.data,
            }
            return t('sms.removed', { name: template.name })
          } catch (error) {
            setFailedTemplate({ name: template.name, action: 'remove' })
            throw error
          } finally {
            setPendingRow(null)
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

  // A create submit stays unavailable until every required field carries
  // text, judged after trimming: a registration needs its template name,
  // and a submission needs the template name, the content, the kind and the
  // language. The parameters and the reviewer's note may be empty. Each
  // missing field is checked on its own so the reason can name it.
  const missingFieldsReason = (labels: readonly string[]): string | undefined =>
    labels.length === 0
      ? undefined
      : t('sms.missingFields', {
          fields: labels.join(t('sms.listSeparator')),
        })
  const registerMissing = name.trim() === '' ? [t('sms.register.nameLabel')] : []
  const registerReady = registerMissing.length === 0
  const offerMissing = [
    ...(offerName.trim() === '' ? [t('sms.offer.nameLabel')] : []),
    ...(offerContent.trim() === '' ? [t('sms.offer.contentLabel')] : []),
    ...(offerKind.trim() === '' ? [t('sms.offer.kindLabel')] : []),
    ...(offerLocale.trim() === '' ? [t('sms.offer.localeLabel')] : []),
  ]
  const offerReady = offerMissing.length === 0

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
      align: 'right',
      header: t('sms.column.actions'),
      cellWrap: 'nowrap',
      cell: (template) => (
        <SmsRowActions
          template={template}
          failedAction={
            failedTemplate?.name === template.name
              ? failedTemplate.action
              : null
          }
          failureText={tableFailureText}
          removalBusy={
            pendingRow?.name === template.name &&
            pendingRow.action === 'remove'
          }
          writePending={tableFeedback.pending}
          onToggle={toggleEnabled}
          onRemove={requestRemove}
          registerRemoveButton={registerRemoveButton}
        />
      ),
    },
  ]

  const rows = templates.data?.templates

  // A landed removal moves focus off the row it takes away: the same
  // control in the next row, else the previous row, else the table's own
  // container. The list is compared by identity, so the landing waits for
  // the re-read that actually drops the row. Focus is only placed when the
  // removal left it on the document body; a reader who has chosen another
  // resting place keeps it.
  useLayoutEffect(() => {
    const names = rows?.map((template) => template.name) ?? []
    const landing = landingAfterRemoval.current
    if (landing !== null && templates.data !== landing.data) {
      landingAfterRemoval.current = null
      const active = document.activeElement
      if (
        (active === null || active === document.body) &&
        !names.includes(landing.name)
      ) {
        const index = previousNames.current.indexOf(landing.name)
        const targetName = names[index] ?? names[index - 1]
        const target =
          targetName === undefined
            ? undefined
            : removeButtons.current.get(targetName)
        if (target !== undefined) {
          target.focus()
        } else {
          tableContainerRef.current?.focus()
        }
      }
    }
    previousNames.current = names
  }, [rows, templates.data])

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
          <ActionButton
            type="button"
            emphasis="primary"
            busy={registerFeedback.pending}
            unavailable={!registerReady}
            unavailableReason={missingFieldsReason(registerMissing)}
            onClick={register}
          >
            {t('sms.register.submit')}
          </ActionButton>
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
          <ActionButton
            type="button"
            emphasis="primary"
            busy={offerFeedback.pending}
            unavailable={!offerReady}
            unavailableReason={missingFieldsReason(offerMissing)}
            onClick={offer}
          >
            {t('sms.offer.submit')}
          </ActionButton>
        </Box>
        <Box ref={offerRegionRef}>
          <WriteFeedbackRegion outcome={offerFeedback.outcome} />
        </Box>
      </SectionCard>

      <SectionCard flushContent>
        <Box
          ref={tableContainerRef}
          tabIndex={-1}
          role="group"
          aria-label={t('sms.tableLabel')}
        >
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
        </Box>
        <Box ref={tableRegionRef}>
          <WriteFeedbackRegion outcome={tableFeedback.outcome} />
        </Box>
      </SectionCard>

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}
