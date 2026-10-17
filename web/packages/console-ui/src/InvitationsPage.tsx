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
  DataTable,
  EmptyState,
  ListSkeleton,
  PageHeader,
  errorCodeOf,
  touchTargetSx,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { formatUtc } from './internal/datetime.js'
import { InlineError } from './internal/inline-error.js'
import { useConsoleInvitationLink } from './internal/invitation-link.js'
import { TenantScope } from './internal/tenant-scope.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type {
  ConsoleInvitation,
  ConsoleIssuedInvitation,
  ConsoleMember,
} from './operations.js'

/** The invitation status's label, guarding the closed set. */
const INVITATION_STATUSES = new Set<string>(['pending', 'accepted', 'revoked'])

function statusLabel(status: string, t: (key: string) => string): string {
  return INVITATION_STATUSES.has(status)
    ? t(`invitations.status.${status}`)
    : t('invitations.status.other')
}

/**
 * The RFC 3339 instant a datetime-local value names, or undefined for an
 * empty or unparseable one. The picker yields a zone-less local wall-clock
 * value; the request carries the instant it names.
 */
function toInviteExpiry(value: string): string | undefined {
  const trimmed = value.trim()
  if (trimmed === '') {
    return undefined
  }
  const instant = new Date(trimmed)
  return Number.isNaN(instant.getTime()) ? undefined : instant.toISOString()
}

export function InvitationsPage() {
  const { t, i18n } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const invitationLink = useConsoleInvitationLink()

  const [tenant, setTenant] = useState('')
  const [email, setEmail] = useState('')
  const [expiresAt, setExpiresAt] = useState('')
  // The invitation just issued, held so its acceptance token -- handed out
  // once -- can be shown as the invitee's link rather than dropped.
  const [issued, setIssued] = useState<ConsoleIssuedInvitation | null>(null)
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
      cellWrap: 'nowrap',
      cell: (invitation) => invitation.email,
    },
    {
      id: 'status',
      header: t('invitations.column.status'),
      cellWrap: 'nowrap',
      cell: (invitation) => statusLabel(invitation.status, t),
    },
    {
      id: 'invitedBy',
      header: t('invitations.column.invitedBy'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (invitation) => invitation.invited_by,
    },
    {
      id: 'expires',
      header: t('invitations.column.expires'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (invitation) => formatUtc(invitation.expires_at, i18n.language),
    },
    {
      id: 'actions',
      header: t('invitations.column.actions'),
      cellWrap: 'nowrap',
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
          sx={touchTargetSx}
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
      cellWrap: 'nowrap',
      cell: (member) => member.account,
    },
    {
      id: 'active',
      header: t('members.column.active'),
      cellWrap: 'nowrap',
      cell: (member) =>
        member.active ? t('members.activeYes') : t('members.activeNo'),
    },
    {
      id: 'ssoExempt',
      header: t('members.column.ssoExempt'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (member) =>
        member.sso_exempt ? t('members.ssoExemptYes') : t('members.ssoExemptNo'),
    },
    {
      id: 'source',
      header: t('members.column.source'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (member) => member.source,
    },
    {
      id: 'actions',
      header: t('members.column.actions'),
      cellWrap: 'nowrap',
      cell: (member) => (
        <Box sx={{ display: 'flex', columnGap: 1 }}>
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
            sx={touchTargetSx}
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
            sx={touchTargetSx}
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
            sx={touchTargetSx}
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
                type="datetime-local"
                value={expiresAt}
                onChange={(event) => setExpiresAt(event.target.value)}
                label={t('invitations.issue.expiresLabel')}
                helperText={t('invitations.issue.expiresHelper')}
                slotProps={{ inputLabel: { shrink: true } }}
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
                      expiresAt: toInviteExpiry(expiresAt),
                    },
                    {
                      onSuccess: (answer) => {
                        setEmail('')
                        setIssued(answer)
                      },
                    },
                  )
                }}
              >
                {t('invitations.issue.submit')}
              </Button>
            </Box>
            {issued != null && (
              <Box sx={{ mb: 2 }}>
                <Typography variant="body2">
                  {invitationLink === undefined
                    ? t('invitations.issued.token', {
                        email: issued.invitation.email,
                      })
                    : t('invitations.issued.link', {
                        email: issued.invitation.email,
                      })}
                </Typography>
                <Typography
                  variant="body2"
                  sx={{ wordBreak: 'break-all', fontFamily: 'monospace' }}
                >
                  {invitationLink === undefined
                    ? issued.token
                    : acceptanceUrl(invitationLink(issued.token))}
                </Typography>
              </Box>
            )}
            <ConsoleAsyncSection
              pending={invitationsPending}
              payload={invitationRows}
              error={invitations.error}
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
            </ConsoleAsyncSection>
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
            <ConsoleAsyncSection
              pending={membersPending}
              payload={memberRows}
              error={members.error}
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
            </ConsoleAsyncSection>
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
 * The absolute URL for a host-built acceptance link: the builder may return
 * a host-relative path (the landing route) or an absolute URL, and the
 * operator copies it whole, so a path is resolved against the current origin.
 * A value that will not resolve is shown as it came.
 */
function acceptanceUrl(link: string): string {
  try {
    return new URL(link, window.location.origin).toString()
  } catch {
    return link
  }
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
