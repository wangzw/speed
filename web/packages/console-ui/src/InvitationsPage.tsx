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
 * The inviter and the member account render through the shared account
 * identity unit: the readable display name where the account read answers,
 * the recorded identifier with an unresolved note where it is refused. The
 * table columns keep the recorded identifier as their full value, so the
 * copy and hover channel stays the value the record carries.
 *
 * Inviting an address mints an invitation the invitee accepts through the
 * tenant-facing invite surface; the console issues and revokes, it does
 * not accept. Every read and write is an operation on the console
 * operations bundle, so the page builds no client and writes no query
 * key.
 *
 * The two titled sections are the page's two feedback scopes: the
 * invitation list (issue and revoke) reports under its table, and the
 * member list (write, remove and SSO exemption) reports under its own.
 * A refusal marks the affected row inline with the same resolved text the
 * region shows and brings the region into view; a success names the
 * invitee or account it changed. The tenant seeds every write, so changing
 * it resets both scopes and disables the selector while either is in
 * flight. The page opens with ui-kit's PageHeader.
 */

import { useEffect, useState } from 'react'
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
  useWriteFeedback,
} from '@speed/ui-kit'
import type { DataTableColumn, WriteFeedback } from '@speed/ui-kit'
import { AccountIdentity } from './internal/account-identity.js'
import { AccountSelector } from './internal/account-selector.js'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { formatUtc } from './internal/datetime.js'
import { useConsoleUiErrorText } from './internal/error-text.js'
import { useConsoleInvitationLink } from './internal/invitation-link.js'
import { TenantScope, tenantFromLocation } from './internal/tenant-scope.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import {
  failureTextOf,
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { useConsoleOperations } from './operations.js'
import type {
  ConsoleAccount,
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

  const [tenant, setTenant] = useState(tenantFromLocation)
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

  // One scope per titled section: the invitation list (issue and revoke)
  // and the member list (write, remove and SSO exemption).
  const invitationFeedback = useWriteFeedback()
  const membersFeedback = useWriteFeedback()
  const invitationRegionRef = useFailureScroll(invitationFeedback.outcome)
  const membersRegionRef = useFailureScroll(membersFeedback.outcome)

  // The row each scope's failure names, cleared when the attempt starts.
  // The member marker also carries which of the row's two writes failed, so
  // the inline marker sits on the control that was refused.
  const [failedInvitation, setFailedInvitation] = useState<string | null>(null)
  const [failedMember, setFailedMember] = useState<{
    readonly account: string
    readonly action: 'remove' | 'sso'
  } | null>(null)
  const resolveErrorText = useConsoleUiErrorText()
  const invitationFailureText = failureTextOf(
    invitationFeedback.outcome,
    resolveErrorText,
  )
  const memberFailureText = failureTextOf(
    membersFeedback.outcome,
    resolveErrorText,
  )

  // The tenant is an argument of every write here: a change clears both
  // slots and every row marker, since the old result would read as
  // describing the tenant left behind.
  const changeTenant = (next: string) => {
    setTenant(next)
    invitationFeedback.reset()
    membersFeedback.reset()
    setFailedInvitation(null)
    setFailedMember(null)
  }

  const issue = () => {
    const address = email
    void invitationFeedback.run(async () => {
      const answer = await issueInvitation.mutateAsync({
        tenant,
        email: address,
        expiresAt: toInviteExpiry(expiresAt),
      })
      setEmail('')
      setIssued(answer)
      return t('invitations.issued.confirm', {
        email: answer.invitation.email,
      })
    })
  }

  const requestRevoke = (invitation: ConsoleInvitation) => {
    confirm.ask({
      title: t('invitations.revokeTitle'),
      message: t('invitations.revokeMessage', { email: invitation.email }),
      confirmLabel: t('common.confirm'),
      run: () => {
        setFailedInvitation(null)
        void invitationFeedback.run(async () => {
          try {
            await revokeInvitation.mutateAsync({
              tenant,
              id: invitation.id,
            })
            return t('invitations.revoked', { email: invitation.email })
          } catch (error) {
            setFailedInvitation(invitation.id)
            throw error
          }
        })
      },
    })
  }

  const requestRemoveMember = (member: ConsoleMember) => {
    confirm.ask({
      title: t('members.removeTitle'),
      message: t('members.removeMessage', { account: member.account }),
      confirmLabel: t('common.confirm'),
      run: () => {
        setFailedMember(null)
        void membersFeedback.run(async () => {
          try {
            await removeMember.mutateAsync({
              tenant,
              account: member.account,
            })
            return t('members.removed', { account: member.account })
          } catch (error) {
            setFailedMember({ account: member.account, action: 'remove' })
            throw error
          }
        })
      },
    })
  }

  const toggleSsoExempt = (member: ConsoleMember) => {
    const exempt = !member.sso_exempt
    setFailedMember(null)
    void membersFeedback.run(async () => {
      try {
        await setMemberSsoExempt.mutateAsync({
          tenant,
          account: member.account,
          sso_exempt: exempt,
        })
        return t(
          exempt ? 'members.ssoExemptSaved' : 'members.ssoExemptCleared',
          { account: member.account },
        )
      } catch (error) {
        setFailedMember({ account: member.account, action: 'sso' })
        throw error
      }
    })
  }

  const invitationColumns: DataTableColumn<ConsoleInvitation>[] = [
    {
      id: 'email',
      header: t('invitations.column.email'),
      cellWrap: 'nowrap',
      cellFullValue: (invitation) => invitation.email,
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
      cellFullValue: (invitation) => invitation.invited_by,
      cell: (invitation) => (
        <AccountIdentity account={invitation.invited_by} />
      ),
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
      cell: (invitation) => {
        const failed = failedInvitation === invitation.id
        const errorId = `invitation-error-${encodeURIComponent(invitation.id)}`
        const pending =
          invitationFeedback.pending && invitation.status === 'pending'
        return (
          <Box>
            <Button
              type="button"
              size="small"
              variant="outlined"
              color="error"
              aria-invalid={failed}
              aria-describedby={failed ? errorId : undefined}
              disabled={invitation.status !== 'pending' || pending}
              loading={pending}
              onClick={() => requestRevoke(invitation)}
            >
              {t('invitations.revoke')}
            </Button>
            {failed && invitationFailureText !== null && (
              <Typography
                id={errorId}
                variant="caption"
                color="error"
                sx={{ display: 'block', mt: 0.5 }}
              >
                {invitationFailureText}
              </Typography>
            )}
          </Box>
        )
      },
    },
  ]

  const memberColumns: DataTableColumn<ConsoleMember>[] = [
    {
      id: 'account',
      header: t('members.column.account'),
      cellWrap: 'nowrap',
      cellFullValue: (member) => member.account,
      cell: (member) => <AccountIdentity account={member.account} />,
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
      cell: (member) => {
        const errorId = `member-error-${encodeURIComponent(member.account)}`
        const ssoFailed =
          failedMember?.account === member.account &&
          failedMember.action === 'sso'
        const removeFailed =
          failedMember?.account === member.account &&
          failedMember.action === 'remove'
        return (
          <Box>
            <Box sx={{ display: 'flex', columnGap: 1 }}>
              <Button
                type="button"
                size="small"
                variant="outlined"
                aria-invalid={ssoFailed}
                aria-describedby={ssoFailed ? errorId : undefined}
                disabled={membersFeedback.pending}
                loading={membersFeedback.pending}
                onClick={() => toggleSsoExempt(member)}
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
                aria-invalid={removeFailed}
                aria-describedby={removeFailed ? errorId : undefined}
                disabled={membersFeedback.pending}
                loading={membersFeedback.pending}
                onClick={() => requestRemoveMember(member)}
              >
                {t('members.remove')}
              </Button>
            </Box>
            {memberFailureText !== null &&
              failedMember?.account === member.account && (
                <Typography
                  id={errorId}
                  variant="caption"
                  color="error"
                  sx={{ display: 'block', mt: 0.5 }}
                >
                  {memberFailureText}
                </Typography>
              )}
          </Box>
        )
      },
    },
  ]

  const invitationRows = invitations.data?.invitations
  const memberRows = members.data
  const invitationsPending = invitations.isPending && invitationRows === undefined
  const membersPending = members.isPending && memberRows === undefined

  // The value the issued block hands over: the host's absolute acceptance
  // link when it supplies a builder, the bare token otherwise.
  const issuedValue =
    issued === null
      ? ''
      : invitationLink === undefined
        ? issued.token
        : acceptanceUrl(invitationLink(issued.token))

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
        <TenantScope
          value={tenant}
          onSelect={changeTenant}
          disabled={invitationFeedback.pending || membersFeedback.pending}
        />
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
                disabled={
                  invitationFeedback.pending || email.trim() === ''
                }
                loading={invitationFeedback.pending}
                onClick={issue}
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
                <Box
                  sx={{
                    display: 'flex',
                    flexWrap: 'wrap',
                    alignItems: 'center',
                    columnGap: 1,
                    rowGap: 0.5,
                  }}
                >
                  <Typography
                    variant="body2"
                    sx={{ wordBreak: 'break-all', fontFamily: 'monospace' }}
                  >
                    {issuedValue}
                  </Typography>
                  <CopyControl
                    key={issuedValue}
                    value={issuedValue}
                    label={
                      invitationLink === undefined
                        ? t('invitations.issued.copyToken')
                        : t('invitations.issued.copyLink')
                    }
                  />
                </Box>
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
            <Box ref={invitationRegionRef}>
              <WriteFeedbackRegion outcome={invitationFeedback.outcome} />
            </Box>
          </Box>

          <Box component="section" sx={{ mb: 3 }}>
            <Typography
              component="h2"
              variant="h2"
              sx={{ fontSize: '1.25rem', mb: 1 }}
            >
              {t('members.title')}
            </Typography>
            <MemberWriteForm tenant={tenant} feedback={membersFeedback} />
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
            <Box ref={membersRegionRef}>
              <WriteFeedbackRegion outcome={membersFeedback.outcome} />
            </Box>
          </Box>
        </>
      )}

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
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

/** How long the copy control keeps its copied state and announcement. */
const COPY_FEEDBACK_MS = 2000

/**
 * The copy control for the one-time hand-off value: it writes the complete
 * value -- the acceptance link or the bare token -- to the clipboard and
 * announces the copy, so the operator hands over the exact value rather
 * than a truncated one. The stable `label` is the control's accessible
 * name. A context without the Clipboard API leaves the control silent
 * rather than failing the page.
 */
function CopyControl({
  value,
  label,
}: {
  readonly value: string
  readonly label: string
}) {
  const { t } = useConsoleUiTranslation()
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!copied) return undefined
    const timer = window.setTimeout(() => setCopied(false), COPY_FEEDBACK_MS)
    return () => window.clearTimeout(timer)
  }, [copied])

  const copy = (): void => {
    const clipboard: Clipboard | undefined = navigator.clipboard
    if (clipboard === undefined) return
    void clipboard.writeText(value).then(
      () => setCopied(true),
      () => undefined,
    )
  }

  return (
    <Box sx={{ display: 'inline-flex', alignItems: 'center', columnGap: 1 }}>
      <Button
        type="button"
        size="small"
        variant="outlined"
        aria-label={label}
        onClick={copy}
      >
        {t('invitations.issued.copy')}
      </Button>
      <Typography role="status" variant="body2" color="text.secondary">
        {copied ? t('invitations.issued.copied') : ''}
      </Typography>
    </Box>
  )
}

/**
 * MemberWriteForm: the membership-write control (PUT
 * /auth/admin/tenants/:tenant/members/:account) -- it adds, suspends or
 * resumes a membership, naming the account chosen through the account
 * search picker, its active state, an optional source and the SSO
 * exemption. A landed write clears the account and source and resets the
 * flags; a refused one keeps them to correct. The write reports through the
 * member section's scope, and a landed one names the account it changed.
 */
function MemberWriteForm({
  tenant,
  feedback,
}: {
  readonly tenant: string
  readonly feedback: WriteFeedback
}) {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const writeMember = operations.useWriteMember()

  const [account, setAccount] = useState<ConsoleAccount | null>(null)
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
        <AccountSelector
          label={t('members.write.accountLabel')}
          value={account}
          onChange={setAccount}
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
          disabled={feedback.pending || account === null}
          loading={feedback.pending}
          onClick={() => {
            if (account === null) {
              return
            }
            const target = account
            const writtenSource = source
            void feedback.run(async () => {
              await writeMember.mutateAsync({
                tenant,
                account: target.id,
                active,
                ...(writtenSource === '' ? {} : { source: writtenSource }),
                sso_exempt: ssoExempt,
              })
              setAccount(null)
              setSource('')
              setActive(true)
              setSsoExempt(false)
              return t('members.write.saved', {
                account:
                  target.display_name === '' ? target.id : target.display_name,
              })
            })
          }}
        >
          {t('members.write.submit')}
        </Button>
      </Box>
    </Box>
  )
}
