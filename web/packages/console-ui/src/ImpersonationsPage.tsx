/**
 * ImpersonationsPage: the impersonation administration page of the
 * console.
 *
 * It carries two impersonation surfaces that share one page:
 *
 *  - the caller's own impersonation session, over /auth/impersonation:
 *    the status read (GET) names the impersonation currently in force on
 *    the operator's own session (a bare 404 answers "not impersonating"),
 *    a start form (POST) begins one -- its request body is the endpoint's
 *    own {account, tenant, reason?, ttl_seconds?} shape -- and an end
 *    action (DELETE) stops it. These three are the caller-session
 *    operations the generated @speed/api-sdk surface ships
 *    (useAuthnImpersonationStatus / Start / End), so the page calls them
 *    directly and invalidates the status read through the generated key
 *    factory; the start control is offered only while no impersonation is
 *    in force (the module refuses a second start on one session with 409);
 *    the start form's target tenant is the page's own tenant selection --
 *    the tenant is a required part of the request (the module refuses an
 *    empty one) and the target account must be a member of it, so the
 *    form has no separate tenant input;
 *
 *  - the tenant's impersonation records, over /auth/admin/impersonations:
 *    the record list (GET) and one record's termination (DELETE). The read
 *    names its tenant as a required query parameter, so the page opens that
 *    half with the same tenant selection the start form uses, fed by the
 *    platform roster exactly as the authorization and SSO pages do with
 *    theirs, and keeps the read disabled until a tenant is chosen. The
 *    tenant is an argument of the request, not the session's current
 *    tenant. These two are operations on the console operations bundle, so
 *    that half builds no client and writes no query key.
 *
 * The status read's bare 404 is a settled answer, not a failure: it means
 * no impersonation is in force, so the page renders it as that content --
 * never as the error placeholder and never as a stale marker -- and the
 * cached grant is discarded, because the read that just said "none" is
 * the authority. Every other status failure renders through the console's
 * shared read branches. A successful end resets the status read's cache
 * and re-reads it through the generated key, so the section can only land
 * on what the server answers next.
 *
 * A settled grant collapses the record half: an impersonated identity
 * reaches only a closed set of routes, so the gateway refuses the roster
 * read and the record read alike. Once the status read names a grant the
 * page issues neither, and replaces the tenant selector, the record list
 * and its terminate controls with one line saying the records are
 * available again after the impersonation ends. The record read also
 * waits for the status read to settle, so a page opened while an
 * impersonation is already in force never issues it. A status read that
 * is still pending, or that failed with anything but the bare 404, is not
 * evidence of an impersonation: the record half renders as it always has.
 * Ending an impersonation drops the cached record pages along with
 * resetting the status read, so the surface that returns reads again
 * instead of presenting the pre-impersonation list as current.
 *
 * Both halves report their writes through feedback scopes: the start and
 * the end share one scope whose region lives in the current-impersonation
 * section (the start form unmounts when the start lands, so a region
 * inside it would take the success message with it), and the record
 * table's terminate actions own a scope with the region under the table.
 * A start or end refused for insufficient step-up hands over to the
 * elevation gate and leaves the scope empty; the replay is a new run.
 *
 * A record is in force while its `ended_at` is the zero instant (the
 * sentinel the view carries); the state column says so and the terminate
 * control is offered only then. Every account identifier the page shows --
 * the record's actor and target and the in-force panel's target -- renders
 * through the shared account identity unit, with the recorded identifier
 * kept as the column's full value. The timestamps render in UTC through
 * Intl. The page opens with ui-kit's PageHeader.
 */

import { useEffect, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
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
import {
  DataTable,
  EmptyState,
  ListSkeleton,
  PageHeader,
  useWriteFeedback,
} from '@speed/ui-kit'
import type { DataTableColumn, WriteFeedback } from '@speed/ui-kit'
import { AccountIdentity } from './internal/account-identity.js'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { formatUtc, isZeroInstant } from './internal/datetime.js'
import { useConsoleStepUp } from './internal/step-up-gate.js'
import type { ConsoleStepUp } from './internal/step-up-gate.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { TenantScope, tenantFromLocation } from './internal/tenant-scope.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleImpersonation } from './operations.js'

/** The caller's own impersonation status as the section renders it: the
 * settled grant, or a settled "not impersonating" answer. */
interface ImpersonationStatusView {
  readonly grant: AuthnImpersonationGrant | undefined
}

/**
 * Whether a failure is the status read's "no impersonation in force": the
 * endpoint answers a bare 404, which the transport surfaces either as an
 * HTTP 404 or as the client.http.404 code -- the same structural reading
 * the console's forbidden branch makes of a 403.
 */
function isNotImpersonating(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) {
    return false
  }
  const candidate = error as { readonly status?: unknown; readonly code?: unknown }
  return candidate.status === 404 || candidate.code === 'client.http.404'
}

export function ImpersonationsPage() {
  const { t, i18n } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const queryClient = useQueryClient()

  const [tenant, setTenant] = useState(tenantFromLocation)

  // The current-impersonation section's scope: the start and the end are
  // the same control group (one object, one result location), and the
  // start form unmounts when a start lands, so the page holds the slot.
  const currentFeedback = useWriteFeedback()
  // The record table's scope: its terminate actions share one result
  // location under the table.
  const recordsFeedback = useWriteFeedback()
  const stepUp = useConsoleStepUp()

  // The caller's own impersonation session, over /auth/impersonation. These
  // are the generated caller-session operations (not the administration
  // bundle): the page reads the status, ends an in-force impersonation, and
  // offers the start form only while none is in force.
  const status = useAuthnImpersonationStatus()

  // The bare 404 is settled content, not a failure: it renders as "not
  // impersonating" and discards whatever grant the cache still holds.
  const notImpersonating = isNotImpersonating(status.error)
  const statusError = notImpersonating ? undefined : status.error
  const statusPayload: ImpersonationStatusView | undefined = notImpersonating
    ? { grant: undefined }
    : status.data === undefined
      ? undefined
      : { grant: status.data.grant }
  const grant = statusPayload?.grant
  const statusPending = status.isPending && statusPayload === undefined
  // A settled read that names a grant is the only evidence of an
  // impersonation; a pending read and a failure are "status unknown" and
  // never count as one. The start form is offered only on a settled "not
  // impersonating" answer. The read's error is null rather than undefined
  // when there is none.
  const statusFailed = statusError !== undefined && statusError !== null
  const impersonating = !statusFailed && grant !== undefined
  const notInForce = statusPayload !== undefined && grant === undefined
  // The record read waits for the status read to settle -- a read on a
  // session that turns out to be impersonating is only refused -- and
  // stays disabled while a grant is in force.
  const recordsEnabled = !status.isPending && !impersonating && tenant !== ''

  const impersonations = operations.useImpersonations(
    { tenant },
    { enabled: recordsEnabled },
  )
  const revokeImpersonation = operations.useRevokeImpersonation()

  const endImpersonation = useAuthnImpersonationEnd({
    mutation: {
      onSuccess: () => {
        // The status read is the authority on what is in force: drop the
        // cached grant and re-read, so the section lands on the server's
        // next answer rather than a grant the end just retired.
        void queryClient.resetQueries({
          queryKey: getAuthnImpersonationStatusQueryKey(),
        })
        // The cached record pages predate the end and describe a state it
        // just changed. Drop them so the returning surface reads again
        // instead of presenting the old list as current.
        queryClient.removeQueries({
          queryKey: getAuthnAdminImpersonationsListQueryKey(),
        })
      },
    },
  })

  // The tenant is an argument of every write in both sections, so a change
  // clears their results: the old outcome would read as describing the new
  // tenant.
  useEffect(() => {
    currentFeedback.reset()
    recordsFeedback.reset()
  }, [tenant, currentFeedback.reset, recordsFeedback.reset])

  const endAttempt = (): Promise<void> =>
    currentFeedback.run(async () => {
      try {
        await endImpersonation.mutateAsync()
        return t('impersonations.current.ended', { account: grant?.target ?? '' })
      } catch (error) {
        // A refusal the server answers while the session is still
        // established is a strength shortfall: the gate takes the write
        // over and this attempt settles without touching the slot. The
        // replayed write is a new run.
        if (stepUp.intercept(error, () => void endAttempt())) {
          return null
        }
        throw error
      }
    })

  const terminate = (record: ConsoleImpersonation): void => {
    const attempt = (): Promise<void> =>
      recordsFeedback.run(async () => {
        try {
          await revokeImpersonation.mutateAsync({ id: record.id })
          // A termination changes what the caller's own status read may
          // name, so that read is refreshed alongside the list the bundle
          // already invalidates.
          void status.refetch()
          return t('impersonations.terminated', { account: record.target })
        } catch (error) {
          if (stepUp.intercept(error, () => void attempt())) {
            return null
          }
          throw error
        }
      })
    void attempt()
  }

  const columns: DataTableColumn<ConsoleImpersonation>[] = [
    {
      id: 'actor',
      header: t('impersonations.column.actor'),
      cellWrap: 'nowrap',
      cellFullValue: (record) => record.actor,
      cell: (record) => <AccountIdentity account={record.actor} />,
    },
    {
      id: 'target',
      header: t('impersonations.column.target'),
      cellWrap: 'nowrap',
      cellFullValue: (record) => record.target,
      cell: (record) => <AccountIdentity account={record.target} />,
    },
    {
      id: 'tenant',
      header: t('impersonations.column.tenant'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cellFullValue: (record) => record.tenant,
      cell: (record) => record.tenant,
    },
    {
      id: 'started',
      header: t('impersonations.column.started'),
      cellWrap: 'nowrap',
      cell: (record) => formatUtc(record.started_at, i18n.language),
    },
    {
      id: 'ends',
      header: t('impersonations.column.ends'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (record) => formatUtc(record.ends_at, i18n.language),
    },
    {
      id: 'state',
      header: t('impersonations.column.state'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (record) =>
        isZeroInstant(record.ended_at) ? (
          <Typography variant="body2" color="warning.main">
            {t('impersonations.inForce')}
          </Typography>
        ) : (
          <Typography variant="body2" color="text.secondary">
            {t('impersonations.ended', { reason: record.end_reason ?? '' })}
          </Typography>
        ),
    },
    {
      id: 'actions',
      header: t('impersonations.column.actions'),
      cellWrap: 'nowrap',
      cell: (record) => (
        <Button
          type="button"
          size="small"
          variant="outlined"
          color="error"
          disabled={!isZeroInstant(record.ended_at) || recordsFeedback.pending}
          loading={recordsFeedback.pending}
          onClick={() => terminate(record)}
        >
          {t('impersonations.terminate')}
        </Button>
      ),
    },
  ]

  const rows = impersonations.data
  const pending = impersonations.isPending && rows === undefined

  return (
    <Box>
      <PageHeader
        title={t('impersonations.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('impersonations.title') },
        ]}
        actions={
          <Button
            type="button"
            variant="outlined"
            size="small"
            onClick={() => {
              void status.refetch()
              // The record read is refused while impersonating and waits for
              // the status read to settle; once it does, enabling the read
              // fetches it.
              if (recordsEnabled) void impersonations.refetch()
            }}
          >
            {t('common.refresh')}
          </Button>
        }
        sx={{ mb: 2 }}
      />

      <Box component="section" sx={{ mb: 3 }}>
        <Typography
          component="h2"
          variant="h2"
          sx={{ fontSize: '1.25rem', mb: 1 }}
        >
          {t('impersonations.current.title')}
        </Typography>
        <ConsoleAsyncSection
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
                      ends: formatUtc(settled.grant.ends_at, i18n.language),
                    })}
                  </Typography>
                </Stack>
                <Button
                  type="button"
                  size="small"
                  variant="outlined"
                  color="error"
                  disabled={currentFeedback.pending}
                  loading={currentFeedback.pending}
                  onClick={() => void endAttempt()}
                >
                  {t('impersonations.current.end')}
                </Button>
              </Alert>
            )
          }
        </ConsoleAsyncSection>

        {notInForce && (
          <Box sx={{ mt: 2 }}>
            <Typography
              component="h2"
              variant="h2"
              sx={{ fontSize: '1.25rem', mb: 1 }}
            >
              {t('impersonations.start.title')}
            </Typography>
            <StartImpersonationForm
              tenant={tenant}
              feedback={currentFeedback}
              stepUp={stepUp}
              // A start writes a record into the selected tenant's list as
              // well as onto the operator's own session.
              onStarted={() => void impersonations.refetch()}
            />
          </Box>
        )}

        {/* The start form unmounts when a start lands; this region stays
            with the section, so the success message is not unmounted with
            the form that produced it. */}
        <WriteFeedbackRegion outcome={currentFeedback.outcome} />
      </Box>

      {impersonating ? (
        // The record half is not offered through an impersonated identity:
        // the gateway refuses the roster read and the record read, and a
        // list rendered from refused reads would read as "no records". One
        // line replaces the selector, the list and its terminate controls;
        // the current-impersonation section and its end control remain.
        <Typography variant="body2" color="text.secondary" sx={{ mb: 3 }}>
          {t('impersonations.recordsUnavailable')}
        </Typography>
      ) : (
        <>
          <Box component="section" sx={{ mb: 3 }}>
            <TenantScope
              value={tenant}
              onSelect={setTenant}
              disabled={currentFeedback.pending || recordsFeedback.pending}
            />
          </Box>

          {tenant === '' ? (
            <EmptyState
              headingLevel="h2"
              title={t('impersonations.needTenant.title')}
              description={t('impersonations.needTenant.description')}
            />
          ) : (
            <>
              <ConsoleAsyncSection
                pending={pending}
                payload={rows}
                error={impersonations.error}
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
              </ConsoleAsyncSection>
              {/* The table and its row actions share one result location. */}
              <WriteFeedbackRegion outcome={recordsFeedback.outcome} />
            </>
          )}
        </>
      )}

      {stepUp.session !== null && (
        <StepUpChallenge
          open={stepUp.gate.open}
          session={stepUp.session}
          onSuccess={stepUp.gate.succeed}
          onCancel={stepUp.gate.cancel}
        />
      )}
    </Box>
  )
}

/**
 * StartImpersonationForm: the control that begins an impersonation on the
 * operator's own session (POST /auth/impersonation). It renders the
 * endpoint's request-body fields -- a required target account, an optional
 * reason, an optional TTL in whole seconds, and the page's tenant selection
 * as the required target tenant -- and reports the write through the
 * section's feedback scope. The target account must be a member of that
 * tenant, so the form takes the tenant as a prop instead of offering a
 * second, unrelated input; the submit stays disabled until a tenant is
 * selected. A landed start clears the form and invalidates the status read
 * through the generated key factory, so the page's status section converges
 * on the record the server just wrote; a refused start keeps the values so
 * the operator can correct them. A precondition line above the controls
 * states the factor-age requirement: the server answers an unmet
 * `MFA.Seasoned` with the same step-up refusal a weak session gets, and this
 * line is what keeps an operator from retrying a sign-in that cannot help.
 */
function StartImpersonationForm({
  tenant,
  feedback,
  stepUp,
  onStarted,
}: {
  readonly tenant: string
  readonly feedback: WriteFeedback
  readonly stepUp: ConsoleStepUp
  /** Fired after a start lands, so the page re-reads the tenant's records. */
  readonly onStarted: () => void
}) {
  const { t } = useConsoleUiTranslation()
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

  const [account, setAccount] = useState('')
  const [reason, setReason] = useState('')
  const [ttl, setTtl] = useState('')

  const ttlSeconds = ttl.trim() === '' ? undefined : Number(ttl)

  const submit = (): void => {
    // The refused write's body is captured here: a step-up replay must
    // re-fire exactly this request, not whatever the fields hold later.
    const body = {
      data: {
        account,
        tenant,
        ...(reason.trim() === '' ? {} : { reason }),
        ...(ttlSeconds === undefined || Number.isNaN(ttlSeconds)
          ? {}
          : { ttl_seconds: ttlSeconds }),
      },
    }
    const attempt = (): Promise<void> =>
      feedback.run(async () => {
        try {
          const answer = await startImpersonation.mutateAsync(body)
          setAccount('')
          setReason('')
          setTtl('')
          onStarted()
          return t('impersonations.current.started', {
            account: answer.grant.target,
          })
        } catch (error) {
          // The gate takes a step-up refusal over; the attempt settles
          // without touching the slot, and a won challenge replays this
          // body as a new run.
          if (stepUp.intercept(error, () => void attempt())) {
            return null
          }
          throw error
        }
      })
    void attempt()
  }

  return (
    <Box>
      {/* The precondition stays visible above the controls: the server
          answers an unmet factor-age requirement with the same step-up
          refusal a weak session gets, and signing in again cannot fix that
          one, so this is the only place that tells the operator why the
          start keeps failing. */}
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
        <TextField
          size="small"
          value={account}
          onChange={(event) => setAccount(event.target.value)}
          label={t('impersonations.start.accountLabel')}
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
        <Button
          type="button"
          variant="contained"
          disabled={
            feedback.pending ||
            account.trim() === '' ||
            tenant === ''
          }
          loading={feedback.pending}
          onClick={submit}
        >
          {t('impersonations.start.submit')}
        </Button>
      </Box>
      {tenant === '' && (
        <Box sx={{ mt: 1 }}>
          <Typography variant="body2" color="text.secondary">
            {t('impersonations.start.tenantHint')}
          </Typography>
        </Box>
      )}
    </Box>
  )
}
