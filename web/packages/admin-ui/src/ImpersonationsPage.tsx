import { useCallback, useEffect, useId, useRef, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Skeleton from '@mui/material/Skeleton'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnAdminImpersonationsListQueryKey,
  getAuthnImpersonationStatusQueryKey,
  useAuthnImpersonationEnd,
  useAuthnImpersonationStart,
  useAuthnImpersonationStatus,
} from '@speed/api-sdk'
import type { AuthnImpersonationGrant } from '@speed/api-sdk'
import { useStepUpGate } from '@speed/auth-core'
import type { StepUpGate } from '@speed/auth-core'
import {
  ActionButton,
  DataTable,
  EmptyState,
  ListSkeleton,
  LONG_TEXT_COLUMN_MAX_WIDTH,
  PRIMARY_COLUMN_MAX_WIDTH,
  RefreshButton,
  SectionCard,
  httpStatusOf,
  pageRefreshRead,
  useFormatInstant,
  usePageRefresh,
  useWriteFeedback,
} from '@speed/ui-kit'
import type { DataTableColumn, WriteFeedback } from '@speed/ui-kit'
import {
  AccountIdentity,
  accountNotice,
  useAccountIdentity,
  type AccountIdentityValue,
} from './internal/account-identity.js'
import { AccountSelector } from './internal/account-selector.js'
import { AdminAsyncSection } from './internal/async-section.js'
import { isNullInstant } from './internal/datetime.js'
import {
  useAdminImpersonationChange,
  type AdminImpersonationChange,
} from './internal/impersonation-change.js'
import { useAdminImpersonationStarting } from './internal/impersonation-starting.js'
import { useAdminSession } from './internal/session.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { ADMIN_UI_NAMESPACE } from './resources.js'
import { TenantScope, tenantFromLocation } from './internal/tenant-scope.js'
import { useAdminUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'
import { useSectionHeadingFocus } from './internal/section-heading-focus.js'
import { useAdminOperations } from './operations.js'
import type { AdminAccount, AdminImpersonation } from './operations.js'
import { AdminPageHeader } from './internal/page-header.js'

interface ImpersonationStatusView {
  readonly grant: AuthnImpersonationGrant | undefined
}

function TerminateButton({
  record,
  busy,
  unavailable,
  unavailableReason,
  onTerminate,
}: {
  readonly record: AdminImpersonation
  readonly busy: boolean
  readonly unavailable: boolean
  readonly unavailableReason?: string
  readonly onTerminate: (
    record: AdminImpersonation,
    identity: AccountIdentityValue,
  ) => void
}) {
  const { t } = useAdminUiTranslation()
  const identity = useAccountIdentity(record.target)
  return (
    <ActionButton
      type="button"
      size="small"
      tone="danger"
      busy={busy}
      unavailable={unavailable}
      unavailableReason={unavailableReason}
      onClick={() =>
        onTerminate(record, {
          name: identity.name,
          username: identity.username,
        })
      }
    >
      {t('impersonations.terminate')}
    </ActionButton>
  )
}

/** Whether a failure is the status read's bare 404 ("no impersonation in
 * force"), surfaced as an HTTP 404 or the client.http.404 code. */
function isNotImpersonating(error: unknown): boolean {
  return httpStatusOf(error) === 404
}

function notifyIdentityChange(
  callback:
    | ((change: AdminImpersonationChange) => void | Promise<void>)
    | undefined,
  change: AdminImpersonationChange,
): void {
  if (callback === undefined) {
    return
  }
  void Promise.resolve()
    .then(() => callback(change))
    .catch(() => undefined)
}

const END_REASON_KEYS: Readonly<Record<string, string>> = {
  explicit: 'explicit',
  revoked: 'revoked',
  expired: 'expired',
  'session-ended': 'sessionEnded',
  'actor-disabled': 'actorDisabled',
  'target-disabled': 'targetDisabled',
  'membership-removed': 'membershipRemoved',
  'start-failed': 'startFailed',
}

/** Props for ImpersonationsPage: the optional controlled tenant scope. */
export interface ImpersonationsPageProps {
  /** A controlled tenant; given, it hides the selector. Empty string is a
   * controlled "no tenant". */
  readonly tenant?: string
  /** The initial tenant when not controlled; wins over the query parameter. */
  readonly defaultTenant?: string
  /** Reports each re-selection made through the page's own selector. */
  readonly onTenantChange?: (tenant: string) => void
}

/** ImpersonationsPage shows the caller's own impersonation and the tenant's
 * impersonation records. */
export function ImpersonationsPage({
  tenant: controlledTenant,
  defaultTenant,
  onTenantChange,
}: ImpersonationsPageProps = {}) {
  const { t, i18n } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const formatTime = useFormatInstant()
  const queryClient = useQueryClient()

  const [innerTenant, setInnerTenant] = useState(
    () => defaultTenant ?? tenantFromLocation(),
  )
  const tenant = controlledTenant ?? innerTenant
  const tenantControlled = controlledTenant !== undefined

  const currentFeedback = useWriteFeedback()
  const recordsFeedback = useWriteFeedback()
  const [terminatingIds, setTerminatingIds] = useState<ReadonlySet<string>>(
    () => new Set(),
  )
  const { sectionRef: currentSectionRef, focusHeading: focusCurrentHeading } =
    useSectionHeadingFocus()
  const endButtonRef = useRef<HTMLButtonElement | null>(null)
  const session = useAdminSession()
  const gate = useStepUpGate<() => void>(
    (retry) => retry(),
    (error) => session !== null && session.isStepUpShortfall(error),
  )
  const identityChange = useAdminImpersonationChange()

  const status = useAuthnImpersonationStatus()

  const notImpersonating = isNotImpersonating(status.error)
  const statusError = notImpersonating ? undefined : status.error
  const statusPayload: ImpersonationStatusView | undefined = notImpersonating
    ? { grant: undefined }
    : status.data === undefined
      ? undefined
      : { grant: status.data.grant }
  const grant = statusPayload?.grant
  const endIdentity = useAccountIdentity(grant?.target)
  const statusPending = status.isPending && statusPayload === undefined
  const statusFailed = statusError !== undefined && statusError !== null
  const impersonating = !statusFailed && grant !== undefined
  const notInForce = statusPayload !== undefined && grant === undefined
  const recordsEnabled = !status.isPending && !impersonating && tenant !== ''

  const impersonations = operations.useImpersonations(
    { tenant },
    { enabled: recordsEnabled },
  )
  const revokeImpersonation = operations.useRevokeImpersonation()

  const resetPageFeedback = useCallback(() => {
    currentFeedback.reset()
    recordsFeedback.reset()
  }, [currentFeedback.reset, recordsFeedback.reset])

  const pageRefresh = usePageRefresh(
    [
      pageRefreshRead('impersonation-status', status),
      pageRefreshRead('impersonations', impersonations, {
        enabled: recordsEnabled && impersonations.isEnabled,
      }),
    ],
    resetPageFeedback,
  )

  const endImpersonation = useAuthnImpersonationEnd({
    mutation: {
      onSuccess: () => {
        void queryClient.resetQueries({
          queryKey: getAuthnImpersonationStatusQueryKey(),
        })
        queryClient.removeQueries({
          queryKey: getAuthnAdminImpersonationsListQueryKey(tenant),
        })
      },
    },
  })

  useEffect(() => {
    resetPageFeedback()
  }, [tenant, resetPageFeedback])

  const markTerminating = useCallback((id: string, inFlight: boolean) => {
    setTerminatingIds((previous) => {
      const next = new Set(previous)
      if (inFlight) {
        next.add(id)
      } else {
        next.delete(id)
      }
      return next
    })
  }, [])

  const endAttempt = (): Promise<void> =>
    currentFeedback.run(async () => {
      try {
        await endImpersonation.mutateAsync()
      } catch (error) {
        if (gate.intercept(error, () => void endAttempt())) {
          return null
        }
        if (!isNotImpersonating(error)) {
          throw error
        }
        void queryClient.resetQueries({
          queryKey: getAuthnImpersonationStatusQueryKey(),
        })
        queryClient.removeQueries({
          queryKey: getAuthnAdminImpersonationsListQueryKey(tenant),
        })
      }
      // Focus lands on the section's heading when the end control held it,
      // but not when the reader moved it elsewhere.
      const active = document.activeElement
      if (
        active === null ||
        active === document.body ||
        active === endButtonRef.current
      ) {
        focusCurrentHeading()
      }
      notifyIdentityChange(identityChange, 'ended')
      return accountNotice(
        t,
        {
          named: 'impersonations.current.ended',
          unnamed: 'impersonations.current.endedUnnamed',
        },
        endIdentity,
      )
    })

  const terminate = (
    record: AdminImpersonation,
    identity: AccountIdentityValue,
  ): void => {
    const attempt = (): Promise<void> => {
      markTerminating(record.id, true)
      return recordsFeedback.run(async () => {
        try {
          await revokeImpersonation.mutateAsync({ id: record.id, tenant })
          void status.refetch()
          return accountNotice(
            t,
            {
              named: 'impersonations.terminated',
              unnamed: 'impersonations.terminatedUnnamed',
            },
            identity,
          )
        } catch (error) {
          if (gate.intercept(error, () => void attempt())) {
            return null
          }
          throw error
        } finally {
          markTerminating(record.id, false)
        }
      })
    }
    void attempt()
  }

  const endReasonLabel = (reason: string): string => {
    const leaf = END_REASON_KEYS[reason]
    const key = leaf === undefined ? '' : `impersonations.endReason.${leaf}`
    return key !== '' && i18n.exists(key, { ns: ADMIN_UI_NAMESPACE })
      ? t(key)
      : reason
  }

  const columns: DataTableColumn<AdminImpersonation>[] = [
    {
      id: 'actor',
      header: t('impersonations.column.actor'),
      priority: 'medium',
      cellWrap: 'nowrap',
      contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
      cell: (record) => <AccountIdentity account={record.actor} />,
    },
    {
      id: 'target',
      header: t('impersonations.column.target'),
      cellWrap: 'nowrap',
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (record) => <AccountIdentity account={record.target} />,
    },
    {
      id: 'tenant',
      header: t('impersonations.column.tenant'),
      priority: 'low',
      cellWrap: 'nowrap',
      cellFullValue: (record) => record.tenant,
      contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
      cell: (record) => record.tenant,
    },
    {
      id: 'started',
      header: t('impersonations.column.started'),
      cellWrap: 'nowrap',
      cell: (record) => formatTime(record.started_at),
    },
    {
      id: 'ends',
      header: t('impersonations.column.ends'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (record) => formatTime(record.ends_at),
    },
    {
      id: 'state',
      header: t('impersonations.column.state'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (record) =>
        isNullInstant(record.ended_at) ? (
          <Typography variant="body2" color="warning.main">
            {t('impersonations.inForce')}
          </Typography>
        ) : (
          <Typography variant="body2" color="text.secondary">
            {t('impersonations.ended', {
              reason: endReasonLabel(record.end_reason ?? ''),
            })}
          </Typography>
        ),
    },
    {
      id: 'actions',
      align: 'right',
      header: t('impersonations.column.actions'),
      cellWrap: 'nowrap',
      cell: (record) => {
        const ended = !isNullInstant(record.ended_at)
        const terminating = terminatingIds.has(record.id)
        return (
          <TerminateButton
            record={record}
            busy={terminating}
            unavailable={ended || (recordsFeedback.pending && !terminating)}
            unavailableReason={
              ended ? t('impersonations.terminateEnded') : undefined
            }
            onTerminate={terminate}
          />
        )
      },
    },
  ]

  const rows = impersonations.data
  const pending = impersonations.isPending && rows === undefined

  return (
    <Box>
      <AdminPageHeader
        title={t('impersonations.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('impersonations.title') },
        ]}
        actions={
          <RefreshButton
            busy={pageRefresh.busy}
            writePending={
              currentFeedback.pending || recordsFeedback.pending
            }
            label={t('common.refresh')}
            onClick={pageRefresh.refresh}
          />
        }
        sx={{ mb: 2 }}
      />

      <SectionCard
        ref={currentSectionRef}
        title={t('impersonations.current.title')}
        sx={{ mb: 3 }}
      >
        <AdminAsyncSection
          pending={statusPending}
          payload={statusPayload}
          error={statusError}
          empty={false}
          refreshing={status.isFetching}
          loading={
            <Typography role="status" variant="body2" color="text.secondary">
              {t('impersonations.current.loading')}
            </Typography>
          }
          errorState={{
            title: t('impersonations.current.error.title'),
            description: t('impersonations.current.error.description'),
            retryLabel: t('impersonations.retry'),
            onRetry: () => void status.refetch(),
          }}
        >
          {(settled) =>
            settled.grant === undefined ? (
              <Typography variant="body2" color="text.secondary">
                {t('impersonations.current.none')}
              </Typography>
            ) : (
              <Alert severity="warning" variant="outlined">
                <Stack sx={{ gap: 0.5, mb: 1 }}>
                  <Typography variant="body2">
                    {t('impersonations.current.target')}
                    <AccountIdentity account={settled.grant.target} />
                  </Typography>
                  <Typography variant="body2">
                    {t('impersonations.current.tenant', {
                      tenant: settled.grant.tenant,
                    })}
                  </Typography>
                  <Typography variant="body2">
                    {t('impersonations.current.ends', {
                      ends: formatTime(settled.grant.ends_at),
                    })}
                  </Typography>
                </Stack>
                <ActionButton
                  ref={endButtonRef}
                  type="button"
                  size="small"
                  tone="danger"
                  busy={currentFeedback.pending}
                  onClick={() => void endAttempt()}
                >
                  {t('impersonations.current.end')}
                </ActionButton>
              </Alert>
            )
          }
        </AdminAsyncSection>

        {notInForce && (
          <Box sx={{ mt: 2 }}>
            <Typography component="h3" variant="h6" sx={{ mb: 1 }}>
              {t('impersonations.start.title')}
            </Typography>
            <StartImpersonationForm
              tenant={tenant}
              feedback={currentFeedback}
              gate={gate}
              identityChange={identityChange}
              onStarted={() => void impersonations.refetch()}
            />
          </Box>
        )}

        {/* The start form unmounts when a start lands; this region stays
            with the section, so the success message is not unmounted with
            the form that produced it. */}
        <WriteFeedbackRegion outcome={currentFeedback.outcome} />
      </SectionCard>

      {impersonating ? (
        <Typography variant="body2" color="text.secondary" sx={{ mb: 3 }}>
          {t('impersonations.recordsUnavailable')}
        </Typography>
      ) : (
        <>
          {!tenantControlled && (
            <SectionCard sx={{ mb: 3 }}>
              <TenantScope
                value={tenant}
                onSelect={(next) => {
                  setInnerTenant(next)
                  onTenantChange?.(next)
                }}
                disabled={currentFeedback.pending || recordsFeedback.pending}
              />
            </SectionCard>
          )}

          {tenant === '' ? (
            <EmptyState
              headingLevel="h2"
              title={t('impersonations.needTenant.title')}
              description={t('impersonations.needTenant.description')}
            />
          ) : (
            <>
              <SectionCard flushContent>
                <AdminAsyncSection
                  pending={pending}
                  payload={rows}
                  error={impersonations.error}
                  refreshing={impersonations.isFetching}
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
                </AdminAsyncSection>
                <WriteFeedbackRegion outcome={recordsFeedback.outcome} />
              </SectionCard>
            </>
          )}
        </>
      )}

      {session !== null && (
        <StepUpChallenge
          open={gate.open}
          reason={gate.reason}
          session={session}
          onSuccess={gate.succeed}
          onCancel={gate.cancel}
        />
      )}
    </Box>
  )
}

function StartImpersonationForm({
  tenant,
  feedback,
  gate,
  identityChange,
  onStarted,
}: {
  readonly tenant: string
  readonly feedback: WriteFeedback
  readonly gate: StepUpGate<() => void>
  readonly identityChange:
    | ((change: AdminImpersonationChange) => void | Promise<void>)
    | undefined
  readonly onStarted: () => void
}) {
  const { t } = useAdminUiTranslation()
  const onStarting = useAdminImpersonationStarting()
  const queryClient = useQueryClient()
  const startImpersonation = useAuthnImpersonationStart({
    mutation: {
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnImpersonationStatusQueryKey(),
        })
      },
    },
  })

  const [account, setAccount] = useState<AdminAccount | null>(null)
  const [reason, setReason] = useState('')
  const [ttl, setTtl] = useState('')
  const identity = useAccountIdentity(account?.id)
  const tenantHintId = useId()

  const submitExplanation =
    tenant === ''
      ? { unavailableDescribedBy: tenantHintId }
      : account === null
        ? { unavailableReason: t('impersonations.start.accountRequired') }
        : {}

  const ttlSeconds = ttl.trim() === '' ? undefined : Number(ttl)

  const submit = (): void => {
    // The refused write's body is captured here: a step-up replay must
    // re-fire exactly this request, not whatever the fields hold later.
    if (account === null) {
      return
    }
    const typedAccount = account.id
    const body = {
      data: {
        account: typedAccount,
        tenant,
        ...(reason.trim() === '' ? {} : { reason }),
        ...(ttlSeconds === undefined || Number.isNaN(ttlSeconds)
          ? {}
          : { ttl_seconds: ttlSeconds }),
      },
    }
    const attempt = (): Promise<void> => {
      const release = onStarting?.()
      return feedback.run(async () => {
        try {
          const answer = await startImpersonation.mutateAsync(body)
          setAccount(null)
          setReason('')
          setTtl('')
          onStarted()
          notifyIdentityChange(identityChange, 'started')
          return accountNotice(
            t,
            {
              named: 'impersonations.current.started',
              unnamed: 'impersonations.current.startedUnnamed',
            },
            answer.grant.target === typedAccount
              ? { name: identity.name, username: identity.username }
              : { name: undefined, username: undefined },
          )
        } catch (error) {
          if (gate.intercept(error, () => void attempt())) {
            release?.()
            return null
          }
          release?.()
          throw error
        }
      })
    }
    void attempt()
  }

  return (
    <Box>
      <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
        {t('impersonations.start.precondition')}
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
        <AccountSelector
          label={t('impersonations.start.accountLabel')}
          value={account}
          onChange={setAccount}
        />
        <TextField
          size="small"
          value={reason}
          onChange={(event) => setReason(event.target.value)}
          label={t('impersonations.start.reasonLabel')}
        />
        <TextField
          size="small"
          value={ttl}
          onChange={(event) => setTtl(event.target.value)}
          label={t('impersonations.start.ttlLabel')}
        />
        <ActionButton
          type="button"
          emphasis="primary"
          busy={feedback.pending}
          unavailable={account === null || tenant === ''}
          {...submitExplanation}
          onClick={submit}
        >
          {t('impersonations.start.submit')}
        </ActionButton>
      </Box>
      {tenant === '' && (
        <Box sx={{ mt: 1 }}>
          <Typography id={tenantHintId} variant="body2" color="text.secondary">
            {t('impersonations.start.tenantHint')}
          </Typography>
        </Box>
      )}
    </Box>
  )
}
