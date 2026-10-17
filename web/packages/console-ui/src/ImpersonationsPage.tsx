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
 *    own {account, tenant?, reason?, ttl_seconds?} shape -- and an end
 *    action (DELETE) stops it. These three are the caller-session
 *    operations the generated @speed/api-sdk surface ships
 *    (useAuthnImpersonationStatus / Start / End), so the page calls them
 *    directly and invalidates the status read through the generated key
 *    factory; the start control is offered only while no impersonation is
 *    in force (the module refuses a second start on one session with 409);
 *
 *  - the tenant's impersonation records, over /auth/admin/impersonations:
 *    the record list (GET) and one record's termination (DELETE). The read
 *    names its tenant as a required query parameter, so the page opens that
 *    half with a tenant selector fed by the platform roster, exactly as the
 *    authorization and SSO pages do with theirs, and keeps the read disabled
 *    until a tenant is chosen. The tenant is an argument of the request, not
 *    the session's current tenant. These two are operations on the console
 *    operations bundle, so that half builds no client and writes no query
 *    key.
 *
 * A record is in force while its `ended_at` is the zero instant (the
 * sentinel the view carries); the state column says so and the terminate
 * control is offered only then. The timestamps render in UTC through
 * Intl. The page opens with ui-kit's PageHeader.
 */

import { useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnImpersonationStatusQueryKey,
  useAuthnImpersonationEnd,
  useAuthnImpersonationStart,
  useAuthnImpersonationStatus,
} from '@speed/api-sdk'
import {
  DataTable,
  EmptyState,
  ListSkeleton,
  PageHeader,
  errorCodeOf,
  touchTargetSx,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { formatUtc, isZeroInstant } from './internal/datetime.js'
import { InlineError } from './internal/inline-error.js'
import { useConsoleStepUp } from './internal/step-up-gate.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { TenantScope } from './internal/tenant-scope.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleImpersonation } from './operations.js'

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

  const [tenant, setTenant] = useState('')

  const impersonations = operations.useImpersonations({ tenant })
  const revokeImpersonation = operations.useRevokeImpersonation()

  // The caller's own impersonation session, over /auth/impersonation. These
  // are the generated caller-session operations (not the administration
  // bundle): the page reads the status, ends an in-force impersonation, and
  // offers the start form only while none is in force.
  const status = useAuthnImpersonationStatus()
  const endImpersonation = useAuthnImpersonationEnd({
    mutation: {
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnImpersonationStatusQueryKey(),
        })
      },
    },
  })

  const grant = status.data?.grant
  const notImpersonating = isNotImpersonating(status.error)

  const columns: DataTableColumn<ConsoleImpersonation>[] = [
    {
      id: 'actor',
      header: t('impersonations.column.actor'),
      cellWrap: 'nowrap',
      cell: (record) => record.actor,
    },
    {
      id: 'target',
      header: t('impersonations.column.target'),
      cellWrap: 'nowrap',
      cell: (record) => record.target,
    },
    {
      id: 'tenant',
      header: t('impersonations.column.tenant'),
      priority: 'medium',
      cellWrap: 'nowrap',
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
          disabled={!isZeroInstant(record.ended_at) || revokeImpersonation.isPending}
          onClick={() => revokeImpersonation.mutate({ id: record.id })}
          sx={touchTargetSx}
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
              void impersonations.refetch()
            }}
            sx={touchTargetSx}
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
        {status.isPending ? (
          <Typography role="status" variant="body2" color="text.secondary">
            {t('impersonations.current.loading')}
          </Typography>
        ) : grant !== undefined ? (
          <Alert severity="warning" variant="outlined">
            <Stack sx={{ gap: 0.5, mb: 1 }}>
              <Typography variant="body2">
                {t('impersonations.current.target', { target: grant.target })}
              </Typography>
              <Typography variant="body2">
                {t('impersonations.current.tenant', { tenant: grant.tenant })}
              </Typography>
              <Typography variant="body2">
                {t('impersonations.current.ends', {
                  ends: formatUtc(grant.ends_at, i18n.language),
                })}
              </Typography>
            </Stack>
            <Button
              type="button"
              size="small"
              variant="outlined"
              color="error"
              disabled={endImpersonation.isPending}
              onClick={() => endImpersonation.mutate()}
              sx={touchTargetSx}
            >
              {t('impersonations.current.end')}
            </Button>
          </Alert>
        ) : notImpersonating ? (
          <Typography variant="body2" color="text.secondary">
            {t('impersonations.current.none')}
          </Typography>
        ) : status.isError ? (
          <InlineError code={errorCodeOf(status.error)} />
        ) : null}
        <Box sx={{ mt: 1 }}>
          <InlineError
            code={
              endImpersonation.isError
                ? errorCodeOf(endImpersonation.error)
                : null
            }
          />
        </Box>
      </Box>

      {grant === undefined && (
        <Box component="section" sx={{ mb: 3 }}>
          <Typography
            component="h2"
            variant="h2"
            sx={{ fontSize: '1.25rem', mb: 1 }}
          >
            {t('impersonations.start.title')}
          </Typography>
          <StartImpersonationForm />
        </Box>
      )}

      <Box component="section" sx={{ mb: 3 }}>
        <TenantScope value={tenant} onSelect={setTenant} />
      </Box>

      {tenant === '' ? (
        <EmptyState
          headingLevel="h2"
          title={t('impersonations.needTenant.title')}
          description={t('impersonations.needTenant.description')}
        />
      ) : (
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
      )}

      <Box sx={{ mt: 2 }}>
        <InlineError
          code={
            revokeImpersonation.isError
              ? errorCodeOf(revokeImpersonation.error)
              : null
          }
        />
      </Box>
    </Box>
  )
}

/**
 * StartImpersonationForm: the control that begins an impersonation on the
 * operator's own session (POST /auth/impersonation). It renders the
 * endpoint's request-body fields -- a required target account, an optional
 * tenant and reason, and an optional TTL in whole seconds -- and calls the
 * generated start mutation. A landed start clears the form and invalidates
 * the status read through the generated key factory, so the page's status
 * section converges on the record the server just wrote; a refused start
 * keeps the values so the operator can correct them.
 */
function StartImpersonationForm() {
  const { t } = useConsoleUiTranslation()
  const queryClient = useQueryClient()
  const stepUp = useConsoleStepUp()
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
  const [tenant, setTenant] = useState('')
  const [reason, setReason] = useState('')
  const [ttl, setTtl] = useState('')

  const ttlSeconds = ttl.trim() === '' ? undefined : Number(ttl)

  const submit = (): void => {
    startImpersonation.mutate(
      {
        data: {
          account,
          ...(tenant.trim() === '' ? {} : { tenant }),
          ...(reason.trim() === '' ? {} : { reason }),
          ...(ttlSeconds === undefined || Number.isNaN(ttlSeconds)
            ? {}
            : { ttl_seconds: ttlSeconds }),
        },
      },
      {
        onSuccess: () => {
          setAccount('')
          setTenant('')
          setReason('')
          setTtl('')
        },
        onError: (error) => {
          // A refusal the server answers while the session is still
          // established is a strength shortfall: arm the step-up dialog
          // with a re-fire of the same start and clear the mutation's own
          // error so the dialog opens alone.
          if (stepUp.intercept(error, submit)) {
            startImpersonation.reset()
          }
        },
      },
    )
  }

  return (
    <Box>
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
          value={tenant}
          onChange={(event) => setTenant(event.target.value)}
          label={t('impersonations.start.tenantLabel')}
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
          disabled={startImpersonation.isPending || account.trim() === ''}
          onClick={submit}
        >
          {t('impersonations.start.submit')}
        </Button>
      </Box>
      <Box sx={{ mt: 1 }}>
        <InlineError
          code={
            startImpersonation.isError
              ? errorCodeOf(startImpersonation.error)
              : null
          }
        />
      </Box>

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
