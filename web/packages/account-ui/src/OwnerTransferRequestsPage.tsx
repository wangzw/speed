/**
 * OwnerTransferRequestsPage: the account-level surface where a member
 * confirms or declines an organization's request to take its ownership.
 */

import { useLayoutEffect, useRef, useState } from 'react'
import type { ReactElement } from 'react'
import { formatInstant } from '@speed/i18n'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Card from '@mui/material/Card'
import Chip from '@mui/material/Chip'
import Stack from '@mui/material/Stack'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getTenantAdminMeTransferRequestQueryKey,
  getTenantAdminMeTransferRequestQueryOptions,
  getTenantAdminMeTransferRequestsQueryKey,
  getTenantProfileGetQueryKey,
  useTenantAdminMeConfirmTransfer,
  useTenantAdminMeDeclineTransfer,
  useTenantAdminMeTransferRequest,
  useTenantAdminMeTransferRequests,
} from '@speed/api-sdk'
import type { MyTransferView } from '@speed/api-sdk'
import { tenantQueryKey } from '@speed/auth-core'
import {
  ActionButton,
  ConfirmDialog,
  errorCodeOf,
  httpStatusOf,
  PageLayout,
  SectionCard,
  useWriteFeedback,
  WriteFeedbackRegion as SharedWriteFeedbackRegion,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { useAccountUiErrorText } from './internal/error-text.js'
import { useAccountTimeZone } from './internal/time-zone.js'
import { useAccountUiTranslation } from './internal/translation.js'

/**
 * The host addresses the request by path and supplies the routes and
 * actions for opening it; the page reads and acts through the account's
 * transfer endpoints.
 */
export interface OwnerTransferRequestsPageProps {
  /** The request the detail view reads. Absent or empty renders the list. */
  readonly request?: string
  /** Builds the detail view's address for one request; absent leaves the
   * list's rows without a link. */
  readonly requestHref?: (id: string) => string
  /** The host's action for a row the list cannot link itself. */
  readonly onOpenRequest?: (id: string) => void
  /** The address of the list, for the detail view's way back. */
  readonly listHref?: string
  /** The host's action to open an organization the caller now owns. */
  readonly onOpenOrganization?: (tenant: string) => void | Promise<unknown>
}

const NOT_FOUND = 'transfer.not_found'
const EXPIRED = 'transfer.expired'
const NOT_PENDING = 'transfer.not_pending'
const CONFLICT = 'transfer.conflict'
const UNKNOWN = 'transfer.unknown'

function statusLabel(
  t: ReturnType<typeof useAccountUiTranslation>['t'],
  status: MyTransferView['status'],
): string {
  switch (status) {
    case 'pending':
      return t('transferRequests.statusValues.pending')
    case 'confirmed':
      return t('transferRequests.statusValues.confirmed')
    case 'declined':
      return t('transferRequests.statusValues.declined')
    case 'cancelled':
      return t('transferRequests.statusValues.cancelled')
    case 'expired':
      return t('transferRequests.statusValues.expired')
    default:
      return status
  }
}

/** A date-time as this account renders one, or the empty string when the
 * formatter cannot read the value. */
function displayInstant(
  value: string,
  language: string,
  timeZone: string,
): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime())
    ? ''
    : formatInstant(value, language, { timeZone })
}

/** The account-level surface where a member confirms or declines an
 * organization's request to take its ownership. */
export function OwnerTransferRequestsPage({
  request,
  requestHref,
  onOpenRequest,
  listHref,
  onOpenOrganization,
}: OwnerTransferRequestsPageProps = {}): ReactElement {
  const { t } = useAccountUiTranslation()
  const list = useTenantAdminMeTransferRequests()
  const detailEnabled = request !== undefined && request !== ''
  const detailOptions = useTenantAdminMeTransferRequest(request ?? '', {
    query: {
      queryKey: getTenantAdminMeTransferRequestQueryKey(request ?? ''),
      enabled: detailEnabled,
    },
  })

  const showDetail = detailEnabled
  const payload = showDetail ? detailOptions.data : list.data
  const pending = showDetail
    ? detailOptions.isPending && detailOptions.data === undefined
    : list.isPending && list.data === undefined
  const error = showDetail ? detailOptions.error : list.error
  const refetch = showDetail ? detailOptions.refetch : list.refetch

  return (
    <PageLayout
      title={t('transferRequests.title')}
      description={t('transferRequests.description')}
    >
      <AccountAsyncSection
        pending={pending}
        payload={payload}
        empty={
          !showDetail && (list.data?.requests?.length ?? 0) === 0
        }
        loading={
          <Typography variant="body2" role="status">
            {t('transferRequests.loading')}
          </Typography>
        }
        errorState={{
          title: t('transferRequests.error.title'),
          description: t('transferRequests.error.description'),
          retryLabel: t('transferRequests.retry'),
          onRetry: () => {
            void refetch()
          },
        }}
        emptyState={{
          title: t('transferRequests.emptyTitle'),
          description: t('transferRequests.empty'),
        }}
        error={error}
        refreshing={
          (showDetail ? detailOptions.isFetching : list.isFetching) &&
          payload !== undefined
        }
      >
        {() =>
          showDetail ? (
            <RequestDetail
              request={detailOptions.data?.request}
              listHref={listHref}
              onOpenOrganization={onOpenOrganization}
            />
          ) : (
            <RequestList
              requests={list.data?.requests ?? []}
              requestHref={requestHref}
              onOpenRequest={onOpenRequest}
            />
          )
        }
      </AccountAsyncSection>
    </PageLayout>
  )
}

function RequestFacts({
  request,
  language,
  timeZone,
}: {
  readonly request: MyTransferView
  readonly language: string
  readonly timeZone: string
}): ReactElement {
  const { t } = useAccountUiTranslation()
  return (
    <Box sx={{ display: 'flex', flexWrap: 'wrap', columnGap: 3, rowGap: 0.5 }}>
      <Typography variant="body2">
        {t('transferRequests.requestedAt', {
          requested: displayInstant(request.requested_at, language, timeZone),
        })}
      </Typography>
      <Typography variant="body2">
        {t('transferRequests.expiresAt', {
          expires: displayInstant(request.expires_at, language, timeZone),
        })}
      </Typography>
    </Box>
  )
}

function RequestList({
  requests,
  requestHref,
  onOpenRequest,
}: {
  readonly requests: readonly MyTransferView[]
  readonly requestHref?: (id: string) => string
  readonly onOpenRequest?: (id: string) => void
}): ReactElement {
  const { t, i18n } = useAccountUiTranslation()
  const timeZone = useAccountTimeZone()
  return (
    <SectionCard
      title={t('transferRequests.pendingTitle')}
      description={t('transferRequests.pendingDescription')}
    >
      <Stack component="ul" spacing={1.5} sx={{ listStyle: 'none', m: 0, p: 0 }}>
        {requests.map((request) => {
          const href = requestHref?.(request.id)
          return (
            <Card
              component="li"
              variant="outlined"
              key={request.id}
              sx={{ p: 2 }}
            >
              <Box
                sx={{
                  display: 'flex',
                  flexWrap: 'wrap',
                  alignItems: 'flex-start',
                  columnGap: 2,
                  rowGap: 1,
                }}
              >
                <Box sx={{ flex: '1 1 20rem', minWidth: 0 }}>
                  <Typography variant="subtitle1" component="h2">
                    {request.tenant}
                  </Typography>
                  <RequestFacts
                    request={request}
                    language={i18n.language}
                    timeZone={timeZone}
                  />
                </Box>
                {href !== undefined ? (
                  <Button
                    component="a"
                    href={href}
                    size="small"
                    variant="outlined"
                  >
                    {t('transferRequests.open')}
                  </Button>
                ) : onOpenRequest !== undefined ? (
                  <Button
                    type="button"
                    size="small"
                    variant="outlined"
                    onClick={() => onOpenRequest(request.id)}
                  >
                    {t('transferRequests.open')}
                  </Button>
                ) : null}
              </Box>
            </Card>
          )
        })}
      </Stack>
    </SectionCard>
  )
}

function RequestDetail({
  request,
  listHref,
  onOpenOrganization,
}: {
  readonly request: MyTransferView | undefined
  readonly listHref?: string
  readonly onOpenOrganization?: (tenant: string) => void | Promise<unknown>
}): ReactElement | null {
  const { t, i18n } = useAccountUiTranslation()
  const timeZone = useAccountTimeZone()
  const queryClient = useQueryClient()
  const confirm = useTenantAdminMeConfirmTransfer()
  const decline = useTenantAdminMeDeclineTransfer()
  const resolveError = useAccountUiErrorText()
  const feedback = useWriteFeedback()
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [declineOpen, setDeclineOpen] = useState(false)
  // Only the control that started the write reports busy.
  const [pendingAction, setPendingAction] = useState<
    'confirm' | 'decline' | null
  >(null)
  // The card is the focus landing when a landed decision removes both
  // controls; the group is captured when the write starts so the landing can
  // tell a lost focus from one the operator moved elsewhere.
  const cardRegion = useRef<HTMLDivElement | null>(null)
  const actionGroup = useRef<HTMLDivElement | null>(null)
  const pendingLanding = useRef<{
    readonly group: HTMLElement | null
  } | null>(null)

  // Focus moves to the card only when the group's removal is what took it; a
  // connected control outside the group means the operator chose that place.
  useLayoutEffect(() => {
    const target = pendingLanding.current
    if (target === null || request === undefined) {
      return
    }
    if (request.status === 'pending') {
      // Judged on the read's settled answer, not this render's, so a re-read
      // that already reported a terminal state is not mistaken for one that
      // still carries the request.
      const reread = queryClient.getQueryData<{
        readonly request: MyTransferView
      }>(getTenantAdminMeTransferRequestQueryKey(request.id))
      if (
        pendingAction === null &&
        (reread === undefined || reread.request.status === 'pending')
      ) {
        pendingLanding.current = null
      }
      return
    }
    pendingLanding.current = null
    if (!focusFellWith(target.group)) {
      return
    }
    cardRegion.current?.focus()
  }, [request, pendingAction])

  if (request === undefined) {
    return null
  }

  const resolve = (code: string): string => {
    switch (code) {
      case NOT_FOUND:
        return t('transferRequests.error.notFound')
      case EXPIRED:
        return t('transferRequests.error.expired')
      case NOT_PENDING:
        return t('transferRequests.error.notPending')
      case CONFLICT:
        return t('transferRequests.error.conflict')
      default:
        return t('transferRequests.error.unknown')
    }
  }

  /** Resolves on the detail read, keeping the write in flight until the
   * decision is reported. */
  const invalidate = (tenant: string): Promise<void> => {
    void queryClient.invalidateQueries({
      queryKey: getTenantAdminMeTransferRequestsQueryKey(),
    })
    const reread = queryClient.invalidateQueries({
      queryKey: getTenantAdminMeTransferRequestQueryKey(request.id),
    })
    // The tenant surface's reads are tenant-prefixed, so invalidate both the
    // bare and the prefixed key.
    void queryClient.invalidateQueries({
      queryKey: getTenantProfileGetQueryKey(tenant),
    })
    void queryClient.invalidateQueries({
      queryKey: tenantQueryKey(tenant, ...getTenantProfileGetQueryKey(tenant)),
    })
    return reread
  }

  /** The refusal as this page words it; a conflict re-reads the detail. */
  const requestFailure = async (
    error: unknown,
  ): Promise<Error & { readonly code: string }> => {
    const status = httpStatusOf(error)
    if (status === 404) {
      return failure(NOT_FOUND)
    }
    if (status === 403 && errorCodeOf(error) === 'authn.contact_required') {
      return failure('authn.contact_required')
    }
    if (status === 409) {
      const reread = await queryClient.fetchQuery(
        getTenantAdminMeTransferRequestQueryOptions(request.id),
      )
      if (reread.request.status === 'expired') {
        return failure(EXPIRED)
      }
      if (reread.request.status !== 'pending') {
        return failure(NOT_PENDING)
      }
      return failure(CONFLICT)
    }
    return failure(UNKNOWN)
  }

  const runConfirm = (): void => {
    pendingLanding.current = { group: actionGroup.current }
    setPendingAction('confirm')
    void feedback
      .run(async () => {
        try {
          await confirm.mutateAsync({ request: request.id })
        } catch (error) {
          throw await requestFailure(error)
        }
        await invalidate(request.tenant)
        return t('transferRequests.confirmed', { tenant: request.tenant })
      })
      .finally(() => setPendingAction(null))
  }

  const runDecline = (): void => {
    pendingLanding.current = { group: actionGroup.current }
    setPendingAction('decline')
    void feedback
      .run(async () => {
        try {
          await decline.mutateAsync({ request: request.id })
        } catch (error) {
          throw await requestFailure(error)
        }
        await invalidate(request.tenant)
        return t('transferRequests.declined', { tenant: request.tenant })
      })
      .finally(() => setPendingAction(null))
  }

  const pending = request.status === 'pending'
  const confirming = pendingAction === 'confirm'
  const declining = pendingAction === 'decline'

  return (
    <SectionCard
      title={request.tenant}
      description={t('transferRequests.detailDescription')}
    >
      <Box
        // Reachable by script without joining the Tab order.
        ref={cardRegion}
        role="group"
        aria-label={request.tenant}
        tabIndex={-1}
      >
        {listHref !== undefined ? (
          <Button component="a" href={listHref} size="small" sx={{ mb: 2 }}>
            {t('transferRequests.back')}
          </Button>
        ) : null}
        <Stack spacing={1}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
            <Typography variant="body2">
              {t('transferRequests.status')}
            </Typography>
            <Chip size="small" variant="outlined" label={statusLabel(t, request.status)} />
          </Box>
          <RequestFacts
            request={request}
            language={i18n.language}
            timeZone={timeZone}
          />
        </Stack>
        {pending ? (
          <Box
            ref={actionGroup}
            sx={{ display: 'flex', flexWrap: 'wrap', gap: 1.5, mt: 2 }}
          >
            <ActionButton
              type="button"
              emphasis="primary"
              busy={confirming}
              unavailable={declining}
              onClick={() => {
                feedback.reset()
                setConfirmOpen(true)
              }}
            >
              {t('transferRequests.confirm')}
            </ActionButton>
            <ActionButton
              type="button"
              busy={declining}
              unavailable={confirming}
              onClick={() => {
                feedback.reset()
                setDeclineOpen(true)
              }}
            >
              {t('transferRequests.decline')}
            </ActionButton>
          </Box>
        ) : (
          <>
            <Typography variant="body2" color="text.secondary" sx={{ mt: 2 }}>
              {t('transferRequests.terminal')}
            </Typography>
            {request.status === 'confirmed' &&
            onOpenOrganization !== undefined ? (
              <Button
                type="button"
                variant="outlined"
                sx={{ mt: 1.5 }}
                onClick={() => {
                  void onOpenOrganization(request.tenant)
                }}
              >
                {t('transferRequests.openOrganization')}
              </Button>
            ) : null}
          </>
        )}
        <SharedWriteFeedbackRegion
          outcome={feedback.outcome}
          resolve={(code) =>
            code === 'authn.contact_required'
              ? resolveError(code)
              : resolve(code)
          }
          sx={{ mt: 2 }}
        />
      </Box>

      <ConfirmDialog
        open={confirmOpen}
        variant="danger"
        doubleConfirm
        title={t('transferRequests.confirmTitle', { tenant: request.tenant })}
        message={t('transferRequests.confirmMessage', {
          tenant: request.tenant,
        })}
        confirmLabel={t('transferRequests.confirm')}
        cancelLabel={t('transferRequests.cancel')}
        confirmLoading={confirming}
        onCancel={() => setConfirmOpen(false)}
        onConfirm={() => {
          setConfirmOpen(false)
          runConfirm()
        }}
      />
      <ConfirmDialog
        open={declineOpen}
        title={t('transferRequests.declineTitle', { tenant: request.tenant })}
        message={t('transferRequests.declineMessage', {
          tenant: request.tenant,
        })}
        confirmLabel={t('transferRequests.decline')}
        cancelLabel={t('transferRequests.cancel')}
        confirmLoading={declining}
        onCancel={() => setDeclineOpen(false)}
        onConfirm={() => {
          setDeclineOpen(false)
          runDecline()
        }}
      />
    </SectionCard>
  )
}

function focusFellWith(removed: HTMLElement | null): boolean {
  const active = document.activeElement
  if (active === null || active === document.body) {
    return true
  }
  if (!(active instanceof HTMLElement) || !active.isConnected) {
    return true
  }
  return removed !== null && removed.contains(active)
}

function failure(code: string): Error & { readonly code: string } {
  return Object.assign(new Error(code), { code })
}
