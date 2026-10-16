/**
 * InvitationsPage: the tenant invitation and membership page of the
 * console.
 *
 * Both surfaces are tenant-scoped (/auth/admin/tenants/:tenant/...), so
 * the page opens with a tenant selector and keeps its reads disabled
 * until a tenant is chosen. It renders:
 *
 *  - the tenant's invitations: list them (address, status, inviter,
 *    expiry), invite an address, and revoke a pending invitation;
 *  - the tenant's members: list them (account, active, SSO exemption,
 *    source) and remove a membership.
 *
 * Inviting an address mints an invitation the invitee accepts through the
 * tenant-facing invite surface; the console issues and revokes, it does
 * not accept. Every read and write is an operation on the console
 * operations bundle, so the page builds no client and writes no query
 * key. The page opens with ui-kit's PageHeader.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Checkbox from '@mui/material/Checkbox'
import FormControlLabel from '@mui/material/FormControlLabel'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  AsyncSection,
  DataTable,
  EmptyState,
  ListSkeleton,
  PageHeader,
  errorCodeOf,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { formatUtc } from './internal/datetime.js'
import { InlineError } from './internal/inline-error.js'
import { TenantScope } from './internal/tenant-scope.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleInvitation, ConsoleMember } from './operations.js'

/** The invitation status's label, guarding the closed set. */
const INVITATION_STATUSES = new Set<string>(['pending', 'accepted', 'revoked'])

function statusLabel(status: string, t: (key: string) => string): string {
  return INVITATION_STATUSES.has(status)
    ? t(`invitations.status.${status}`)
    : t('invitations.status.other')
}

export function InvitationsPage() {
  const { t, i18n } = useConsoleUiTranslation()
  const operations = useConsoleOperations()

  const [tenant, setTenant] = useState('')
  const [email, setEmail] = useState('')
  const [expiresAt, setExpiresAt] = useState('')
  const confirm = usePendingConfirm()

  const invitations = operations.useInvitations(tenant)
  const members = operations.useMembers(tenant)
  const issueInvitation = operations.useIssueInvitation()
  const revokeInvitation = operations.useRevokeInvitation()
  const removeMember = operations.useRemoveMember()
  const setMemberSsoExempt = operations.useSetMemberSsoExempt()

  const invitationColumns: DataTableColumn<ConsoleInvitation>[] = [
    {
      id: 'email',
      header: t('invitations.column.email'),
      cell: (invitation) => invitation.email,
    },
    {
      id: 'status',
      header: t('invitations.column.status'),
      cell: (invitation) => statusLabel(invitation.status, t),
    },
    {
      id: 'invitedBy',
      header: t('invitations.column.invitedBy'),
      priority: 'medium',
      cell: (invitation) => invitation.invited_by,
    },
    {
      id: 'expires',
      header: t('invitations.column.expires'),
      priority: 'low',
      cell: (invitation) => formatUtc(invitation.expires_at, i18n.language),
    },
    {
      id: 'actions',
      header: t('invitations.column.actions'),
      cell: (invitation) => (
        <Button
          type="button"
          size="small"
          variant="outlined"
          color="error"
          disabled={invitation.status !== 'pending' || revokeInvitation.isPending}
          onClick={() =>
            confirm.ask({
              title: t('invitations.revokeTitle'),
              message: t('invitations.revokeMessage', { email: invitation.email }),
              confirmLabel: t('common.confirm'),
              run: () => revokeInvitation.mutate({ tenant, id: invitation.id }),
            })
          }
        >
          {t('invitations.revoke')}
        </Button>
      ),
    },
  ]

  const memberColumns: DataTableColumn<ConsoleMember>[] = [
    {
      id: 'account',
      header: t('members.column.account'),
      cell: (member) => member.account,
    },
    {
      id: 'active',
      header: t('members.column.active'),
      cell: (member) =>
        member.active ? t('members.activeYes') : t('members.activeNo'),
    },
    {
      id: 'ssoExempt',
      header: t('members.column.ssoExempt'),
      priority: 'low',
      cell: (member) =>
        member.sso_exempt ? t('members.ssoExemptYes') : t('members.ssoExemptNo'),
    },
    {
      id: 'source',
      header: t('members.column.source'),
      priority: 'medium',
      cell: (member) => member.source,
    },
    {
      id: 'actions',
      header: t('members.column.actions'),
      cell: (member) => (
        <Box sx={{ display: 'flex', flexWrap: 'wrap', columnGap: 1, rowGap: 1 }}>
          <Button
            type="button"
            size="small"
            variant="outlined"
            disabled={setMemberSsoExempt.isPending}
            onClick={() =>
              setMemberSsoExempt.mutate({
                tenant,
                account: member.account,
                sso_exempt: !member.sso_exempt,
              })
            }
          >
            {member.sso_exempt
              ? t('members.ssoExemptClear')
              : t('members.ssoExemptSet')}
          </Button>
          <Button
            type="button"
            size="small"
            variant="outlined"
            color="error"
            disabled={removeMember.isPending}
            onClick={() =>
              confirm.ask({
                title: t('members.removeTitle'),
                message: t('members.removeMessage', { account: member.account }),
                confirmLabel: t('common.confirm'),
                run: () => removeMember.mutate({ tenant, account: member.account }),
              })
            }
          >
            {t('members.remove')}
          </Button>
        </Box>
      ),
    },
  ]

  const invitationRows = invitations.data?.invitations
  const memberRows = members.data
  const invitationsPending = invitations.isPending && invitationRows === undefined
  const membersPending = members.isPending && memberRows === undefined

  const writeError = firstErrorCode([
    issueInvitation.error,
    revokeInvitation.error,
    removeMember.error,
    setMemberSsoExempt.error,
  ])

  return (
    <Box>
      <PageHeader
        title={t('invitations.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('invitations.title') },
        ]}
        actions={
          <Button
            type="button"
            variant="outlined"
            size="small"
            onClick={() => {
              void invitations.refetch()
              void members.refetch()
            }}
          >
            {t('common.refresh')}
          </Button>
        }
        sx={{ mb: 2 }}
      />

      <Box component="section" sx={{ mb: 3 }}>
        <TenantScope value={tenant} onSelect={setTenant} />
      </Box>

      {tenant === '' ? (
        <EmptyState
          headingLevel="h2"
          title={t('invitations.needTenant.title')}
          description={t('invitations.needTenant.description')}
        />
      ) : (
        <>
          <Box component="section" sx={{ mb: 3 }}>
            <Typography
              component="h2"
              variant="h2"
              sx={{ fontSize: '1.25rem', mb: 1 }}
            >
              {t('invitations.section.title')}
            </Typography>
            <Box
              sx={{
                display: 'flex',
                flexWrap: 'wrap',
                alignItems: 'flex-end',
                columnGap: 2,
                rowGap: 1,
                mb: 2,
              }}
            >
              <TextField
                size="small"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                label={t('invitations.issue.emailLabel')}
              />
              <TextField
                size="small"
                value={expiresAt}
                onChange={(event) => setExpiresAt(event.target.value)}
                label={t('invitations.issue.expiresLabel')}
              />
              <Button
                type="button"
                variant="contained"
                disabled={issueInvitation.isPending || email.trim() === ''}
                onClick={() => {
                  issueInvitation.mutate(
                    {
                      tenant,
                      email,
                      expiresAt: expiresAt.trim() === '' ? undefined : expiresAt,
                    },
                    { onSuccess: () => setEmail('') },
                  )
                }}
              >
                {t('invitations.issue.submit')}
              </Button>
            </Box>
            <AsyncSection
              pending={invitationsPending}
              payload={invitationRows}
              empty={invitationRows !== undefined && invitationRows.length === 0}
              headingLevel="h3"
              loading={
                <ListSkeleton
                  label={t('invitations.loading')}
                  rows={[<Skeleton key="a" variant="text" width="54%" />]}
                />
              }
              errorState={{
                title: t('invitations.error.title'),
                description: t('invitations.error.description'),
                retryLabel: t('invitations.retry'),
                onRetry: () => void invitations.refetch(),
              }}
              emptyState={{
                title: t('invitations.empty.title'),
                description: t('invitations.empty.description'),
              }}
            >
              {(settled) => (
                <DataTable
                  rows={settled}
                  columns={invitationColumns}
                  rowKey={(invitation) => invitation.id}
                />
              )}
            </AsyncSection>
          </Box>

          <Box component="section" sx={{ mb: 3 }}>
            <Typography
              component="h2"
              variant="h2"
              sx={{ fontSize: '1.25rem', mb: 1 }}
            >
              {t('members.title')}
            </Typography>
            <MemberWriteForm tenant={tenant} />
            <AsyncSection
              pending={membersPending}
              payload={memberRows}
              empty={memberRows !== undefined && memberRows.length === 0}
              headingLevel="h3"
              loading={
                <ListSkeleton
                  label={t('members.loading')}
                  rows={[<Skeleton key="a" variant="text" width="50%" />]}
                />
              }
              errorState={{
                title: t('members.error.title'),
                description: t('members.error.description'),
                retryLabel: t('members.retry'),
                onRetry: () => void members.refetch(),
              }}
              emptyState={{
                title: t('members.empty.title'),
                description: t('members.empty.description'),
              }}
            >
              {(settled) => (
                <DataTable
                  rows={settled}
                  columns={memberColumns}
                  rowKey={(member) => member.account}
                />
              )}
            </AsyncSection>
          </Box>

          <InlineError code={writeError} />
        </>
      )}

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

/**
 * MemberWriteForm: the membership-write control (PUT
 * /auth/admin/tenants/:tenant/members/:account) -- it adds, suspends or
 * resumes a membership, naming the account, its active state, an optional
 * source and the SSO exemption. A landed write clears the account and source
 * and resets the flags; a refused one keeps them to correct.
 */
function MemberWriteForm({ tenant }: { readonly tenant: string }) {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const writeMember = operations.useWriteMember()

  const [account, setAccount] = useState('')
  const [source, setSource] = useState('')
  const [active, setActive] = useState(true)
  const [ssoExempt, setSsoExempt] = useState(false)

  return (
    <Box sx={{ mb: 2 }}>
      <Box
        sx={{
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'center',
          columnGap: 2,
          rowGap: 1,
        }}
      >
        <TextField
          size="small"
          value={account}
          onChange={(event) => setAccount(event.target.value)}
          label={t('members.write.accountLabel')}
        />
        <TextField
          size="small"
          value={source}
          onChange={(event) => setSource(event.target.value)}
          label={t('members.write.sourceLabel')}
        />
        <FormControlLabel
          control={
            <Checkbox
              checked={active}
              onChange={(event) => setActive(event.target.checked)}
            />
          }
          label={t('members.write.activeLabel')}
        />
        <FormControlLabel
          control={
            <Checkbox
              checked={ssoExempt}
              onChange={(event) => setSsoExempt(event.target.checked)}
            />
          }
          label={t('members.write.ssoExemptLabel')}
        />
        <Button
          type="button"
          variant="contained"
          disabled={writeMember.isPending || account === ''}
          onClick={() =>
            writeMember.mutate(
              {
                tenant,
                account,
                active,
                ...(source === '' ? {} : { source }),
                sso_exempt: ssoExempt,
              },
              {
                onSuccess: () => {
                  setAccount('')
                  setSource('')
                  setActive(true)
                  setSsoExempt(false)
                },
              },
            )
          }
        >
          {t('members.write.submit')}
        </Button>
      </Box>
      <Box sx={{ mt: 1 }}>
        <InlineError
          code={writeMember.isError ? errorCodeOf(writeMember.error) : null}
        />
      </Box>
    </Box>
  )
}
