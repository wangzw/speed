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
 *  - the tenant's members: list them (display name, account, active, SSO
 *    exemption, source) and remove a membership.
 *
 * A member row renders from the member read on both surfaces: the display
 * name column and the login username column carry that row's `display_name`
 * and `username`, and the row's actions name the member the same way, so a
 * member row never issues a platform account read. The inviter renders through
 * the shared account identity unit on the platform surface -- the readable
 * display name and username where the account read answers, the neutral copy
 * with an unresolved note where it is refused -- and from the tenant's own
 * member row on the organization surface (`tenantMode`). That surface also
 * omits the member write form, whose account field searches the platform
 * roster. No column offers the recorded account identifier as its copy or
 * hover value.
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
 * flight. A request the tenant admission layer refuses because the tenant
 * requires enterprise single sign-on renders its resolved sentence with a
 * re-login exit: a refused write shows it under the region, and the
 * invitation list's failed read shows it in place of the retryable
 * placeholder (a retry cannot clear the refusal while the session is a
 * local one). The exit ends the session so the host's session gate returns
 * to its sign-in surface, where the operator signs in through the tenant's
 * provider. No local factor can clear that refusal, so the page opens no
 * step-up dialog and lists no factor. The page opens with ui-kit's
 * PageHeader.
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
  PRIMARY_COLUMN_MAX_WIDTH,
  SectionCard,
  useFormatInstant,
  useWriteFeedback,
} from '@speed/ui-kit'
import type { DataTableColumn, WriteFeedback } from '@speed/ui-kit'
import {
  AccountIdentity,
  accountIdentityValue,
  accountNotice,
  TenantAccountIdentity,
  type AccountIdentityValue,
} from './internal/account-identity.js'
import { AccountSelector } from './internal/account-selector.js'
import { AdminAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { CursorPager } from './internal/cursor-pager.js'
import {
  invitationStatusOf,
  type InvitationStatus,
} from './internal/invitation-status.js'
import { useAdminUiErrorText } from './internal/error-text.js'
import { useAdminHasFeature } from './internal/features.js'
import { useAdminInvitationLink } from './internal/invitation-link.js'
import { RefreshButton } from '@speed/ui-kit'
import { ADMIN_UI_NAMESPACE } from './resources.js'
import { useAdminSession } from './internal/session.js'
import { TenantScope, tenantFromLocation } from './internal/tenant-scope.js'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  pageRefreshRead,
  usePageRefresh,
} from '@speed/ui-kit'
import {
  failureTextOf,
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { useAdminOperations } from './operations.js'
import { AdminPageHeader } from './internal/page-header.js'
import type {
  AdminAccount,
  AdminInvitation,
  AdminIssuedInvitation,
  AdminMember,
} from './operations.js'

/**
 * The id of the page's own "choose a tenant first" text. The disabled
 * refresh is described by it, so the explanation has one copy on the page.
 */
const TENANT_HINT_ID = 'invitations-tenant-hint'

/**
 * The member table's page size. The membership read answers the cursor of the
 * page that follows, so a full page is not mistaken for the last one and the
 * page never reads a probe row of its own.
 */
const MEMBER_PAGE_SIZE = 100

/** No member names resolved yet, for a tenant the page has read no page of. */
const EMPTY_MEMBER_IDENTITIES: ReadonlyMap<string, AdminMember> = new Map()

/** The invitation just issued, with the tenant it was issued under. */
type IssuedInvitation = AdminIssuedInvitation & { readonly tenant: string }

/**
 * The member sources the membership surface writes by name. A `jit:<id>`
 * value names the provider a just-in-time membership came from.
 */
const MEMBER_SOURCES = new Set<string>(['admin', 'initial', 'invite'])
const JIT_SOURCE_PREFIX = 'jit:'

/**
 * The invitation status's label. Expiry is derived from the deadline (the
 * stored state still reads pending), so the shared helper decides the fourth
 * label; a stored value outside the closed set renders the generic one.
 */
function statusLabel(status: InvitationStatus, t: (key: string) => string): string {
  return t(`invitations.status.${status}`)
}

/** The status a row displays, derived once per render from its deadline. */
function displayedStatus(invitation: AdminInvitation): InvitationStatus {
  return invitationStatusOf(invitation)
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
  /**
   * Tenant mode: the organization surface's configuration of the same page.
   * The member write form is not rendered -- its account field searches the
   * platform account roster -- and the inviter and member identities resolve
   * from the tenant's own member read (`display_name`) instead of the
   * platform account read. The page therefore issues no `/api/v1/system`
   * request. The tenant is still the controlled prop the wrapper supplies.
   */
  readonly tenantMode?: boolean
}

export function InvitationsPage({
  tenant: controlledTenant,
  defaultTenant,
  onTenantChange,
  tenantMode = false,
}: InvitationsPageProps = {}) {
  const { t, i18n } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const formatTime = useFormatInstant()
  const session = useAdminSession()
  const invitationLink = useAdminInvitationLink()
  const hasFeature = useAdminHasFeature()
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
  // The member table pages by cursor: the cursors this page has collected,
  // the index it is on, and the account identifier of the page's last row as
  // the cursor of the next. The stack is tagged with the tenant it was read
  // for, and a frame for another tenant derives as empty synchronously, so the
  // first read after a switch carries no cursor even before the reset lands.
  const [memberCursorState, setMemberCursorState] = useState<{
    readonly tenant: string
    readonly cursors: readonly string[]
    readonly page: number
  }>(() => ({ tenant, cursors: [''], page: 0 }))
  const memberCursorFrame =
    memberCursorState.tenant === tenant
      ? memberCursorState
      : { tenant, cursors: [''], page: 0 }
  const memberPage = memberCursorFrame.page
  const memberCursor = memberCursorFrame.cursors[memberPage] ?? ''
  // The invitation just issued, held with the tenant it was issued under so
  // its acceptance token -- handed out once -- can be shown as the invitee's
  // link rather than dropped, and named for that tenant.
  const [issued, setIssued] = useState<IssuedInvitation | null>(null)
  const confirm = usePendingConfirm()

  const invitations = operations.useInvitations(tenant)
  const members = operations.useMembers(tenant, {
    limit: MEMBER_PAGE_SIZE,
    ...(memberCursor === '' ? {} : { after: memberCursor }),
  })
  // The member read answers the page envelope; the cursor beside the rows is
  // what says another page follows.
  const memberRows = members.data?.members
  // Tenant mode resolves an account's identity from the member rows this
  // page has read. An account the read has not settled on, was refused, or
  // does not carry is unresolved -- rendered by the neutral copy -- rather
  // than looked up through the platform account read whose access the
  // organization surface does not have. Identities accumulate
  // across the member pages the operator visits, so an inviter resolved on an
  // earlier page stays named after paging past it.
  const [memberIdentitiesByTenant, setMemberIdentitiesByTenant] = useState<
    Readonly<Record<string, ReadonlyMap<string, AdminMember>>>
  >({})
  const memberIdentities =
    memberIdentitiesByTenant[tenant] ?? EMPTY_MEMBER_IDENTITIES
  useEffect(() => {
    if (memberRows === undefined) {
      return
    }
    setMemberIdentitiesByTenant((previous) => {
      const current = previous[tenant] ?? EMPTY_MEMBER_IDENTITIES
      const next = new Map(current)
      let changed = false
      for (const row of memberRows) {
        const recorded = next.get(row.account)
        if (
          recorded === undefined ||
          recorded.display_name !== row.display_name ||
          recorded.username !== row.username
        ) {
          next.set(row.account, row)
          changed = true
        }
      }
      return changed ? { ...previous, [tenant]: next } : previous
    })
  }, [memberRows, tenant])
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
  const resolveErrorText = useAdminUiErrorText()
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
  // The cursor stack carries its tenant, so a switch already frames the first
  // read as empty; this reset then drops the previous tenant's stack after the
  // commit. Both matter: the derivation keeps the first post-switch read from
  // carrying the old cursor, and the reset keeps a later switch back from
  // resuming a page the operator left long ago. The refresh action keeps the
  // current page, so the reset follows the tenant alone.
  useEffect(() => {
    setMemberCursorState({ tenant, cursors: [''], page: 0 })
  }, [tenant])
  const changeTenant = (next: string) => {
    setInnerTenant(next)
    onTenantChange?.(next)
  }

  // The tenant admission layer's SSO refusal cannot be cleared by any local
  // factor: the exit ends the session so the host's session gate returns to
  // its sign-in surface, where the operator signs in through the tenant's
  // provider. Without a session there is no exit to offer.
  const relogin = useCallback(() => {
    void session?.logout().catch(() => undefined)
  }, [session])

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
    // The issue and the revoke share the invitation scope: this attempt
    // starts from a clean result, so the refused row's marker and its inline
    // reason do not stand beside the new answer.
    setFailedInvitation(null)
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

  const requestRevoke = (invitation: AdminInvitation) => {
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
    member: AdminMember,
    identity: AccountIdentityValue,
  ) => {
    confirm.ask({
      title: t('members.removeTitle'),
      // The confirmation names the member the way the roster does: by
      // display name and, when the account view carries one, its login
      // username. An empty display name is named by that username alone.
      // The account id is a machine value the operator cannot match to a row,
      // so an identity with neither name nor username (a pending, refused,
      // missing or feature-gated account read) keeps the sentence neutral
      // rather than falling back to it.
      message:
        identity.name === undefined
          ? identity.username === undefined
            ? t('members.removeMessageUnnamed')
            : t('members.removeMessageByIdentifier', {
                identifier: identity.username,
              })
          : identity.username === undefined
            ? t('members.removeMessage', { name: identity.name })
            : t('members.removeMessageWithIdentifier', {
                name: identity.name,
                identifier: identity.username,
              }),
      confirmLabel: t('common.confirm'),
      run: () => {
        setFailedMember(null)
        void membersFeedback.run(async () => {
          try {
            await removeMember.mutateAsync({
              tenant,
              account: member.account,
            })
            return accountNotice(
              t,
              {
                named: 'members.removed',
                unnamed: 'members.removedUnnamed',
              },
              identity,
            )
          } catch (error) {
            setFailedMember({ account: member.account, action: 'remove' })
            throw error
          }
        })
      },
    })
  }

  const toggleSsoExempt = (
    member: AdminMember,
    identity: AccountIdentityValue,
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
        return accountNotice(
          t,
          exempt
            ? {
                named: 'members.ssoExemptSaved',
                unnamed: 'members.ssoExemptSavedUnnamed',
              }
            : {
                named: 'members.ssoExemptCleared',
                unnamed: 'members.ssoExemptClearedUnnamed',
              },
          identity,
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
      i18n.exists(key, { ns: ADMIN_UI_NAMESPACE })
      ? options === undefined
        ? t(key)
        : t(key, options)
      : source
  }

  const invitationColumns: DataTableColumn<AdminInvitation>[] = [
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
      cell: (invitation) => statusLabel(displayedStatus(invitation), t),
    },
    {
      id: 'invitedBy',
      header: t('invitations.column.invitedBy'),
      priority: 'medium',
      cellWrap: 'nowrap',
      contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
      cell: (invitation) => {
        const inviter = memberIdentities.get(invitation.invited_by)
        return tenantMode ? (
          <TenantAccountIdentity
            name={inviter?.display_name}
            username={inviter?.username}
          />
        ) : (
          <AccountIdentity account={invitation.invited_by} />
        )
      },
    },
    {
      id: 'expires',
      header: t('invitations.column.expires'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (invitation) => formatTime(invitation.expires_at),
    },
    {
      id: 'actions',
      align: 'right',
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

  const memberColumns: DataTableColumn<AdminMember>[] = [
    {
      id: 'displayName',
      header: t('members.column.displayName'),
      cellWrap: 'nowrap',
      cellFullValue: (member) =>
        member.display_name === '' ? undefined : member.display_name,
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      // The member read carries the account's display name, so the name is the
      // row's primary identity without any platform account read. An account
      // with no display name leaves this cell empty: the account model defines
      // no fallback for the name, and the username column beside it still
      // names the row.
      cell: (member) => member.display_name,
    },
    {
      id: 'username',
      header: t('members.column.username'),
      // The display name beside it is the page's primary identity, so the
      // composed identity is the secondary one: it sheds below the sm floor,
      // where the primary cell and the row actions stay on screen, and
      // remains visible at every tier floor from sm up.
      priority: 'high',
      cellWrap: 'nowrap',
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      // Both surfaces show the member record's login username beside its
      // display name column, and the header names exactly that. Neither shows
      // or copies the account id, and no account read is needed: the member
      // read carries the username. A record with no username gets the neutral
      // copy, never the display name repeated from the column beside it.
      cell: (member) =>
        member.username === '' ? (
          <Typography variant="body2">
            {t('common.accountUnavailable')}
          </Typography>
        ) : (
          member.username
        ),
    },
    {
      id: 'active',
      header: t('members.column.active'),
      // The least important of the columns sm first shows: the name, the
      // account and the row actions fit the sm floor together, so the status
      // yields at that tier and returns from md up.
      priority: 'medium',
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
            cell: (member: AdminMember) =>
              member.sso_exempt
                ? t('members.ssoExemptYes')
                : t('members.ssoExemptNo'),
          } satisfies DataTableColumn<AdminMember>,
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
      align: 'right',
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
  const invitationsPending = invitations.isPending && invitationRows === undefined
  const membersPending = members.isPending && memberRows === undefined
  // The server's next cursor is the only evidence another page follows; an
  // exact-multiple last page reports none, so it never reads an empty page.
  const memberNextCursor = members.data?.next_cursor ?? ''
  const memberHasNext = memberNextCursor !== ''
  // `keepPreviousData` leaves the previous page's envelope on screen while
  // the next one loads, so its `next_cursor` still names that same page.
  // Reading it again would push the same cursor twice; the control waits for
  // the read to settle (and the pager is disabled while it runs).
  const nextMemberPage = () => {
    if (!memberHasNext || memberNextCursor === '' || members.isFetching) {
      return
    }
    setMemberCursorState((previous) => {
      const current =
        previous.tenant === tenant
          ? previous
          : { tenant, cursors: [''], page: 0 }
      const grown = [...current.cursors]
      grown[current.page + 1] = memberNextCursor
      return { tenant, cursors: grown, page: current.page + 1 }
    })
  }
  const previousMemberPage = () => {
    if (members.isFetching) {
      return
    }
    setMemberCursorState((previous) => {
      const current =
        previous.tenant === tenant
          ? previous
          : { tenant, cursors: [''], page: 0 }
      return current.page === 0
        ? current
        : { ...current, page: current.page - 1 }
    })
  }

  // The value the issued block hands over: the host's absolute acceptance
  // link when it supplies a builder, the bare token otherwise. A notice whose
  // delivery succeeded carries no token -- the recipient already holds the
  // link -- so there is nothing to hand over and no control that would copy
  // an empty value.
  const issuedToken = issued?.token
  const issuedValue =
    issuedToken === undefined
      ? ''
      : invitationLink === undefined
        ? issuedToken
        : acceptanceUrl(invitationLink(issuedToken))

  return (
    <Box>
      <AdminPageHeader
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
                key={issuedValue === '' ? issued.invitation.id : issuedValue}
                notice={issued}
                value={issuedValue}
                hasBuilder={invitationLink !== undefined}
                onClose={() => setIssued(null)}
              />
            )}
            <AdminAsyncSection
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
              ssoRelogin={session === null ? undefined : relogin}
              mfaRelogin={session === null ? undefined : relogin}
            >
              {(settled) => (
                <DataTable
                  rows={settled}
                  columns={invitationColumns}
                  rowKey={(invitation) => invitation.id}
                />
              )}
            </AdminAsyncSection>
            <Box ref={invitationRegionRef}>
              <WriteFeedbackRegion
                outcome={invitationFeedback.outcome}
                ssoRelogin={session === null ? undefined : relogin}
                mfaRelogin={session === null ? undefined : relogin}
              />
            </Box>
          </SectionCard>

          <SectionCard title={t('members.title')} sx={{ mb: 3 }}>
            {!tenantMode && (
              <MemberWriteForm
                tenant={tenant}
                feedback={membersFeedback}
                ssoExemption={ssoExemption}
              />
            )}
            <AdminAsyncSection
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
              mfaRelogin={session === null ? undefined : relogin}
            >
              {(settled) => (
                <DataTable
                  rows={settled}
                  columns={memberColumns}
                  rowKey={(member) => member.account}
                />
              )}
            </AdminAsyncSection>
            {memberRows !== undefined && (
              <CursorPager
                page={memberPage}
                hasNext={memberHasNext}
                busy={membersFeedback.pending || members.isFetching}
                onPrevious={previousMemberPage}
                onNext={nextMemberPage}
              />
            )}
            <Box ref={membersRegionRef}>
              <WriteFeedbackRegion
                outcome={membersFeedback.outcome}
                ssoRelogin={session === null ? undefined : relogin}
                mfaRelogin={session === null ? undefined : relogin}
              />
            </Box>
          </SectionCard>
        </>
      )}

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}

interface MemberRowActionsProps {
  readonly member: AdminMember
  readonly failed: 'remove' | 'sso' | null
  readonly failureText: string | null
  readonly pending: boolean
  /** Whether the SSO surface is present, so the toggle exists. */
  readonly ssoExemption: boolean
  readonly onRemove: (
    member: AdminMember,
    identity: AccountIdentityValue,
  ) => void
  readonly onToggleSsoExempt: (
    member: AdminMember,
    identity: AccountIdentityValue,
  ) => void
}

/**
 * The member row's own actions. A landed write names the account the way the
 * row shows it -- from the member read, on both surfaces -- and never the
 * recorded identifier as a fallback.
 */
function MemberRowActions(props: MemberRowActionsProps) {
  return (
    <MemberActionsBody {...props} identity={memberRowIdentity(props.member)} />
  )
}

/**
 * The member row's identity: the member read's own display name and login
 * username. Neither is an identifier standing in for the other.
 */
function memberRowIdentity(member: AdminMember): AccountIdentityValue {
  return {
    name: member.display_name === '' ? undefined : member.display_name,
    username: member.username === '' ? undefined : member.username,
  }
}

function MemberActionsBody({
  member,
  failed,
  failureText,
  pending,
  ssoExemption,
  onRemove,
  onToggleSsoExempt,
  identity,
}: MemberRowActionsProps & { readonly identity: AccountIdentityValue }) {
  const { t } = useAdminUiTranslation()
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
            onClick={() => onToggleSsoExempt(member, identity)}
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
          onClick={() => onRemove(member, identity)}
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
 *
 * A notice whose delivery succeeded carries no token: the recipient already
 * holds the link, so the panel says the invitation was emailed and offers no
 * copy control. A failed or unconfigured carrier leaves the token on the
 * answer, and the panel hands it over as before.
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
  const { t } = useAdminUiTranslation()
  const emailed = notice.token === undefined
  return (
    <OneTimeValuePanel
      message={
        emailed ? (
          // The notice reached the address, so the invitation is the email's
          // business: there is no link for the operator to hand over.
          t('invitations.issued.emailed', {
            email: notice.invitation.email,
          })
        ) : (
          <>
            {hasBuilder
              ? t('invitations.issued.link', {
                  email: notice.invitation.email,
                })
              : t('invitations.issued.token', {
                  email: notice.invitation.email,
                })}
            {/* The notice's delivery outcome sits beside the one-time value: a
                failed or unconfigured carrier is why the operator still has to
                hand the link over themselves. */}
            <Typography
              component="span"
              variant="body2"
              color={
                notice.email_delivery === 'failed' ? 'error' : 'text.secondary'
              }
              sx={{ display: 'block', mt: 0.5 }}
            >
              {t(`invitations.issued.delivery.${notice.email_delivery}`)}
            </Typography>
          </>
        )
      }
      {...(emailed
        ? {}
        : {
            value,
            copyLabel: hasBuilder
              ? t('invitations.issued.copyLink')
              : t('invitations.issued.copyToken'),
          })}
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
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const writeMember = operations.useWriteMember()

  const [account, setAccount] = useState<AdminAccount | null>(null)
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
              return accountNotice(
                t,
                {
                  named: 'members.write.saved',
                  unnamed: 'members.write.savedUnnamed',
                },
                accountIdentityValue(target),
              )
            })
          }}
        >
          {t('members.write.submit')}
        </Button>
      </Box>
    </Box>
  )
}
