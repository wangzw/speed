/**
 * SessionsPage: the account-session administration page of the console.
 *
 * It lists the sessions of one account -- reached by naming the account's
 * identifier -- and revokes a single session. The read is
 * GET /auth/admin/accounts/:id/sessions and the revoke is
 * DELETE /auth/admin/sessions/:id; both are operations on the console
 * operations bundle, so the page builds no client and writes no query
 * key. The session's own answer is a snapshot: the assurance level is the
 * view's integer (0 none, 1 single, 2 multi), rendered as a label, and
 * the timestamps render in UTC through Intl. A revoked session still
 * lists. The page opens with ui-kit's PageHeader.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  DataTable,
  EmptyState,
  InlineSuccess,
  ListSkeleton,
  PageHeader,
  errorCodeOf,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { formatUtc } from './internal/datetime.js'
import { InlineError } from './internal/inline-error.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleAccountSession } from './operations.js'

/** The assurance level's label from its integer (0 none, 1 single, 2 multi). */
function assuranceKey(assurance: number): string {
  if (assurance >= 2) {
    return 'sessions.assurance.multi'
  }
  return assurance === 1 ? 'sessions.assurance.single' : 'sessions.assurance.none'
}

export function SessionsPage() {
  const { t, i18n } = useConsoleUiTranslation()
  const operations = useConsoleOperations()

  const [accountDraft, setAccountDraft] = useState('')
  const [account, setAccount] = useState('')
  const confirm = usePendingConfirm()
  // The confirmation a landed revoke fills into the page's success notice.
  const [success, setSuccess] = useState<string | null>(null)

  const sessions = operations.useAccountSessions({
    id: account === '' ? undefined : account,
  })
  const revokeSession = operations.useRevokeSession()

  const columns: DataTableColumn<ConsoleAccountSession>[] = [
    {
      id: 'id',
      header: t('sessions.column.id'),
      cellWrap: 'nowrap',
      cellFullValue: (session) => session.id,
      cell: (session) => <Typography variant="body2">{session.id}</Typography>,
    },
    {
      id: 'kind',
      header: t('sessions.column.kind'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (session) => session.kind,
    },
    {
      id: 'assurance',
      header: t('sessions.column.assurance'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (session) => t(assuranceKey(session.assurance)),
    },
    {
      id: 'created',
      header: t('sessions.column.created'),
      cellWrap: 'nowrap',
      cell: (session) => formatUtc(session.created_at, i18n.language),
    },
    {
      id: 'lastSeen',
      header: t('sessions.column.lastSeen'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (session) => formatUtc(session.last_seen_at, i18n.language),
    },
    {
      id: 'expires',
      header: t('sessions.column.expires'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (session) => formatUtc(session.expires_at, i18n.language),
    },
    {
      id: 'state',
      header: t('sessions.column.state'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (session) =>
        session.revoked ? t('sessions.revoked') : t('sessions.active'),
    },
    {
      id: 'actions',
      header: t('sessions.column.actions'),
      cellWrap: 'nowrap',
      cell: (session) => (
        <Button
          type="button"
          size="small"
          variant="outlined"
          color="error"
          aria-label={t('sessions.revokeAriaWithId', { id: session.id })}
          disabled={session.revoked || revokeSession.isPending}
          onClick={() =>
            confirm.ask({
              title: t('sessions.revokeTitle'),
              message: t('sessions.revokeMessage', { id: session.id }),
              confirmLabel: t('common.confirm'),
              run: () => {
                setSuccess(null)
                revokeSession.mutate(
                  { accountId: account, sessionId: session.id },
                  { onSuccess: () => setSuccess(t('sessions.revokedNotice')) },
                )
              },
            })
          }
        >
          {t('sessions.revoke')}
        </Button>
      ),
    },
  ]

  const rows = sessions.data
  const pending = sessions.isPending && rows === undefined

  return (
    <Box>
      <PageHeader
        title={t('sessions.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('sessions.title') },
        ]}
        actions={
          <Button
            type="button"
            variant="outlined"
            size="small"
            onClick={() => void sessions.refetch()}
          >
            {t('common.refresh')}
          </Button>
        }
        sx={{ mb: 2 }}
      />

      <Box
        component="section"
        sx={{
          mb: 3,
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'flex-end',
          columnGap: 2,
          rowGap: 1,
        }}
      >
        <TextField
          size="small"
          value={accountDraft}
          onChange={(event) => setAccountDraft(event.target.value)}
          label={t('sessions.accountLabel')}
        />
        <Button
          type="button"
          variant="contained"
          onClick={() => setAccount(accountDraft.trim())}
        >
          {t('sessions.load')}
        </Button>
      </Box>

      {account === '' ? (
        <EmptyState
          headingLevel="h2"
          title={t('sessions.needAccount.title')}
          description={t('sessions.needAccount.description')}
        />
      ) : (
        <ConsoleAsyncSection
          pending={pending}
          payload={rows}
          error={sessions.error}
          empty={rows !== undefined && rows.length === 0}
          headingLevel="h2"
          loading={
            <ListSkeleton
              label={t('sessions.loading')}
              rows={[
                <Skeleton key="a" variant="text" width="58%" />,
                <Skeleton key="b" variant="text" width="46%" />,
              ]}
            />
          }
          errorState={{
            title: t('sessions.error.title'),
            description: t('sessions.error.description'),
            retryLabel: t('sessions.retry'),
            onRetry: () => void sessions.refetch(),
          }}
          emptyState={{
            title: t('sessions.empty.title'),
            description: t('sessions.empty.description'),
          }}
        >
          {(settled) => (
            <DataTable
              rows={settled}
              columns={columns}
              rowKey={(session) => session.id}
            />
          )}
        </ConsoleAsyncSection>
      )}

      <Box sx={{ mt: 2 }}>
        <InlineError
          code={revokeSession.isError ? errorCodeOf(revokeSession.error) : null}
        />
        <InlineSuccess message={success} />
      </Box>

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}
