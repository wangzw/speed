/**
 * InvitationsPage: the tenant invitation and membership page of the
 * console.
 *
 * Both surfaces are tenant-scoped (/api/v1/tenants/:tenant/...), so
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
 * not accept. The acceptance value is handed out once: it is displayed in a
 * page-held panel beside the invitation group's region, named for the tenant
 * and the invitee and closed only by its explicit control, so a scope reset
 * or a later failure cannot drop the only copy. Every read and write is an
 * operation on the console operations bundle, so the page builds no client
 * and writes no query key.
 *
 * The two titled sections are the page's two feedback scopes: the
 * invitation list (issue and revoke) reports under its table, and the
 * member list (write, remove and SSO exemption) reports under its own.
 * A refusal marks the affected row inline with the same resolved text the
 * region shows and brings the region into view; a success names the
 * invitee or account it changed. The tenant seeds every write, so changing
 * it resets both scopes and disables the selector while either is in
 * flight. The page opens with ui-kit's PageHeader.
 *
 * The tenant scope is optionally controlled: `tenant` locks the page to the
 * given value and hides the selector, `defaultTenant` seeds the page's own
 * selection ahead of the URL's `tenant` parameter, and `onTenantChange`
 * reports each re-selection. Given none of them the page opens on the URL's
 * parameter and the selector changes it, as before.
 *
 * The member table's SSO-exemption column, its toggle and the membership
 * form's exemption checkbox render only where `authn.admin.sso` is present.
 */

import { useCallback, useEffect, useState } from 'react'
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
  LONG_TEXT_COLUMN_MAX_WIDTH,
  OneTimeValuePanel,
  PageHeader,
  PRIMARY_COLUMN_MAX_WIDTH,
  SectionCard,
  useWriteFeedback,
} from '@speed/ui-kit'
import type { DataTableColumn, WriteFeedback } from '@speed/ui-kit'
import { AccountIdentity, useAccountIdentity } from './internal/account-identity.js'
import { AccountSelector } from './internal/account-selector.js'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { formatInstant } from '@speed/i18n'
import { useConsoleUiErrorText } from './internal/error-text.js'
import { useConsoleHasFeature } from './internal/features.js'
import { useConsoleInvitationLink } from './internal/invitation-link.js'
import { RefreshButton } from '@speed/ui-kit'
import { CONSOLE_UI_NAMESPACE } from './resources.js'
import { TenantScope, tenantFromLocation } from './internal/tenant-scope.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import {
  pageRefreshRead,
  usePageRefresh,
} from '@speed/ui-kit'
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

/**
 * The id of the page's own "choose a tenant first" text. The disabled
 * refresh is described by it, so the explanation has one copy on the page.
 */
const TENANT_HINT_ID = 'invitations-tenant-hint'

/** The invitation just issued, with the tenant it was issued under. */
type IssuedInvitation = ConsoleIssuedInvitation & { readonly tenant: string }

/** The invitation status's label, guarding the closed set. */
const INVITATION_STATUSES = new Set<string>(['pending', 'accepted', 'revoked'])

/**
 * The member sources the membership surface writes by name. A `jit:<id>`
 * value names the provider a just-in-time membership came from.
 */
const MEMBER_SOURCES = new Set<string>(['admin', 'invite'])
const JIT_SOURCE_PREFIX = 'jit:'

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

export interface InvitationsPageProps {
  /**
   * A controlled tenant scope: when given, the page's reads and writes name
   * this tenant and the selector is not rendered. An empty string is a
   * controlled "no tenant".
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

export function InvitationsPage({
  tenant: controlledTenant,
  defaultTenant,
  onTenantChange,
}: InvitationsPageProps = {}) {
  const { t, i18n } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const invitationLink = useConsoleInvitationLink()
  const hasFeature = useConsoleHasFeature()
  // The member table's exemption column, toggle and the write form's
  // checkbox exist only where the SSO surface does.
  const ssoExemption = hasFeature('authn.admin.sso')

  const [innerTenant, setInnerTenant] = useState(
    () => defaultTenant ?? tenantFromLocation(),
  )
  const tenant = controlledTenant ?? innerTenant
  const tenantControlled = controlledTenant !== undefined
  const [email, setEmail] = useState('')
  const [expiresAt, setExpiresAt] = useState('')
  // The invitation just issued, held with the tenant it was issued under so
  // its acceptance token -- handed out once -- can be shown as the invitee's
  // link rather than dropped, and named for that tenant.
  const [issued, setIssued] = useState<IssuedInvitation | null>(null)
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
  const resetPageFeedback = useCallback(() => {
    invitationFeedback.reset()
    membersFeedback.reset()
    setFailedInvitation(null)
    setFailedMember(null)
  }, [invitationFeedback.reset, membersFeedback.reset])
  useEffect(() => {
    resetPageFeedback()
  }, [tenant, resetPageFeedback])
  const changeTenant = (next: string) => {
    setInnerTenant(next)
    onTenantChange?.(next)
  }

  // The refresh action re-reads both body reads -- the invitation list and
  // the member list -- and resets both scopes and row markers first. The
  // one-time acceptance panel belongs to the page, not a slot, and stays.
  const pageRefresh = usePageRefresh(
    [
      pageRefreshRead('invitations', invitations),
      pageRefreshRead('members', members),
    ],
    resetPageFeedback,
  )

  const issue = () => {
    const address = email
    void invitationFeedback.run(async () => {
      const answer = await issueInvitation.mutateAsync({
        tenant,
        email: address,
        expiresAt: toInviteExpiry(expiresAt),
      })
      setEmail('')
      setIssued({ ...answer, tenant })
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

  const requestRemoveMember = (
    member: ConsoleMember,
    displayName: string | undefined,
  ) => {
    confirm.ask({
      title: t('members.removeTitle'),
      // The confirmation names the member the roster names it: by display
      // name. The account id is a machine value the operator cannot match
      // to a row, so an unresolved identity (a pending, refused, missing or
      // feature-gated account read) keeps the sentence neutral rather than
      // falling back to it.
      message:
        displayName === undefined
          ? t('members.removeMessageUnnamed')
          : t('members.removeMessage', { name: displayName }),
      confirmLabel: t('common.confirm'),
      run: () => {
        setFailedMember(null)
        void membersFeedback.run(async () => {
          try {
            await removeMember.mutateAsync({
              tenant,
              account: member.account,
            })
            return t('members.removed', {
              account: displayName ?? member.account,
            })
          } catch (error) {
            setFailedMember({ account: member.account, action: 'remove' })
            throw error
          }
        })
      },
    })
  }

  const toggleSsoExempt = (
    member: ConsoleMember,
    accountName: string,
  ) => {
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
          { account: accountName },
        )
      } catch (error) {
        setFailedMember({ account: member.account, action: 'sso' })
        throw error
      }
    })
  }

  // A source the surface names renders its own label; a `jit:<id>` value
  // names the provider that provisioned the membership; anything else is
  // shown as the record carries it.
  const memberSourceLabel = (source: string): string => {
    let key: string | null = null
    let options: Record<string, string> | undefined
    if (source.startsWith(JIT_SOURCE_PREFIX)) {
      key = 'members.source.jit'
      options = { provider: source.slice(JIT_SOURCE_PREFIX.length) }
    } else if (MEMBER_SOURCES.has(source)) {
      key = `members.source.${source}`
    }
    return key !== null &&
      i18n.exists(key, { ns: CONSOLE_UI_NAMESPACE })
      ? t(key, options)
      : source
  }

  const invitationColumns: DataTableColumn<ConsoleInvitation>[] = [
    {
      id: 'email',
      header: t('invitations.column.email'),
      cellWrap: 'nowrap',
      cellFullValue: (invitation) => invitation.email,
      // The invitation's primary identity: never hidden, and the shared
      // whole-cell cap -- which counts the copy control -- keeps the row
      // actions on screen.
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (invitation) => (
        <Typography variant="body2" noWrap>
          {invitation.email}
        </Typography>
      ),
    },
    {
      id: 'status',
      header: t('invitations.column.status'),
      priority: 'high',
      cellWrap: 'nowrap',
      cell: (invitation) => statusLabel(invitation.status, t),
    },
    {
      id: 'invitedBy',
      header: t('invitations.column.invitedBy'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cellFullValue: (invitation) => invitation.invited_by,
      contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
      cell: (invitation) => (
        <AccountIdentity account={invitation.invited_by} />
      ),
    },
    {
      id: 'expires',
      header: t('invitations.column.expires'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (invitation) =>
        formatInstant(invitation.expires_at, i18n.language, {
          timeZone: 'UTC',
        }),
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
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (member) => <AccountIdentity account={member.account} />,
    },
    {
      id: 'active',
      header: t('members.column.active'),
      priority: 'high',
      cellWrap: 'nowrap',
      cell: (member) =>
        member.active ? t('members.activeYes') : t('members.activeNo'),
    },
    ...(ssoExemption
      ? [
          {
            id: 'ssoExempt',
            header: t('members.column.ssoExempt'),
            priority: 'low',
            cellWrap: 'nowrap',
            cell: (member: ConsoleMember) =>
              member.sso_exempt
                ? t('members.ssoExemptYes')
                : t('members.ssoExemptNo'),
          } satisfies DataTableColumn<ConsoleMember>,
        ]
      : []),
    {
      id: 'source',
      header: t('members.column.source'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (member) => memberSourceLabel(member.source),
    },
    {
      id: 'actions',
      header: t('members.column.actions'),
      cellWrap: 'nowrap',
      cell: (member) => (
        <MemberRowActions
          member={member}
          failed={
            failedMember?.account === member.account
              ? failedMember.action
              : null
          }
          failureText={memberFailureText}
          pending={membersFeedback.pending}
          ssoExemption={ssoExemption}
          onRemove={requestRemoveMember}
          onToggleSsoExempt={toggleSsoExempt}
        />
      ),
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
        sx={{ mb: 2 }}
      />

      <SectionCard sx={{ mb: 3 }}>
        {tenantControlled ? (
          <RefreshButton
            busy={pageRefresh.busy}
            disabled={tenant === ''}
            writePending={
              invitationFeedback.pending || membersFeedback.pending
            }
            describedBy={TENANT_HINT_ID}
            disabledReason={t('invitations.needTenant.description')}
            label={t('common.refresh')}
            onClick={pageRefresh.refresh}
          />
        ) : (
          <TenantScope
            value={tenant}
            onSelect={changeTenant}
            disabled={invitationFeedback.pending || membersFeedback.pending}
            trailing={
              <RefreshButton
                busy={pageRefresh.busy}
                disabled={tenant === ''}
                writePending={
                  invitationFeedback.pending || membersFeedback.pending
                }
                describedBy={TENANT_HINT_ID}
                disabledReason={t('invitations.needTenant.description')}
                label={t('common.refresh')}
                onClick={pageRefresh.refresh}
              />
            }
          />
        )}
      </SectionCard>

      {tenant === '' ? (
        <EmptyState
          headingLevel="h2"
          title={t('invitations.needTenant.title')}
          description={
            <span id={TENANT_HINT_ID}>
              {t('invitations.needTenant.description')}
            </span>
          }
        />
      ) : (
        <>
          <SectionCard title={t('invitations.section.title')} sx={{ mb: 3 }}>
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
              <InvitationLinkNotice
                key={issuedValue}
                notice={issued}
                value={issuedValue}
                hasBuilder={invitationLink !== undefined}
                onClose={() => setIssued(null)}
              />
            )}
            <ConsoleAsyncSection
              pending={invitationsPending}
              payload={invitationRows}
              error={invitations.error}
              refreshing={invitations.isFetching}
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
          </SectionCard>

          <SectionCard title={t('members.title')} sx={{ mb: 3 }}>
            <MemberWriteForm
              tenant={tenant}
              feedback={membersFeedback}
              ssoExemption={ssoExemption}
            />
            <ConsoleAsyncSection
              pending={membersPending}
              payload={memberRows}
              error={members.error}
              refreshing={members.isFetching}
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
          </SectionCard>
        </>
      )}

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}

/**
 * The member row's own actions. The account's readable identity comes from
 * the same account read the row's identity cell renders (one cached read
 * per account), so a landed write names the account the way the row shows
 * it and falls back to the recorded identifier when the read is refused.
 * The removal confirmation takes only the resolved display name: a neutral
 * sentence stands in when the identity is unresolved, so the recorded
 * identifier never appears in its copy.
 */
function MemberRowActions({
  member,
  failed,
  failureText,
  pending,
  ssoExemption,
  onRemove,
  onToggleSsoExempt,
}: {
  readonly member: ConsoleMember
  readonly failed: 'remove' | 'sso' | null
  readonly failureText: string | null
  readonly pending: boolean
  /** Whether the SSO surface is present, so the toggle exists. */
  readonly ssoExemption: boolean
  readonly onRemove: (
    member: ConsoleMember,
    displayName: string | undefined,
  ) => void
  readonly onToggleSsoExempt: (
    member: ConsoleMember,
    accountName: string,
  ) => void
}) {
  const { t } = useConsoleUiTranslation()
  const { name } = useAccountIdentity(member.account)
  // A landed write names the account by display name, falling back to the
  // recorded identifier when the read resolves none.
  const namedAccount = name ?? member.account
  const errorId = `member-error-${encodeURIComponent(member.account)}`
  const ssoFailed = failed === 'sso'
  const removeFailed = failed === 'remove'
  return (
    <Box>
      <Box sx={{ display: 'flex', columnGap: 1 }}>
        {ssoExemption && (
          <Button
            type="button"
            size="small"
            variant="outlined"
            aria-invalid={ssoFailed}
            aria-describedby={ssoFailed ? errorId : undefined}
            disabled={pending}
            loading={pending}
            onClick={() => onToggleSsoExempt(member, namedAccount)}
          >
            {member.sso_exempt
              ? t('members.ssoExemptClear')
              : t('members.ssoExemptSet')}
          </Button>
        )}
        <Button
          type="button"
          size="small"
          variant="outlined"
          color="error"
          aria-invalid={removeFailed}
          aria-describedby={removeFailed ? errorId : undefined}
          disabled={pending}
          loading={pending}
          onClick={() => onRemove(member, name)}
        >
          {t('members.remove')}
        </Button>
      </Box>
      {failureText !== null && failed !== null && (
        <Typography
          id={errorId}
          variant="caption"
          color="error"
          sx={{ display: 'block', mt: 0.5 }}
        >
          {failureText}
        </Typography>
      )}
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

/**
 * InvitationLinkNotice: the display of a just-issued invitation's acceptance
 * value -- the host-built link, or the bare token when the host supplies no
 * builder -- a value handed out once.
 *
 * Its state is the page's (`InvitationsPage` holds it beside the tenant it
 * was issued under), so a scope reset, a later failure or a context change
 * cannot drop the only copy; a second issuance in the same group replaces
 * it. The panel is one polite live region with an explicit close control
 * whose accessible name names the tenant and the invitee, and its copy
 * control hands over the complete value.
 */
function InvitationLinkNotice({
  notice,
  value,
  hasBuilder,
  onClose,
}: {
  readonly notice: IssuedInvitation
  readonly value: string
  readonly hasBuilder: boolean
  readonly onClose: () => void
}) {
  const { t } = useConsoleUiTranslation()
  return (
    <OneTimeValuePanel
      message={
        hasBuilder
          ? t('invitations.issued.link', { email: notice.invitation.email })
          : t('invitations.issued.token', { email: notice.invitation.email })
      }
      value={value}
      copyLabel={
        hasBuilder
          ? t('invitations.issued.copyLink')
          : t('invitations.issued.copyToken')
      }
      closeLabel={t('invitations.issued.close')}
      closeAriaLabel={t('invitations.issued.closeAria', {
        tenant: notice.tenant,
        email: notice.invitation.email,
      })}
      onClose={onClose}
    />
  )
}

/**
 * MemberWriteForm: the membership-write control (PUT
 * /api/v1/tenants/:tenant/members/:account) -- it adds, suspends or
 * resumes a membership, naming the account chosen through the account
 * search picker, its active state, an optional source and, where the SSO
 * surface is present, the SSO exemption. A landed write clears the account
 * and source and resets the flags; a refused one keeps them to correct. The
 * write reports through the member section's scope, and a landed one names
 * the account it changed.
 */
function MemberWriteForm({
  tenant,
  feedback,
  ssoExemption,
}: {
  readonly tenant: string
  readonly feedback: WriteFeedback
  /** Whether the SSO surface is present, so the exemption is writable. */
  readonly ssoExemption: boolean
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
        {ssoExemption && (
          <FormControlLabel
            control={
              <Checkbox
                checked={ssoExempt}
                onChange={(event) => setSsoExempt(event.target.checked)}
              />
            }
            label={t('members.write.ssoExemptLabel')}
          />
        )}
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
                ...(ssoExemption ? { sso_exempt: ssoExempt } : {}),
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
