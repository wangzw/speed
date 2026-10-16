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
import { useForm } from 'react-hook-form'
import type { SubmitHandler } from 'react-hook-form'
import {
  DataTable,
  FormField,
  FormLayout,
  ListSkeleton,
  PageHeader,
  errorCodeOf,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { formatUtc } from './internal/datetime.js'
import { InlineError } from './internal/inline-error.js'
import { FormWidth } from './internal/layout.js'
import { useConsoleStepUp } from './internal/step-up-gate.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
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
  const stepUp = useConsoleStepUp()

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

  // The step-up-gated writes each fire through the gate: a refusal the
  // server answers while the session is still established (a strength
  // shortfall) arms the dialog with a re-fire of the same write and clears
  // the mutation's own error, so the dialog opens alone rather than beside
  // the 401 banner.

  function deleteOne(id: string): void {
    deleteAccount.mutate(
      { id },
      {
        onSuccess: () => {
          if (selectedId === id) {
            setSelectedId('')
          }
        },
        onError: (error) => {
          if (stepUp.intercept(error, () => deleteOne(id))) {
            deleteAccount.reset()
          }
        },
      },
    )
  }

  function resetAllFactors(): void {
    resetMFA.mutate(
      { id: selectedId },
      {
        onError: (error) => {
          if (stepUp.intercept(error, resetAllFactors)) {
            resetMFA.reset()
          }
        },
      },
    )
  }

  function revokeAllSessions(): void {
    revokeSessions.mutate(
      { id: selectedId },
      {
        onError: (error) => {
          if (stepUp.intercept(error, revokeAllSessions)) {
            revokeSessions.reset()
          }
        },
      },
    )
  }

  function removeOne(identifier: string): void {
    removeIdentifier.mutate(
      { id: selectedId, identifier },
      {
        onSuccess: () => setIdentifierToRemove(''),
        onError: (error) => {
          if (stepUp.intercept(error, () => removeOne(identifier))) {
            removeIdentifier.reset()
          }
        },
      },
    )
  }

  function revokeOneSecret(secret: string): void {
    revokeSecret.mutate(
      { id: selectedId, secret },
      {
        onError: (error) => {
          if (stepUp.intercept(error, () => revokeOneSecret(secret))) {
            revokeSecret.reset()
          }
        },
      },
    )
  }

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
                run: () => deleteOne(row.id),
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
              run: () => revokeOneSecret(row.id),
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
                  run: () => resetAllFactors(),
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
                  run: () => revokeAllSessions(),
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
                  run: () => removeOne(identifierToRemove),
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

/** The create form's fields, in the shape react-hook-form holds them. */
type CreateAccountFields = {
  kind: 'user' | 'service'
  displayName: string
  username: string
  email: string
  emailVia: VerifiedVia
  phone: string
  phoneVia: VerifiedVia
  password: string
  mfaRequired: boolean
  attributes: string
}

/**
 * AccountCreateForm: the account-creation control (POST /auth/admin/accounts).
 * It names the fields the handler decodes into its create request; each is
 * optional but the kind, and a field left blank is not sent. The fields flow
 * through ui-kit's form family (FormLayout's responsive grid of FormField
 * adapters), so they wrap into a form rather than crowding one row. The
 * attribute set is typed as a JSON object and marks the field when it does
 * not parse. A landed create clears the form; a refused one keeps it to
 * correct.
 */
function AccountCreateForm() {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const createAccount = operations.useCreateAccount()

  const form = useForm<CreateAccountFields>({
    defaultValues: {
      kind: 'user',
      displayName: '',
      username: '',
      email: '',
      emailVia: '',
      phone: '',
      phoneVia: '',
      password: '',
      mfaRequired: false,
      attributes: '',
    },
  })

  const viaOptions = (
    <>
      <MenuItem value="">{t('accounts.create.via.none')}</MenuItem>
      <MenuItem value="admin">{t('accounts.create.via.admin')}</MenuItem>
      <MenuItem value="import-unverified">
        {t('accounts.create.via.importUnverified')}
      </MenuItem>
    </>
  )

  const onSubmit: SubmitHandler<CreateAccountFields> = (values) => {
    const parsed =
      values.attributes.trim() === '' ? null : parseStringMap(values.attributes)
    createAccount.mutate(
      {
        kind: values.kind,
        ...(values.displayName === '' ? {} : { display_name: values.displayName }),
        ...(values.username === '' ? {} : { username: values.username }),
        ...(values.email === '' ? {} : { email: values.email }),
        ...(values.emailVia === '' ? {} : { email_verified_via: values.emailVia }),
        ...(values.phone === '' ? {} : { phone: values.phone }),
        ...(values.phoneVia === '' ? {} : { phone_verified_via: values.phoneVia }),
        ...(values.password === '' ? {} : { password: values.password }),
        ...(parsed === null ? {} : { attributes: parsed }),
        ...(values.mfaRequired ? { mfa_required: true } : {}),
      },
      { onSuccess: () => form.reset() },
    )
  }

  return (
    <Box component="section" sx={{ mb: 3 }}>
      <Typography variant="h2" sx={{ fontSize: '1.25rem', mb: 1 }}>
        {t('accounts.create.title')}
      </Typography>
      <FormWidth>
        <FormLayout
          form={form}
          onSubmit={onSubmit}
          columns={2}
          maxWidth={false}
          actions={
            <Button
              type="submit"
              variant="contained"
              disabled={createAccount.isPending}
            >
              {t('accounts.create.submit')}
            </Button>
          }
        >
          <FormField
            name="kind"
            render={({ field }) => (
              <TextField
                {...field}
                select
                size="small"
                fullWidth
                label={t('accounts.create.kindLabel')}
              >
                <MenuItem value="user">{t('accounts.create.kind.user')}</MenuItem>
                <MenuItem value="service">{t('accounts.create.kind.service')}</MenuItem>
              </TextField>
            )}
          />
          <FormField
            name="displayName"
            render={({ field }) => (
              <TextField
                {...field}
                size="small"
                fullWidth
                label={t('accounts.create.displayNameLabel')}
              />
            )}
          />
          <FormField
            name="username"
            render={({ field }) => (
              <TextField
                {...field}
                size="small"
                fullWidth
                label={t('accounts.create.usernameLabel')}
              />
            )}
          />
          <FormField
            name="email"
            render={({ field }) => (
              <TextField
                {...field}
                size="small"
                fullWidth
                label={t('accounts.create.emailLabel')}
              />
            )}
          />
          <FormField
            name="emailVia"
            render={({ field }) => (
              <TextField
                {...field}
                select
                size="small"
                fullWidth
                label={t('accounts.create.emailViaLabel')}
              >
                {viaOptions}
              </TextField>
            )}
          />
          <FormField
            name="phone"
            render={({ field }) => (
              <TextField
                {...field}
                size="small"
                fullWidth
                label={t('accounts.create.phoneLabel')}
              />
            )}
          />
          <FormField
            name="phoneVia"
            render={({ field }) => (
              <TextField
                {...field}
                select
                size="small"
                fullWidth
                label={t('accounts.create.phoneViaLabel')}
              >
                {viaOptions}
              </TextField>
            )}
          />
          <FormField
            name="password"
            render={({ field }) => (
              <TextField
                {...field}
                size="small"
                fullWidth
                type="password"
                label={t('accounts.create.passwordLabel')}
              />
            )}
          />
          <FormField
            name="attributes"
            rules={{
              validate: (value) =>
                value.trim() === '' ||
                parseStringMap(value) !== null ||
                t('accounts.create.attributesHelper'),
            }}
            render={({ field, invalid, errorText }) => (
              <TextField
                {...field}
                size="small"
                fullWidth
                multiline
                minRows={2}
                label={t('accounts.create.attributesLabel')}
                error={invalid}
                helperText={errorText ?? t('accounts.create.attributesHelper')}
              />
            )}
          />
          <FormField
            name="mfaRequired"
            render={({ field }) => (
              <FormControlLabel
                control={
                  <Checkbox
                    checked={field.value}
                    onChange={(event) => field.onChange(event.target.checked)}
                    onBlur={field.onBlur}
                    name={field.name}
                  />
                }
                label={t('accounts.create.mfaLabel')}
              />
            )}
          />
        </FormLayout>
      </FormWidth>
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
  const stepUp = useConsoleStepUp()
  const patchAccount = operations.usePatchAccount()
  const setStatus = operations.useSetAccountStatus()
  const setMfaRequired = operations.useSetMFARequired()
  const resetPassword = operations.useResetPassword()
  const addIdentifier = operations.useAddIdentifier()
  const createSecret = operations.useCreateSecret()

  const id = account.id
  // Every control is seeded from the account as loaded, so a control shows
  // the account's own value and a write fires only for a field the operator
  // actually changed: a status or MFA change goes out only when it differs
  // from the account's, and the patch carries only the fields that moved.
  const currentStatus: 'active' | 'disabled' =
    account.status === 'disabled' ? 'disabled' : 'active'
  const currentAttributes =
    account.attributes === undefined ? '' : JSON.stringify(account.attributes)
  const currentProtected: 'true' | 'false' = account.protected ? 'true' : 'false'

  const [statusDraft, setStatusDraft] = useState<'active' | 'disabled'>(currentStatus)
  const [mfaDraft, setMfaDraft] = useState<'true' | 'false'>(
    account.mfa_required ? 'true' : 'false',
  )
  const [displayName, setDisplayName] = useState(account.display_name)
  const [attributes, setAttributes] = useState(currentAttributes)
  const [protectedDraft, setProtectedDraft] = useState<'unchanged' | 'true' | 'false'>(
    currentProtected,
  )
  const [tempPassword, setTempPassword] = useState('')
  const [identifierKind, setIdentifierKind] = useState<'username' | 'email'>('username')
  const [identifierValue, setIdentifierValue] = useState('')
  const [secretLabel, setSecretLabel] = useState('')
  const [secretExpires, setSecretExpires] = useState('')
  const [createdSecret, setCreatedSecret] = useState<string | null>(null)

  const attributesInvalid =
    attributes.trim() !== '' && parseStringMap(attributes) === null
  const statusUnchanged = statusDraft === currentStatus
  const mfaUnchanged = (mfaDraft === 'true') === account.mfa_required
  // The protected control keeps its 'unchanged' sentinel: selecting it, or
  // leaving the control at the account's own value, is a no-op, and the patch
  // carries a protected field only for a real change.
  const protectedUnchanged =
    protectedDraft === 'unchanged' || protectedDraft === currentProtected
  const patchUnchanged =
    displayName === account.display_name &&
    attributes === currentAttributes &&
    protectedUnchanged

  // Each write fires through the step-up gate: a refusal the server answers
  // while the session is still established arms the dialog with a re-fire
  // of the same write, and the mutation's own error is cleared so the
  // dialog opens alone.

  function saveStatus(): void {
    setStatus.mutate(
      { id, status: statusDraft },
      {
        onError: (error) => {
          if (stepUp.intercept(error, saveStatus)) {
            setStatus.reset()
          }
        },
      },
    )
  }

  function saveMfa(): void {
    setMfaRequired.mutate(
      { id, required: mfaDraft === 'true' },
      {
        onError: (error) => {
          if (stepUp.intercept(error, saveMfa)) {
            setMfaRequired.reset()
          }
        },
      },
    )
  }

  function savePatch(): void {
    const parsed = attributes.trim() === '' ? null : parseStringMap(attributes)
    patchAccount.mutate(
      {
        id,
        ...(displayName === account.display_name ? {} : { display_name: displayName }),
        ...(attributes === currentAttributes || parsed === null
          ? {}
          : { attributes: parsed }),
        ...(protectedUnchanged ? {} : { protected: protectedDraft === 'true' }),
      },
      {
        onError: (error) => {
          if (stepUp.intercept(error, savePatch)) {
            patchAccount.reset()
          }
        },
      },
    )
  }

  function savePassword(): void {
    resetPassword.mutate(
      { id, password: tempPassword },
      {
        onSuccess: () => setTempPassword(''),
        onError: (error) => {
          if (stepUp.intercept(error, savePassword)) {
            resetPassword.reset()
          }
        },
      },
    )
  }

  function addOne(): void {
    addIdentifier.mutate(
      { id, kind: identifierKind, value: identifierValue },
      {
        onSuccess: () => setIdentifierValue(''),
        onError: (error) => {
          if (stepUp.intercept(error, addOne)) {
            addIdentifier.reset()
          }
        },
      },
    )
  }

  function createOne(): void {
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
        onError: (error) => {
          if (stepUp.intercept(error, createOne)) {
            createSecret.reset()
          }
        },
      },
    )
  }

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
          disabled={setStatus.isPending || statusUnchanged}
          onClick={() => saveStatus()}
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
          disabled={setMfaRequired.isPending || mfaUnchanged}
          onClick={() => saveMfa()}
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
          disabled={patchAccount.isPending || attributesInvalid || patchUnchanged}
          onClick={() => savePatch()}
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
          onClick={() => savePassword()}
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
          onClick={() => addOne()}
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
          onClick={() => createOne()}
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

      {stepUp.session !== null && (
        <StepUpChallenge
          open={stepUp.gate.open}
          session={stepUp.session}
          onSuccess={stepUp.gate.succeed}
          onCancel={stepUp.gate.cancel}
        />
      )}
    </>
  )
}
