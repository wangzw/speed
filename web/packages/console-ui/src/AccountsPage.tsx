/**
 * AccountsPage: the account-administration page of the console.
 *
 * It lists accounts (GET /auth/admin/accounts, narrowed by an identifier
 * prefix of one identifier kind), creates an account, and opens one
 * account's detail: its read-only fields, its registered factors, its
 * service secrets and its sessions, with the write controls the platform
 * declares a request body for -- create an account; change its status,
 * MFA requirement, display name, attributes and protected flag; reset its
 * password; add a login name or email; create a service secret; and the
 * body-free writes delete the account, reset its factors, revoke its
 * sessions, revoke a secret, revoke a session and remove a login
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
 * Each write request body is the shape the module's OpenAPI fragment
 * declares for its endpoint, which the handler decodes; the account view
 * carries no identifier list (the module exposes identifier add/remove
 * but no identifier read), so identifier removal and add name the
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
import Checkbox from '@mui/material/Checkbox'
import FormControlLabel from '@mui/material/FormControlLabel'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  DataTable,
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

      <AccountCreateForm />

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

      <ConsoleAsyncSection
        pending={pending}
        payload={rows}
        error={accounts.error}
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
      </ConsoleAsyncSection>

      {selectedId !== '' && (
        <Box component="section" sx={{ mt: 3 }}>
          <Typography
            component="h2"
            variant="h2"
            sx={{ fontSize: '1.25rem', mb: 1 }}
          >
            {t('accounts.detail.title', { id: selectedId })}
          </Typography>

          <ConsoleAsyncSection
            pending={account.isPending && account.data === undefined}
            payload={account.data}
            error={account.error}
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
          </ConsoleAsyncSection>

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

          {account.data !== undefined && (
            <AccountDetailWrites key={selectedId} account={account.data} />
          )}

          <Typography component="h3" variant="subtitle1" sx={{ mb: 1 }}>
            {t('accounts.factors.title')}
          </Typography>
          <ConsoleAsyncSection
            pending={factors.isPending && factors.data === undefined}
            payload={factors.data}
            error={factors.error}
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
          </ConsoleAsyncSection>

          <Typography component="h3" variant="subtitle1" sx={{ mt: 2, mb: 1 }}>
            {t('accounts.secrets.title')}
          </Typography>
          <ConsoleAsyncSection
            pending={secrets.isPending && secrets.data === undefined}
            payload={secrets.data}
            error={secrets.error}
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
          </ConsoleAsyncSection>

          <Typography component="h3" variant="subtitle1" sx={{ mt: 2, mb: 1 }}>
            {t('accounts.sessions.title')}
          </Typography>
          <ConsoleAsyncSection
            pending={sessions.isPending && sessions.data === undefined}
            payload={sessions.data}
            error={sessions.error}
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
          </ConsoleAsyncSection>

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

/** The ways an administrator may state an identifier's verification. */
type VerifiedVia = '' | 'admin' | 'import-unverified'

/**
 * Parses an optional attribute map the operator typed as a JSON object with
 * string values. Returns null when the text is not such an object, so a form
 * can mark the field and keep the submit disabled.
 */
function parseStringMap(text: string): Record<string, string> | null {
  try {
    const parsed: unknown = JSON.parse(text)
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return null
    }
    const out: Record<string, string> = {}
    for (const [key, value] of Object.entries(parsed)) {
      if (typeof value !== 'string') {
        return null
      }
      out[key] = value
    }
    return out
  } catch {
    return null
  }
}

/**
 * AccountCreateForm: the account-creation control (POST /auth/admin/accounts).
 * It names the fields the handler decodes into its create request; each is
 * optional but the kind, and a field left blank is not sent. The attribute
 * set is typed as a JSON object and marks the form when it does not parse.
 * A landed create clears the form; a refused one keeps it to correct.
 */
function AccountCreateForm() {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const createAccount = operations.useCreateAccount()

  const [kind, setKind] = useState<'user' | 'service'>('user')
  const [displayName, setDisplayName] = useState('')
  const [username, setUsername] = useState('')
  const [email, setEmail] = useState('')
  const [emailVia, setEmailVia] = useState<VerifiedVia>('')
  const [phone, setPhone] = useState('')
  const [phoneVia, setPhoneVia] = useState<VerifiedVia>('')
  const [password, setPassword] = useState('')
  const [mfaRequired, setMfaRequired] = useState(false)
  const [attributes, setAttributes] = useState('')

  const attributesInvalid =
    attributes.trim() !== '' && parseStringMap(attributes) === null

  const viaOptions = (
    <>
      <MenuItem value="">{t('accounts.create.via.none')}</MenuItem>
      <MenuItem value="admin">{t('accounts.create.via.admin')}</MenuItem>
      <MenuItem value="import-unverified">
        {t('accounts.create.via.importUnverified')}
      </MenuItem>
    </>
  )

  return (
    <Box component="section" sx={{ mb: 3 }}>
      <Typography variant="h2" sx={{ fontSize: '1.25rem', mb: 1 }}>
        {t('accounts.create.title')}
      </Typography>
      <Box
        sx={{
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'flex-start',
          columnGap: 2,
          rowGap: 1,
        }}
      >
        <TextField
          select
          size="small"
          value={kind}
          onChange={(event) => setKind(event.target.value as 'user' | 'service')}
          label={t('accounts.create.kindLabel')}
          sx={{ minWidth: 140 }}
        >
          <MenuItem value="user">{t('accounts.create.kind.user')}</MenuItem>
          <MenuItem value="service">{t('accounts.create.kind.service')}</MenuItem>
        </TextField>
        <TextField
          size="small"
          value={displayName}
          onChange={(event) => setDisplayName(event.target.value)}
          label={t('accounts.create.displayNameLabel')}
        />
        <TextField
          size="small"
          value={username}
          onChange={(event) => setUsername(event.target.value)}
          label={t('accounts.create.usernameLabel')}
        />
        <TextField
          size="small"
          value={email}
          onChange={(event) => setEmail(event.target.value)}
          label={t('accounts.create.emailLabel')}
        />
        <TextField
          select
          size="small"
          value={emailVia}
          onChange={(event) => setEmailVia(event.target.value as VerifiedVia)}
          label={t('accounts.create.emailViaLabel')}
          sx={{ minWidth: 180 }}
        >
          {viaOptions}
        </TextField>
        <TextField
          size="small"
          value={phone}
          onChange={(event) => setPhone(event.target.value)}
          label={t('accounts.create.phoneLabel')}
        />
        <TextField
          select
          size="small"
          value={phoneVia}
          onChange={(event) => setPhoneVia(event.target.value as VerifiedVia)}
          label={t('accounts.create.phoneViaLabel')}
          sx={{ minWidth: 180 }}
        >
          {viaOptions}
        </TextField>
        <TextField
          size="small"
          type="password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          label={t('accounts.create.passwordLabel')}
        />
        <FormControlLabel
          control={
            <Checkbox
              checked={mfaRequired}
              onChange={(event) => setMfaRequired(event.target.checked)}
            />
          }
          label={t('accounts.create.mfaLabel')}
        />
        <TextField
          size="small"
          value={attributes}
          onChange={(event) => setAttributes(event.target.value)}
          label={t('accounts.create.attributesLabel')}
          error={attributesInvalid}
          helperText={t('accounts.create.attributesHelper')}
          multiline
          minRows={1}
          sx={{ minWidth: 260 }}
        />
        <Button
          type="button"
          variant="contained"
          disabled={createAccount.isPending || attributesInvalid}
          onClick={() => {
            const parsed = attributes.trim() === '' ? null : parseStringMap(attributes)
            createAccount.mutate(
              {
                kind,
                ...(displayName === '' ? {} : { display_name: displayName }),
                ...(username === '' ? {} : { username }),
                ...(email === '' ? {} : { email }),
                ...(emailVia === '' ? {} : { email_verified_via: emailVia }),
                ...(phone === '' ? {} : { phone }),
                ...(phoneVia === '' ? {} : { phone_verified_via: phoneVia }),
                ...(password === '' ? {} : { password }),
                ...(parsed === null ? {} : { attributes: parsed }),
                ...(mfaRequired ? { mfa_required: true } : {}),
              },
              {
                onSuccess: () => {
                  setDisplayName('')
                  setUsername('')
                  setEmail('')
                  setEmailVia('')
                  setPhone('')
                  setPhoneVia('')
                  setPassword('')
                  setMfaRequired(false)
                  setAttributes('')
                },
              },
            )
          }}
        >
          {t('accounts.create.submit')}
        </Button>
      </Box>
      <Box sx={{ mt: 1 }}>
        <InlineError
          code={createAccount.isError ? errorCodeOf(createAccount.error) : null}
        />
      </Box>
    </Box>
  )
}

/**
 * AccountDetailWrites: the account-level write controls the platform contract
 * now declares a body for -- the status change, the MFA requirement, the patch
 * (display name / attributes / protected), the password reset, the identifier
 * add and the service-secret creation. Each renders as one field group beside
 * its own failure banner; the page's delete, factor-reset, session-revoke and
 * identifier-remove controls stay with the detail above. The account is passed
 * settled, and the component is keyed by the account id at its call site so a
 * newly selected account starts from its own values.
 */
function AccountDetailWrites({ account }: { readonly account: ConsoleAccount }) {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const patchAccount = operations.usePatchAccount()
  const setStatus = operations.useSetAccountStatus()
  const setMfaRequired = operations.useSetMFARequired()
  const resetPassword = operations.useResetPassword()
  const addIdentifier = operations.useAddIdentifier()
  const createSecret = operations.useCreateSecret()

  const id = account.id
  const [displayName, setDisplayName] = useState('')
  const [attributes, setAttributes] = useState('')
  const [protectedDraft, setProtectedDraft] = useState<'unchanged' | 'true' | 'false'>(
    'unchanged',
  )
  const [statusDraft, setStatusDraft] = useState<'active' | 'disabled'>(
    account.status === 'disabled' ? 'active' : 'disabled',
  )
  const [mfaDraft, setMfaDraft] = useState<'true' | 'false'>(
    account.mfa_required ? 'false' : 'true',
  )
  const [tempPassword, setTempPassword] = useState('')
  const [identifierKind, setIdentifierKind] = useState<'username' | 'email'>('username')
  const [identifierValue, setIdentifierValue] = useState('')
  const [secretLabel, setSecretLabel] = useState('')
  const [secretExpires, setSecretExpires] = useState('')
  const [createdSecret, setCreatedSecret] = useState<string | null>(null)

  const attributesInvalid =
    attributes.trim() !== '' && parseStringMap(attributes) === null

  const rowSx = {
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'flex-end',
    columnGap: 2,
    rowGap: 1,
    mb: 2,
  } as const

  return (
    <>
      <Typography component="h3" variant="subtitle1" sx={{ mb: 1 }}>
        {t('accounts.writes.title')}
      </Typography>

      <Box sx={rowSx}>
        <TextField
          select
          size="small"
          value={statusDraft}
          onChange={(event) =>
            setStatusDraft(event.target.value as 'active' | 'disabled')
          }
          label={t('accounts.status.label')}
          sx={{ minWidth: 160 }}
        >
          <MenuItem value="active">{t('accounts.status.active')}</MenuItem>
          <MenuItem value="disabled">{t('accounts.status.disabled')}</MenuItem>
        </TextField>
        <Button
          type="button"
          variant="outlined"
          disabled={setStatus.isPending}
          onClick={() => setStatus.mutate({ id, status: statusDraft })}
        >
          {t('accounts.status.submit')}
        </Button>
        <InlineError
          code={setStatus.isError ? errorCodeOf(setStatus.error) : null}
        />
      </Box>

      <Box sx={rowSx}>
        <TextField
          select
          size="small"
          value={mfaDraft}
          onChange={(event) => setMfaDraft(event.target.value as 'true' | 'false')}
          label={t('accounts.mfa.label')}
          sx={{ minWidth: 160 }}
        >
          <MenuItem value="true">{t('accounts.mfa.required')}</MenuItem>
          <MenuItem value="false">{t('accounts.mfa.notRequired')}</MenuItem>
        </TextField>
        <Button
          type="button"
          variant="outlined"
          disabled={setMfaRequired.isPending}
          onClick={() => setMfaRequired.mutate({ id, required: mfaDraft === 'true' })}
        >
          {t('accounts.mfa.submit')}
        </Button>
        <InlineError
          code={setMfaRequired.isError ? errorCodeOf(setMfaRequired.error) : null}
        />
      </Box>

      <Box sx={rowSx}>
        <TextField
          size="small"
          value={displayName}
          onChange={(event) => setDisplayName(event.target.value)}
          label={t('accounts.patch.displayNameLabel')}
        />
        <TextField
          size="small"
          value={attributes}
          onChange={(event) => setAttributes(event.target.value)}
          label={t('accounts.patch.attributesLabel')}
          error={attributesInvalid}
          helperText={t('accounts.patch.attributesHelper')}
          multiline
          minRows={1}
          sx={{ minWidth: 240 }}
        />
        <TextField
          select
          size="small"
          value={protectedDraft}
          onChange={(event) =>
            setProtectedDraft(event.target.value as 'unchanged' | 'true' | 'false')
          }
          label={t('accounts.patch.protectedLabel')}
          sx={{ minWidth: 180 }}
        >
          <MenuItem value="unchanged">
            {t('accounts.patch.protected.unchanged')}
          </MenuItem>
          <MenuItem value="true">{t('accounts.patch.protected.true')}</MenuItem>
          <MenuItem value="false">{t('accounts.patch.protected.false')}</MenuItem>
        </TextField>
        <Button
          type="button"
          variant="outlined"
          disabled={patchAccount.isPending || attributesInvalid}
          onClick={() => {
            const parsed = attributes.trim() === '' ? null : parseStringMap(attributes)
            patchAccount.mutate(
              {
                id,
                ...(displayName === '' ? {} : { display_name: displayName }),
                ...(parsed === null ? {} : { attributes: parsed }),
                ...(protectedDraft === 'unchanged'
                  ? {}
                  : { protected: protectedDraft === 'true' }),
              },
              {
                onSuccess: () => {
                  setDisplayName('')
                  setAttributes('')
                  setProtectedDraft('unchanged')
                },
              },
            )
          }}
        >
          {t('accounts.patch.submit')}
        </Button>
        <InlineError
          code={patchAccount.isError ? errorCodeOf(patchAccount.error) : null}
        />
      </Box>

      <Box sx={rowSx}>
        <TextField
          size="small"
          type="password"
          value={tempPassword}
          onChange={(event) => setTempPassword(event.target.value)}
          label={t('accounts.password.label')}
        />
        <Button
          type="button"
          variant="outlined"
          disabled={resetPassword.isPending || tempPassword === ''}
          onClick={() =>
            resetPassword.mutate(
              { id, password: tempPassword },
              { onSuccess: () => setTempPassword('') },
            )
          }
        >
          {t('accounts.password.submit')}
        </Button>
        <InlineError
          code={resetPassword.isError ? errorCodeOf(resetPassword.error) : null}
        />
      </Box>

      <Box sx={rowSx}>
        <TextField
          select
          size="small"
          value={identifierKind}
          onChange={(event) =>
            setIdentifierKind(event.target.value as 'username' | 'email')
          }
          label={t('accounts.addIdentifier.kindLabel')}
          sx={{ minWidth: 140 }}
        >
          <MenuItem value="username">
            {t('accounts.identifierKind.username')}
          </MenuItem>
          <MenuItem value="email">{t('accounts.identifierKind.email')}</MenuItem>
        </TextField>
        <TextField
          size="small"
          value={identifierValue}
          onChange={(event) => setIdentifierValue(event.target.value)}
          label={t('accounts.addIdentifier.valueLabel')}
        />
        <Button
          type="button"
          variant="outlined"
          disabled={addIdentifier.isPending || identifierValue === ''}
          onClick={() =>
            addIdentifier.mutate(
              { id, kind: identifierKind, value: identifierValue },
              { onSuccess: () => setIdentifierValue('') },
            )
          }
        >
          {t('accounts.addIdentifier.submit')}
        </Button>
        <InlineError
          code={addIdentifier.isError ? errorCodeOf(addIdentifier.error) : null}
        />
      </Box>

      <Box sx={rowSx}>
        <TextField
          size="small"
          value={secretLabel}
          onChange={(event) => setSecretLabel(event.target.value)}
          label={t('accounts.createSecret.labelLabel')}
        />
        <TextField
          size="small"
          value={secretExpires}
          onChange={(event) => setSecretExpires(event.target.value)}
          label={t('accounts.createSecret.expiresLabel')}
          helperText={t('accounts.createSecret.expiresHelper')}
        />
        <Button
          type="button"
          variant="outlined"
          disabled={createSecret.isPending || secretLabel === ''}
          onClick={() =>
            createSecret.mutate(
              {
                id,
                label: secretLabel,
                ...(secretExpires === '' ? {} : { expires_at: secretExpires }),
              },
              {
                onSuccess: (answer) => {
                  setCreatedSecret(answer.secret)
                  setSecretLabel('')
                  setSecretExpires('')
                },
              },
            )
          }
        >
          {t('accounts.createSecret.submit')}
        </Button>
        <InlineError
          code={createSecret.isError ? errorCodeOf(createSecret.error) : null}
        />
      </Box>

      {createdSecret !== null && (
        <Typography variant="body2" sx={{ wordBreak: 'break-all', mb: 2 }}>
          {t('accounts.createSecret.handedOver', { secret: createdSecret })}
        </Typography>
      )}
    </>
  )
}
