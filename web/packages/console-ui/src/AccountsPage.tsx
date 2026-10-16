/**
 * AccountsPage: the account-administration page of the console.
 *
 * It lists accounts (GET /auth/admin/accounts, narrowed by an identifier
 * prefix of one identifier kind) and opens one account's detail: its
 * read-only fields, its registered factors, its service secrets and its
 * sessions, with the write controls the platform's contract declares a
 * body-free route for -- delete the account, reset its factors, revoke
 * its sessions, revoke a secret, revoke a session, and remove a login
 * identifier named by hand.
 *
 * The list read takes the identifier prefix with its kind, because the
 * module's index is keyed by the two together (it refuses a prefix
 * without a kind): the toolbar carries a kind selector beside the prefix
 * box, and the applied filter is committed on submit so a keystroke never
 * fires a request.
 *
 * The list and the detail's four reads each stand behind their own
 * AsyncSection, so every one of them shows its loading, empty and
 * retryable-error states rather than settling a failed read into a
 * silently empty table.
 *
 * Several of the module's writes are held to a request body the
 * platform's OpenAPI fragment does not yet declare (create an account,
 * patch it, change its status, require MFA, reset its password, add an
 * identifier, create a secret), so those controls are not rendered; see
 * the package's design note on the missing fragment bodies. The account
 * view also carries no identifier list (the module exposes identifier
 * add/remove but no identifier read), so identifier removal names the
 * identifier by hand.
 *
 * Every destructive write is confirmed through ui-kit's danger
 * ConfirmDialog before it fires, the same two-step-free confirmation the
 * authorization page's role removal uses. Every read
 * and write is an operation on the console operations bundle, so the page
 * builds no client and writes no query key. The page opens with ui-kit's
 * PageHeader.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import MenuItem from '@mui/material/MenuItem'
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
import { formatUtc } from './internal/datetime.js'
import { InlineError } from './internal/inline-error.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type {
  ConsoleAccount,
  ConsoleAccountSecret,
  ConsoleAccountSession,
  ConsoleAccountFactor,
} from './operations.js'

/** The account status's label, guarding the closed set. */
const ACCOUNT_STATUSES = new Set<string>(['active', 'disabled', 'deleted'])

/** The identifier kinds the module's index is keyed by, in the order the
 * selector offers them. */
const IDENTIFIER_KINDS = ['username', 'email', 'phone'] as const
type IdentifierKind = (typeof IDENTIFIER_KINDS)[number]

function statusLabel(status: string, t: (key: string) => string): string {
  return ACCOUNT_STATUSES.has(status)
    ? t(`accounts.status.${status}`)
    : t('accounts.status.other')
}

export function AccountsPage() {
  const { t, i18n } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const confirm = usePendingConfirm()

  const [identifierDraft, setIdentifierDraft] = useState('')
  const [identifier, setIdentifier] = useState('')
  const [kindDraft, setKindDraft] = useState<IdentifierKind>('username')
  const [identifierKind, setIdentifierKind] = useState<IdentifierKind>('username')
  const [selectedId, setSelectedId] = useState('')
  const [identifierToRemove, setIdentifierToRemove] = useState('')

  const accounts = operations.useAccounts(
    identifier === ''
      ? undefined
      : { identifier_prefix: identifier, identifier_kind: identifierKind },
  )
  const account = operations.useAccount({
    id: selectedId === '' ? undefined : selectedId,
  })
  const factors = operations.useAccountFactors({
    id: selectedId === '' ? undefined : selectedId,
  })
  const secrets = operations.useAccountSecrets({
    id: selectedId === '' ? undefined : selectedId,
  })
  const sessions = operations.useAccountSessions({
    id: selectedId === '' ? undefined : selectedId,
  })

  const deleteAccount = operations.useDeleteAccount()
  const resetMFA = operations.useResetMFA()
  const revokeSessions = operations.useRevokeAccountSessions()
  const revokeSecret = operations.useRevokeSecret()
  const revokeSession = operations.useRevokeSession()
  const removeIdentifier = operations.useRemoveIdentifier()

  const columns: DataTableColumn<ConsoleAccount>[] = [
    {
      id: 'id',
      header: t('accounts.column.id'),
      cell: (row) => (
        <Typography variant="body2" sx={{ wordBreak: 'break-all' }}>
          {row.id}
        </Typography>
      ),
    },
    {
      id: 'kind',
      header: t('accounts.column.kind'),
      priority: 'medium',
      cell: (row) => row.kind,
    },
    {
      id: 'displayName',
      header: t('accounts.column.displayName'),
      cell: (row) => row.display_name,
    },
    {
      id: 'status',
      header: t('accounts.column.status'),
      cell: (row) => statusLabel(row.status, t),
    },
    {
      id: 'mfa',
      header: t('accounts.column.mfa'),
      priority: 'low',
      cell: (row) =>
        row.mfa_required ? t('accounts.mfaYes') : t('accounts.mfaNo'),
    },
    {
      id: 'actions',
      header: t('accounts.column.actions'),
      cell: (row) => (
        <Box sx={{ display: 'flex', flexWrap: 'wrap', columnGap: 1, rowGap: 1 }}>
          <Button
            type="button"
            size="small"
            variant="outlined"
            onClick={() => setSelectedId(row.id)}
          >
            {t('accounts.manage')}
          </Button>
          <Button
            type="button"
            size="small"
            variant="outlined"
            color="error"
            disabled={deleteAccount.isPending}
            onClick={() =>
              confirm.ask({
                title: t('accounts.deleteTitle'),
                message: t('accounts.deleteMessage', { id: row.id }),
                confirmLabel: t('common.confirm'),
                run: () =>
                  deleteAccount.mutate(
                    { id: row.id },
                    {
                      onSuccess: () => {
                        if (selectedId === row.id) {
                          setSelectedId('')
                        }
                      },
                    },
                  ),
              })
            }
          >
            {t('accounts.delete')}
          </Button>
        </Box>
      ),
    },
  ]

  const secretColumns: DataTableColumn<ConsoleAccountSecret>[] = [
    {
      id: 'label',
      header: t('accounts.secrets.column.label'),
      cell: (row) => row.label,
    },
    {
      id: 'expires',
      header: t('accounts.secrets.column.expires'),
      priority: 'low',
      cell: (row) => formatUtc(row.expires_at, i18n.language),
    },
    {
      id: 'state',
      header: t('accounts.secrets.column.state'),
      cell: (row) =>
        row.revoked ? t('accounts.secrets.revoked') : t('accounts.secrets.active'),
    },
    {
      id: 'actions',
      header: t('accounts.secrets.column.actions'),
      cell: (row) => (
        <Button
          type="button"
          size="small"
          variant="outlined"
          color="error"
          disabled={row.revoked || revokeSecret.isPending}
          onClick={() =>
            confirm.ask({
              title: t('accounts.secrets.revokeTitle'),
              message: t('accounts.secrets.revokeMessage', { label: row.label }),
              confirmLabel: t('common.confirm'),
              run: () => revokeSecret.mutate({ id: selectedId, secret: row.id }),
            })
          }
        >
          {t('accounts.secrets.revoke')}
        </Button>
      ),
    },
  ]

  const sessionColumns: DataTableColumn<ConsoleAccountSession>[] = [
    {
      id: 'id',
      header: t('accounts.sessions.column.id'),
      cell: (row) => (
        <Typography variant="body2" sx={{ wordBreak: 'break-all' }}>
          {row.id}
        </Typography>
      ),
    },
    {
      id: 'created',
      header: t('accounts.sessions.column.created'),
      cell: (row) => formatUtc(row.created_at, i18n.language),
    },
    {
      id: 'state',
      header: t('accounts.sessions.column.state'),
      priority: 'medium',
      cell: (row) =>
        row.revoked ? t('accounts.sessions.revoked') : t('accounts.sessions.active'),
    },
    {
      id: 'actions',
      header: t('accounts.sessions.column.actions'),
      cell: (row) => (
        <Button
          type="button"
          size="small"
          variant="outlined"
          color="error"
          disabled={row.revoked || revokeSession.isPending}
          onClick={() =>
            confirm.ask({
              title: t('accounts.sessions.revokeTitle'),
              message: t('accounts.sessions.revokeMessage', { id: row.id }),
              confirmLabel: t('common.confirm'),
              run: () =>
                revokeSession.mutate({
                  accountId: selectedId,
                  sessionId: row.id,
                }),
            })
          }
        >
          {t('accounts.sessions.revoke')}
        </Button>
      ),
    },
  ]

  const factorColumns: DataTableColumn<ConsoleAccountFactor>[] = [
    {
      id: 'kind',
      header: t('accounts.factors.column.kind'),
      cell: (row) => row.kind,
    },
    {
      id: 'label',
      header: t('accounts.factors.column.label'),
      cell: (row) => row.label,
    },
    {
      id: 'status',
      header: t('accounts.factors.column.status'),
      cell: (row) => row.status,
    },
  ]

  const rows = accounts.data?.accounts
  const pending = accounts.isPending && rows === undefined

  const writeError = firstErrorCode([
    deleteAccount.error,
    resetMFA.error,
    revokeSessions.error,
    revokeSecret.error,
    revokeSession.error,
    removeIdentifier.error,
  ])

  return (
    <Box>
      <PageHeader
        title={t('accounts.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('accounts.title') },
        ]}
        actions={
          <Button
            type="button"
            variant="outlined"
            size="small"
            onClick={() => void accounts.refetch()}
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
          select
          size="small"
          value={kindDraft}
          onChange={(event) => setKindDraft(event.target.value as IdentifierKind)}
          label={t('accounts.identifierKindLabel')}
          sx={{ minWidth: 140 }}
        >
          {IDENTIFIER_KINDS.map((kind) => (
            <MenuItem key={kind} value={kind}>
              {t(`accounts.identifierKind.${kind}`)}
            </MenuItem>
          ))}
        </TextField>
        <TextField
          size="small"
          value={identifierDraft}
          onChange={(event) => setIdentifierDraft(event.target.value)}
          label={t('accounts.identifierLabel')}
        />
        <Button
          type="button"
          variant="contained"
          onClick={() => {
            setIdentifier(identifierDraft.trim())
            setIdentifierKind(kindDraft)
          }}
        >
          {t('accounts.search')}
        </Button>
      </Box>

      <AsyncSection
        pending={pending}
        payload={rows}
        empty={rows !== undefined && rows.length === 0}
        headingLevel="h2"
        loading={
          <ListSkeleton
            label={t('accounts.loading')}
            rows={[
              <Skeleton key="a" variant="text" width="58%" />,
              <Skeleton key="b" variant="text" width="46%" />,
            ]}
          />
        }
        errorState={{
          title: t('accounts.error.title'),
          description: t('accounts.error.description'),
          retryLabel: t('accounts.retry'),
          onRetry: () => void accounts.refetch(),
        }}
        emptyState={{
          title: t('accounts.empty.title'),
          description: t('accounts.empty.description'),
        }}
      >
        {(settled) => (
          <DataTable rows={settled} columns={columns} rowKey={(row) => row.id} />
        )}
      </AsyncSection>

      {selectedId !== '' && (
        <Box component="section" sx={{ mt: 3 }}>
          <Typography
            component="h2"
            variant="h2"
            sx={{ fontSize: '1.25rem', mb: 1 }}
          >
            {t('accounts.detail.title', { id: selectedId })}
          </Typography>

          <AsyncSection
            pending={account.isPending && account.data === undefined}
            payload={account.data}
            empty={false}
            headingLevel="h3"
            loading={
              <ListSkeleton
                label={t('accounts.detail.loading')}
                rows={[
                  <Skeleton key="a" variant="text" width="42%" />,
                  <Skeleton key="b" variant="text" width="34%" />,
                ]}
              />
            }
            errorState={{
              title: t('accounts.detail.error.title'),
              description: t('accounts.detail.error.description'),
              retryLabel: t('accounts.detail.retry'),
              onRetry: () => void account.refetch(),
            }}
          >
            {(settled) => (
              <Box sx={{ mb: 2 }}>
                <Typography variant="body2">
                  {t('accounts.detail.displayNameLine', {
                    value: settled.display_name,
                  })}
                </Typography>
                <Typography variant="body2">
                  {t('accounts.detail.statusLine', {
                    value: statusLabel(settled.status, t),
                  })}
                </Typography>
                <Typography variant="body2">
                  {t('accounts.detail.kindLine', { value: settled.kind })}
                </Typography>
              </Box>
            )}
          </AsyncSection>

          <Box sx={{ display: 'flex', flexWrap: 'wrap', columnGap: 1, rowGap: 1, mb: 2 }}>
            <Button
              type="button"
              variant="outlined"
              disabled={resetMFA.isPending}
              onClick={() =>
                confirm.ask({
                  title: t('accounts.resetMFATitle'),
                  message: t('accounts.resetMFAMessage'),
                  confirmLabel: t('common.confirm'),
                  run: () => resetMFA.mutate({ id: selectedId }),
                })
              }
            >
              {t('accounts.resetMFA')}
            </Button>
            <Button
              type="button"
              variant="outlined"
              color="error"
              disabled={revokeSessions.isPending}
              onClick={() =>
                confirm.ask({
                  title: t('accounts.revokeSessionsTitle'),
                  message: t('accounts.revokeSessionsMessage'),
                  confirmLabel: t('common.confirm'),
                  run: () => revokeSessions.mutate({ id: selectedId }),
                })
              }
            >
              {t('accounts.revokeSessions')}
            </Button>
          </Box>

          <Typography variant="subtitle1" sx={{ mb: 1 }}>
            {t('accounts.factors.title')}
          </Typography>
          <AsyncSection
            pending={factors.isPending && factors.data === undefined}
            payload={factors.data}
            empty={factors.data !== undefined && factors.data.length === 0}
            headingLevel="h3"
            loading={
              <ListSkeleton
                label={t('accounts.factors.loading')}
                rows={[<Skeleton key="a" variant="text" width="48%" />]}
              />
            }
            errorState={{
              title: t('accounts.factors.error.title'),
              description: t('accounts.factors.error.description'),
              retryLabel: t('accounts.factors.retry'),
              onRetry: () => void factors.refetch(),
            }}
            emptyState={{
              title: t('accounts.factors.empty.title'),
              description: t('accounts.factors.empty.description'),
            }}
          >
            {(settled) => (
              <DataTable
                rows={settled}
                columns={factorColumns}
                rowKey={(row) => `${row.kind}:${row.label}`}
              />
            )}
          </AsyncSection>

          <Typography variant="subtitle1" sx={{ mt: 2, mb: 1 }}>
            {t('accounts.secrets.title')}
          </Typography>
          <AsyncSection
            pending={secrets.isPending && secrets.data === undefined}
            payload={secrets.data}
            empty={secrets.data !== undefined && secrets.data.length === 0}
            headingLevel="h3"
            loading={
              <ListSkeleton
                label={t('accounts.secrets.loading')}
                rows={[<Skeleton key="a" variant="text" width="48%" />]}
              />
            }
            errorState={{
              title: t('accounts.secrets.error.title'),
              description: t('accounts.secrets.error.description'),
              retryLabel: t('accounts.secrets.retry'),
              onRetry: () => void secrets.refetch(),
            }}
            emptyState={{
              title: t('accounts.secrets.empty.title'),
              description: t('accounts.secrets.empty.description'),
            }}
          >
            {(settled) => (
              <DataTable
                rows={settled}
                columns={secretColumns}
                rowKey={(row) => row.id}
              />
            )}
          </AsyncSection>

          <Typography variant="subtitle1" sx={{ mt: 2, mb: 1 }}>
            {t('accounts.sessions.title')}
          </Typography>
          <AsyncSection
            pending={sessions.isPending && sessions.data === undefined}
            payload={sessions.data}
            empty={sessions.data !== undefined && sessions.data.length === 0}
            headingLevel="h3"
            loading={
              <ListSkeleton
                label={t('accounts.sessions.loading')}
                rows={[<Skeleton key="a" variant="text" width="48%" />]}
              />
            }
            errorState={{
              title: t('accounts.sessions.error.title'),
              description: t('accounts.sessions.error.description'),
              retryLabel: t('accounts.sessions.retry'),
              onRetry: () => void sessions.refetch(),
            }}
            emptyState={{
              title: t('accounts.sessions.empty.title'),
              description: t('accounts.sessions.empty.description'),
            }}
          >
            {(settled) => (
              <DataTable
                rows={settled}
                columns={sessionColumns}
                rowKey={(row) => row.id}
              />
            )}
          </AsyncSection>

          <Box
            sx={{
              mt: 2,
              display: 'flex',
              flexWrap: 'wrap',
              alignItems: 'flex-end',
              columnGap: 2,
              rowGap: 1,
            }}
          >
            <TextField
              size="small"
              value={identifierToRemove}
              onChange={(event) => setIdentifierToRemove(event.target.value)}
              label={t('accounts.removeIdentifier.label')}
            />
            <Button
              type="button"
              variant="outlined"
              color="error"
              disabled={removeIdentifier.isPending || identifierToRemove.trim() === ''}
              onClick={() =>
                confirm.ask({
                  title: t('accounts.removeIdentifier.confirmTitle'),
                  message: t('accounts.removeIdentifier.confirmMessage', {
                    identifier: identifierToRemove,
                  }),
                  confirmLabel: t('common.confirm'),
                  run: () =>
                    removeIdentifier.mutate(
                      { id: selectedId, identifier: identifierToRemove },
                      { onSuccess: () => setIdentifierToRemove('') },
                    ),
                })
              }
            >
              {t('accounts.removeIdentifier.submit')}
            </Button>
          </Box>
        </Box>
      )}

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
