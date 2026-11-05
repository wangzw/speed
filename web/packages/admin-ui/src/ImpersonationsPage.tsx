/**
 * ImpersonationsPage: the impersonation administration page of the
 * console.
 *
 * It carries two impersonation surfaces that share one page:
 *
 *  - the caller's own impersonation session, over /api/v1/auth/impersonation:
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
 *  - the tenant's impersonation records, over /api/v1/system/tenants/{tenant}/impersonations:
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
 *
 * The tenant scope is optionally controlled: `tenant` locks the page to the
 * given value and hides the selector, `defaultTenant` seeds the page's own
 * selection ahead of the URL's `tenant` parameter, and `onTenantChange`
 * reports each re-selection. Given none of them the page opens on the URL's
 * parameter and the selector changes it, as before.
 *
 * The declaration carries the caller-session feature, so a host narrows
 * the page out by feature presence; the page adds no gate of its own and
 * both halves render together.
 */

import { useCallback, useEffect, useState } from 'react'
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
import { useStepUpGate } from '@speed/auth-core'
import type { StepUpGate } from '@speed/auth-core'
import { formatInstant } from '@speed/i18n'
import {
  DataTable,
  EmptyState,
  ListSkeleton,
  LONG_TEXT_COLUMN_MAX_WIDTH,
  PRIMARY_COLUMN_MAX_WIDTH,
  RefreshButton,
  SectionCard,
  httpStatusOf,
  pageRefreshRead,
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
import { AdminAsyncSection } from './internal/async-section.js'
import { isZeroInstant } from './internal/datetime.js'
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
import { useAdminOperations } from './operations.js'
import type { AdminImpersonation } from './operations.js'
import { AdminPageHeader } from './internal/page-header.js'

/** The caller's own impersonation status as the section renders it: the
 * settled grant, or a settled "not impersonating" answer. */
interface ImpersonationStatusView {
  readonly grant: AuthnImpersonationGrant | undefined
}

/**
 * The record table's terminate control. It resolves the record's target
 * through the same account identity unit the row's target cell renders (one
 * cached read), so a landed termination names the account in the four
 * shared shapes instead of by its opaque id.
 */
function TerminateButton({
  record,
  disabled,
  loading,
  onTerminate,
}: {
  readonly record: AdminImpersonation
  readonly disabled: boolean
  readonly loading: boolean
  readonly onTerminate: (
    record: AdminImpersonation,
    identity: AccountIdentityValue,
  ) => void
}) {
  const { t } = useAdminUiTranslation()
  const identity = useAccountIdentity(record.target)
  return (
    <Button
      type="button"
      size="small"
      variant="outlined"
      color="error"
      disabled={disabled}
      loading={loading}
      onClick={() =>
        onTerminate(record, {
          name: identity.name,
          identifier: identity.identifier,
        })
      }
    >
      {t('impersonations.terminate')}
    </Button>
  )
}

/**
 * Whether a failure is the status read's "no impersonation in force": the
 * endpoint answers a bare 404, which the transport surfaces either as an
 * HTTP 404 or as the client.http.404 code -- read structurally, the same
 * reading the console's forbidden branch makes of a 403.
 */
function isNotImpersonating(error: unknown): boolean {
  return httpStatusOf(error) === 404
}

/**
 * Reports a settled start or end to the host, outside the write attempt:
 * fire-and-forget, with the rejection wrapped, so a host action that fails
 * cannot turn a landed write into a failure the page renders.
 */
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

/**
 * The reasons a record ends, mapped from the wire value to the namespace
 * leaf that names it. A reason outside this set renders as recorded.
 */
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

export interface ImpersonationsPageProps {
  /**
   * A controlled tenant scope: when given, the record read names this tenant
   * and the selector is not rendered. An empty string is a controlled "no
   * tenant".
   */
  readonly tenant?: string
  /**
   * The page's initial tenant when it is not controlled. Wins over the
   * tenant query parameter.
   */
  readonly defaultTenant?: string
  /** Reports each re-selection made through the page's own selector. */
  readonly onTenantChange?: (tenant: string) => void
}

export function ImpersonationsPage({
  tenant: controlledTenant,
  defaultTenant,
  onTenantChange,
}: ImpersonationsPageProps = {}) {
  const { t, i18n } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const queryClient = useQueryClient()

  const [innerTenant, setInnerTenant] = useState(
    () => defaultTenant ?? tenantFromLocation(),
  )
  const tenant = controlledTenant ?? innerTenant
  const tenantControlled = controlledTenant !== undefined

  // The current-impersonation section's scope: the start and the end are
  // the same control group (one object, one result location), and the
  // start form unmounts when a start lands, so the page holds the slot.
  const currentFeedback = useWriteFeedback()
  // The record table's scope: its terminate actions share one result
  // location under the table.
  const recordsFeedback = useWriteFeedback()
  // The console session, and the step-up gate over it: the gate treats a
  // write as a shortfall exactly when the session says so (a 401 that ends
  // the session is not a shortfall, and arming a step-up for it would
  // offer a verification that cannot help).
  const session = useAdminSession()
  const gate = useStepUpGate<() => void>(
    (retry) => retry(),
    (error) => session !== null && session.isStepUpShortfall(error),
  )
  // The host's identity-change action (the shell publishes it). A start
  // and an end both swap whose identity every later read belongs to, so
  // each reports to the same seam once its write settles.
  const identityChange = useAdminImpersonationChange()

  // The caller's own impersonation session, over /api/v1/auth/impersonation. These
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
  // The grant's target as a landed end names it. The current section renders
  // the same identity through the shared unit, so this read is cached and
  // shared with it; an absent grant resolves nothing and the notice stays
  // neutral instead of naming the recorded id.
  const endIdentity = useAccountIdentity(grant?.target)
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

  // Both sections' scopes clear on a context change: another tenant, or the
  // refresh action, which re-reads the status read and -- when the record
  // surface renders -- the record read.
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
          queryKey: getAuthnAdminImpersonationsListQueryKey(tenant),
        })
      },
    },
  })

  // The tenant is an argument of every write in both sections, so a change
  // clears their results: the old outcome would read as describing the new
  // tenant.
  useEffect(() => {
    resetPageFeedback()
  }, [tenant, resetPageFeedback])

  const endAttempt = (): Promise<void> =>
    currentFeedback.run(async () => {
      try {
        await endImpersonation.mutateAsync()
      } catch (error) {
        // A refusal the server answers while the session is still
        // established is a strength shortfall: the gate takes the write
        // over and this attempt settles without touching the slot. The
        // replayed write is a new run.
        if (gate.intercept(error, () => void endAttempt())) {
          return null
        }
        // A bare 404 is the settled "there is no impersonation to end"
        // (ended elsewhere, expired or terminated). It is not a failure:
        // the page closes exactly as a successful end does.
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
      // The write settled: the host re-reads the identity, without the
      // page waiting for it.
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
    const attempt = (): Promise<void> =>
      recordsFeedback.run(async () => {
        try {
          await revokeImpersonation.mutateAsync({ id: record.id, tenant })
          // A termination changes what the caller's own status read may
          // name, so that read is refreshed alongside the list the bundle
          // already invalidates.
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
        }
      })
    void attempt()
  }

  // A closed-set reason renders its own label; anything else (including an
  // empty reason) is shown as the record carries it.
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
      cellFullValue: (record) => record.actor,
      // A long-text identity beside the record's primary one: the flat
      // long-text cap bounds it at every tier, and the complete value
      // stays on the cell title and the copy control.
      contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
      cell: (record) => <AccountIdentity account={record.actor} />,
    },
    {
      id: 'target',
      header: t('impersonations.column.target'),
      cellWrap: 'nowrap',
      cellFullValue: (record) => record.target,
      // The record's primary identity: it never hides, and the shared
      // whole-cell cap -- which counts the copy control -- keeps the
      // terminate action on screen at every tier.
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (record) => <AccountIdentity account={record.target} />,
    },
    {
      id: 'tenant',
      header: t('impersonations.column.tenant'),
      // The page is addressed by one tenant, so every row repeats the
      // selector above it: the tenant is the first column shed. Its
      // identifier has no length bound, so the long-text cap keeps the
      // `lg` tier's total within its floor.
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
      cell: (record) =>
        formatInstant(record.started_at, i18n.language, { timeZone: 'UTC' }),
    },
    {
      id: 'ends',
      header: t('impersonations.column.ends'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (record) =>
        formatInstant(record.ends_at, i18n.language, { timeZone: 'UTC' }),
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
            {t('impersonations.ended', {
              reason: endReasonLabel(record.end_reason ?? ''),
            })}
          </Typography>
        ),
    },
    {
      id: 'actions',
      header: t('impersonations.column.actions'),
      cellWrap: 'nowrap',
      cell: (record) => (
        <TerminateButton
          record={record}
          disabled={!isZeroInstant(record.ended_at) || recordsFeedback.pending}
          loading={recordsFeedback.pending}
          onTerminate={terminate}
        />
      ),
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

      <SectionCard title={t('impersonations.current.title')} sx={{ mb: 3 }}>
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
                      ends: formatInstant(settled.grant.ends_at, i18n.language, {
                        timeZone: 'UTC',
                      }),
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
      </SectionCard>

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
                {/* The table and its row actions share one result location. */}
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

/**
 * StartImpersonationForm: the control that begins an impersonation on the
 * operator's own session (POST /api/v1/auth/impersonation). It renders the
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
 *
 * Before the write goes out it calls the host's `onImpersonationStarting`
 * hook, so a host frame that speaks about the operator can enter its
 * "starting" state ahead of the request. The returned release runs when
 * the write does not land -- refused, intercepted by the step-up gate, or
 * thrown. A write that lands is not released here: the host releases it
 * once its `onImpersonationChange('started')` action settles, because the
 * host is the one that decides how the new identity takes over.
 */
function StartImpersonationForm({
  tenant,
  feedback,
  gate,
  identityChange,
  onStarted,
}: {
  readonly tenant: string
  readonly feedback: WriteFeedback
  /** The step-up gate over that session. */
  readonly gate: StepUpGate<() => void>
  /** The host's identity-change action (see ImpersonationsPage). */
  readonly identityChange:
    | ((change: AdminImpersonationChange) => void | Promise<void>)
    | undefined
  /** Fired after a start lands, so the page re-reads the tenant's records. */
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

  const [account, setAccount] = useState('')
  const [reason, setReason] = useState('')
  const [ttl, setTtl] = useState('')
  // The target account's readable identity, resolved through the same unit
  // the in-force section renders. An unresolved read leaves the notice
  // neutral; the recorded id is never its fallback.
  const identity = useAccountIdentity(
    account.trim() === '' ? undefined : account.trim(),
  )

  const ttlSeconds = ttl.trim() === '' ? undefined : Number(ttl)

  const submit = (): void => {
    // The refused write's body is captured here: a step-up replay must
    // re-fire exactly this request, not whatever the fields hold later.
    const typedAccount = account
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
    const attempt = (): Promise<void> => {
      // The host enters its "starting" state before the request goes out;
      // the returned release runs if this attempt does not land. A landed
      // write is the host's to release once its change action settles.
      const release = onStarting?.()
      return feedback.run(async () => {
        try {
          const answer = await startImpersonation.mutateAsync(body)
          setAccount('')
          setReason('')
          setTtl('')
          onStarted()
          // The write settled: the host re-reads the identity and moves
          // the page, without the form waiting for it.
          notifyIdentityChange(identityChange, 'started')
          // The server's grant is the authority on which account is being
          // impersonated. The identity read above is keyed to the typed
          // account, so it names the grant only when the two agree; a grant
          // naming a different account stays unresolved and the notice stays
          // neutral rather than borrowing the wrong name.
          return accountNotice(
            t,
            {
              named: 'impersonations.current.started',
              unnamed: 'impersonations.current.startedUnnamed',
            },
            answer.grant.target === typedAccount
              ? { name: identity.name, identifier: identity.identifier }
              : { name: undefined, identifier: undefined },
          )
        } catch (error) {
          // The gate takes a step-up refusal over; the attempt settles
          // without touching the slot, and a won challenge replays this
          // body as a new run. Either way this attempt did not land, so
          // the host's starting state is released.
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
