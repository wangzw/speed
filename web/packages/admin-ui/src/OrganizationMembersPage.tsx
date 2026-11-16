/**
 * OrganizationMembersPage: the organization administrator's members and
 * invitations page.
 *
 * One route, two sections in the order the operator works in: members first
 * (the main task), invitations second. The page is not a tabbed surface.
 *
 * Members: a server-side search over `query` (debounced), a table of display
 * name, username, roles, status, source and joined-at, cursor pagination, and
 * a management panel opened from each row. The panel owns bind/unbind,
 * suspend/resume and remove; its permission degradation and the role
 * escalation check live in `internal/member-panel.tsx`.
 *
 * Invitations: an issue form with a one-day / seven-day / thirty-day expiry
 * choice and, for a caller who may manage roles, a role choice whose grants
 * are checked against the caller's own permission set before the write. The
 * list opens on pending invitations, filters by the server's four-value
 * status, and renders the server-judged `state` rather than deriving expiry
 * from the local clock (`internal/invitation-status.ts`). Terminal rows say
 * when they reached that state; only pending rows offer resend and revoke.
 * The one-time panel is page state, so a scope reset or a later failure
 * cannot drop the only copy; its close control asks for confirmation only
 * when it actually holds a link, and only the invitation that was just
 * issued or resent offers a row copy-link action.
 *
 * Every read and write runs through the organization operations bundle,
 * so the page builds no client and writes no query key.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  ConfirmDialog,
  CopyButton,
  DataTable,
  errorCodeOf,
  ListSkeleton,
  LONG_TEXT_COLUMN_MAX_WIDTH,
  OneTimeValuePanel,
  PRIMARY_COLUMN_MAX_WIDTH,
  SectionCard,
  useFormatInstant,
  useWriteFeedback,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { AdminPageHeader } from './internal/page-header.js'
import { AdminAsyncSection } from './internal/async-section.js'
import { AccountsIcon, InvitationsIcon } from './internal/icons.js'
import { identityLabel } from './internal/account-identity.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { CursorPager } from './internal/cursor-pager.js'
import { INVITATION_IDENTITY_MAX_WIDTH } from './internal/invitation-columns.js'
import { useAdminInvitationLink } from './internal/invitation-link.js'
import {
  INVITATION_STATUS_FILTERS,
  remainingText,
  statusLabel,
  statusTone,
  terminalNote,
  type InvitationStatusFilter,
} from './internal/invitation-status.js'
import { StatusTag } from './internal/status-tag.js'
import { useRoleEscalationCheck } from './internal/role-escalation.js'
import { MemberPanel } from './internal/member-panel.js'
import { RowActionMenu } from './internal/row-action-menu.js'
import { useSectionHeadingFocus } from './internal/section-heading-focus.js'
import { useAdminUiErrorText } from './internal/error-text.js'
import { useAdminSession } from './internal/session.js'
import { useAdminUiTranslation } from './internal/translation.js'
import { ADMIN_UI_NAMESPACE } from './resources.js'
import { useDebouncedValue } from './internal/use-debounced-value.js'
import {
  failureTextOf,
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { useOrganizationAdminOperations } from './organization-operations.js'
import { INVITATION_TERMINAL_CODE } from './operations.js'
import type {
  AdminInvitation,
  AdminIssuedInvitation,
  AdminMember,
} from './operations.js'

/** The member table's page size; the server answers the next page's cursor. */
const MEMBER_PAGE_SIZE = 100

/** The invitation table's page size. */
const INVITATION_PAGE_SIZE = 50

/** The issue form's default expiry, in days (the module's configured default). */
const DEFAULT_EXPIRY_DAYS = 7

/** The expiry choices the issue form offers, in days. */
const EXPIRY_CHOICES = [1, 7, 30] as const

/**
 * The two tables carry five high-priority columns from the `sm` container
 * floor (600px). Their horizontal cell padding is tightened from the
 * ui-kit default so those columns fit that floor instead of falling back
 * to the scroll container; the vertical padding is unchanged.
 */
const COMPACT_TABLE_SX = {
  '& .MuiTableCell-root': { px: 1 },
} as const

/**
 * The content cap the member table's two nowrap identity columns take
 * below `md`. The shared primary cap (`PRIMARY_COLUMN_MAX_WIDTH`) is sized
 * for a primary column whose wrapping text can shrink to its longest word;
 * the display name and the account stay on one line, so each contributes
 * its whole cap to the row's unbreakable width. At the 600px `sm` floor
 * those two caps together with the roles, status, joined and action columns
 * exceed the floor. The status column has no cap of its own and its chip
 * grows with the label -- "Suspended" is the widest state -- so the pair has
 * to fit with that worst-case column as well, not only with "Active". 96px
 * makes the suspended row require 576.8px in the browser tier's pinned font,
 * 23.2px inside the floor; the complete values remain reachable through the
 * title and the copy control.
 * From `md` up the shared 200px cap applies unchanged.
 */
const MEMBER_IDENTITY_MAX_WIDTH = {
  belowMd: 96,
  fromMd: PRIMARY_COLUMN_MAX_WIDTH.fromMd,
} as const

/** The sources the surface names itself; a `jit:<id>` value names a provider. */
const MEMBER_SOURCES = new Set<string>(['admin', 'initial', 'invite'])
const JIT_SOURCE_PREFIX = 'jit:'

/** The invitation just issued or resent, held with the tenant it belongs to. */
interface IssuedNotice {
  readonly answer: AdminIssuedInvitation
  readonly tenant: string
}

/**
 * One list's cursor stack tagged with the tenant it was collected for. A
 * frame for another tenant derives as the first page synchronously, so the
 * first read after a switch carries no cursor from the tenant left behind
 * even before the post-commit reset lands.
 */
interface CursorFrame {
  readonly tenant: string
  readonly cursors: readonly string[]
  readonly page: number
}

function firstCursorPage(tenant: string): CursorFrame {
  return { tenant, cursors: [''], page: 0 }
}

/** The query parameter the members section's role filter reads and writes. */
const ROLE_QUERY_PARAM = 'role'

/** The role name a query string carries, or '' when the parameter is absent. */
function roleFromSearch(search: string): string {
  return new URLSearchParams(search).get(ROLE_QUERY_PARAM)?.trim() ?? ''
}

/** The role filter the page opens on from the current URL; '' outside a
 * browser. The parameter is read once, when the page's state is created. */
function roleFromLocation(): string {
  return typeof window === 'undefined'
    ? ''
    : roleFromSearch(window.location.search)
}

/**
 * Writes the role filter into the current URL so the filtered view is
 * addressable and survives a reload. The role is percent-encoded in place
 * (not form-encoded), so a name carrying a space or an ampersand round-trips
 * as the role itself. Every other query parameter is preserved, and an empty
 * role removes the parameter rather than writing an empty one.
 */
function rememberRoleInLocation(role: string): void {
  if (typeof window === 'undefined') {
    return
  }
  const { pathname, search, hash } = window.location
  const parts: string[] = []
  for (const [key, value] of new URLSearchParams(search)) {
    if (key !== ROLE_QUERY_PARAM) {
      parts.push(`${encodeURIComponent(key)}=${encodeURIComponent(value)}`)
    }
  }
  if (role !== '') {
    parts.push(`${ROLE_QUERY_PARAM}=${encodeURIComponent(role)}`)
  }
  const query = parts.length === 0 ? '' : `?${parts.join('&')}`
  window.history.replaceState({}, '', `${pathname}${query}${hash}`)
}

export interface OrganizationMembersPageProps {
  /** The current tenant every read and write names. */
  readonly tenant: string
}

/** The absolute RFC 3339 instant `days` from now, the issue form's expiry. */
function expiryInstant(days: number): string {
  return new Date(Date.now() + days * 24 * 60 * 60 * 1000).toISOString()
}

/**
 * The absolute acceptance URL for a host-built link; a host-relative path is
 * resolved against the current origin, and a value that will not resolve is
 * shown as it came.
 */
function acceptanceUrl(link: string): string {
  try {
    return new URL(link, window.location.origin).toString()
  } catch {
    return link
  }
}

export function OrganizationMembersPage({
  tenant,
}: OrganizationMembersPageProps) {
  const { t, i18n } = useAdminUiTranslation()
  const operations = useOrganizationAdminOperations()
  // Every invitation instant renders through the one entry, in the viewer's
  // display zone; the pure helpers below take it as an argument.
  const formatTime = useFormatInstant()
  const invitationLink = useAdminInvitationLink()
  const resolveErrorText = useAdminUiErrorText()
  const session = useAdminSession()
  // The tenant admission layer's multi-factor refusal cannot be cleared by
  // any retry: the organization requires a fresh sign-in. The exit ends the
  // session so the host's session gate returns to its sign-in surface.
  // Without a session there is no exit to offer.
  const relogin = useCallback(() => {
    void session?.logout().catch(() => undefined)
  }, [session])

  // --- Members ----------------------------------------------------------

  const [memberSearch, setMemberSearch] = useState('')
  const debouncedMemberSearch = useDebouncedValue(memberSearch)
  // The role filter is named by the URL (`?role=...`) and applied on entry:
  // the state's initial value is the parameter, so the first member read
  // already carries it. An empty value is no filter.
  const [roleFilter, setRoleFilter] = useState<string>(roleFromLocation)
  const tenantRoles = operations.useTenantRoles(tenant)
  const roleOptions = tenantRoles.data ?? []
  const [memberCursorState, setMemberCursorState] = useState<CursorFrame>(() =>
    firstCursorPage(tenant),
  )
  const memberCursorFrame =
    memberCursorState.tenant === tenant
      ? memberCursorState
      : firstCursorPage(tenant)
  const memberPage = memberCursorFrame.page
  const memberCursor = memberCursorFrame.cursors[memberPage] ?? ''

  const memberParams = useMemo(
    () => ({
      limit: MEMBER_PAGE_SIZE,
      ...(debouncedMemberSearch === ''
        ? {}
        : { query: debouncedMemberSearch }),
      // The server filters the page by role; the page never narrows the rows
      // it received, so a filtered page is still a full page of matches.
      ...(roleFilter === '' ? {} : { role: roleFilter }),
      ...(memberCursor === '' ? {} : { after: memberCursor }),
    }),
    [debouncedMemberSearch, roleFilter, memberCursor],
  )
  const members = operations.useMembers(tenant, memberParams)
  const memberRows = members.data?.members
  const memberNextCursor = members.data?.next_cursor ?? ''

  // The tenant is the list's context: a switch already derives the first
  // page, and the post-commit reset drops the previous tenant's stack so a
  // later switch back does not resume a page left long ago. The search
  // resets the stack in the input handler itself, so the debounced value's
  // first read never carries a cursor read under the old search.
  useEffect(() => {
    setMemberCursorState(firstCursorPage(tenant))
  }, [tenant])

  const [panelMember, setPanelMember] = useState<AdminMember | null>(null)
  const [panelOpen, setPanelOpen] = useState(false)
  // The panel belongs to the tenant it was opened under. A tenant switch
  // closes it and drops the snapshot; the derivation below also refuses a
  // snapshot from another tenant, so even the render before this effect
  // commits cannot offer a control whose write would name the new tenant's
  // path and the old tenant's member.
  useEffect(() => {
    setPanelOpen(false)
    setPanelMember(null)
  }, [tenant])
  // The panel acts on the member as the list read now carries it, not on the
  // snapshot taken when the row was clicked: a suspend or resume writes the
  // membership and invalidates the list, and the open panel then follows
  // that read instead of showing the state it opened with. A member no
  // current row carries (a search narrowed the list away) keeps the
  // snapshot, which is the last state read for it.
  const livePanelMember =
    panelMember === null || panelMember.tenant !== tenant
      ? null
      : (memberRows?.find((row) => row.account === panelMember.account) ??
        panelMember)
  // A removal's message is written into the list's scope only after the
  // panel has exited: while the modal is open the page behind it is
  // aria-hidden, so a notice written then would never be announced.
  const removalSuccess = useRef<string | null>(null)

  // The member list's feedback scope holds the panel's removal success; the
  // panel's own writes report in its own scope.
  const membersFeedback = useWriteFeedback()
  const membersRegionRef = useFailureScroll(membersFeedback.outcome)

  const roleFilterActive = roleFilter !== ''
  const roleListed = roleOptions.some((role) => role.role === roleFilter)

  // A filter change is a new first page and a new scope: the cursor stack
  // belonged to the previous filter, and a result written under it would read
  // as describing the filter just left behind.
  const changeRoleFilter = (next: string) => {
    setRoleFilter(next)
    setMemberCursorState(firstCursorPage(tenant))
    membersFeedback.reset()
    rememberRoleInLocation(next)
  }

  const openPanel = (member: AdminMember) => {
    setPanelMember(member)
    setPanelOpen(true)
  }
  const requestPanelClose = () => {
    setPanelOpen(false)
  }
  const panelExited = () => {
    setPanelMember(null)
    const message = removalSuccess.current
    removalSuccess.current = null
    if (message !== null) {
      membersFeedback.succeed(message)
    }
  }
  const memberRemoved = (message: string) => {
    removalSuccess.current = message
    setPanelOpen(false)
  }

  const nextMemberPage = () => {
    // `keepPreviousData` leaves the previous page's envelope on screen while
    // the next one loads, so its `next_cursor` still names that same page.
    // Reading it again would push the same cursor twice and show one page
    // twice; the control waits for the read to settle.
    if (memberNextCursor === '' || membersFeedback.pending || members.isFetching) {
      return
    }
    const next = memberNextCursor
    setMemberCursorState((previous) => {
      const current =
        previous.tenant === tenant ? previous : firstCursorPage(tenant)
      const grown = [...current.cursors]
      grown[current.page + 1] = next
      return { tenant, cursors: grown, page: current.page + 1 }
    })
  }
  const previousMemberPage = () => {
    if (memberPage === 0 || membersFeedback.pending || members.isFetching) {
      return
    }
    setMemberCursorState((previous) => {
      const current =
        previous.tenant === tenant ? previous : firstCursorPage(tenant)
      return current.page === 0
        ? current
        : { ...current, page: current.page - 1 }
    })
  }

  // --- Invitations ------------------------------------------------------

  const [email, setEmail] = useState('')
  const [expiryDays, setExpiryDays] = useState<number>(DEFAULT_EXPIRY_DAYS)
  // The role an invitation grants, if any. The choice is tagged with the
  // tenant whose catalog offered it: a switch derives an empty choice in the
  // same render, so no role name reaches a tenant whose roles read has not
  // answered yet. The control renders only for a caller holding the tenant
  // role-management permission.
  const [issueRoleState, setIssueRoleState] = useState({
    tenant,
    role: '',
  })
  const issueRole = issueRoleState.tenant === tenant ? issueRoleState.role : ''
  const setIssueRole = (role: string) => setIssueRoleState({ tenant, role })
  // The issue form's escalation pre-check reads the selected role's grants
  // and compares each with the caller's own set, through the same shared
  // check the member panel's bind control uses.
  const {
    canManageRoles,
    roleSelected: issueRoleSelected,
    grants: issueGrants,
    grantsPending: issueGrantsPending,
    grantsFailed: issueGrantsFailed,
    grantPermissionUnconfirmed: issueGrantUnconfirmed,
    escalationBlocked: issueEscalationBlocked,
  } = useRoleEscalationCheck(tenant, issueRole)
  // The role choice belongs to the control that offered it. Without the
  // tenant role-management permission the control is absent, so a value
  // chosen earlier must not survive to be sent behind the operator's back
  // when the permission returns.
  useEffect(() => {
    if (!canManageRoles) {
      setIssueRoleState((previous) =>
        previous.role === '' ? previous : { ...previous, role: '' },
      )
    }
  }, [canManageRoles])
  // The list opens on the state an operator came to act on; history is one
  // filter choice away.
  const [statusFilter, setStatusFilter] =
    useState<InvitationStatusFilter>('pending')
  // A revoke moves focus to the invitation section's heading: the row may
  // leave the filtered list, taking the menu that started the write with
  // it, while the heading stays put and names the section the result
  // belongs to. A terminal refusal (409) also re-reads the list; when that
  // leaves the row out of the current filter, focus follows the same path.
  const {
    sectionRef: invitationSectionRef,
    focusHeading: focusInvitationHeading,
  } = useSectionHeadingFocus()
  const [invitationCursorState, setInvitationCursorState] =
    useState<CursorFrame>(() => firstCursorPage(tenant))
  const invitationCursorFrame =
    invitationCursorState.tenant === tenant
      ? invitationCursorState
      : firstCursorPage(tenant)
  const invitationPage = invitationCursorFrame.page
  const invitationCursor =
    invitationCursorFrame.cursors[invitationPage] ?? ''

  const invitationParams = useMemo(
    () => ({
      limit: INVITATION_PAGE_SIZE,
      ...(statusFilter === 'all' ? {} : { status: statusFilter }),
      ...(invitationCursor === '' ? {} : { after: invitationCursor }),
    }),
    [statusFilter, invitationCursor],
  )
  const invitations = operations.useInvitations(tenant, invitationParams)
  const invitationRows = invitations.data?.invitations
  const invitationNextCursor = invitations.data?.next_cursor ?? ''

  // The filter resets the cursor in its own change handler for the same
  // reason as the member search; the tenant switch derives the first page
  // synchronously and resets the previous stack after the commit.
  useEffect(() => {
    setInvitationCursorState(firstCursorPage(tenant))
  }, [tenant])

  const [issued, setIssued] = useState<IssuedNotice | null>(null)
  const [closeIssuedOpen, setCloseIssuedOpen] = useState(false)

  const issueInvitation = operations.useIssueInvitation()
  const revokeInvitation = operations.useRevokeInvitation()
  const resendInvitation = operations.useResendInvitation()

  const invitationFeedback = useWriteFeedback()
  const invitationRegionRef = useFailureScroll(invitationFeedback.outcome)
  const [failedInvitation, setFailedInvitation] = useState<string | null>(null)
  const invitationFailureText = failureTextOf(
    invitationFeedback.outcome,
    resolveErrorText,
  )
  const issueDisabled =
    invitationFeedback.pending ||
    email.trim() === '' ||
    issueGrantsPending ||
    issueGrantsFailed ||
    issueGrantUnconfirmed ||
    issueEscalationBlocked
  const confirm = usePendingConfirm()

  const resetPageFeedback = useCallback(() => {
    membersFeedback.reset()
    invitationFeedback.reset()
    setFailedInvitation(null)
  }, [membersFeedback.reset, invitationFeedback.reset])

  useEffect(() => {
    resetPageFeedback()
  }, [tenant, resetPageFeedback])

  // A notice whose delivery succeeded carries no token: the recipient holds
  // the link already, so the panel only says the invitation was emailed.
  const issuedToken = issued?.answer.token
  // Only a panel that holds a link loses something when it closes; a notice
  // the carrier accepted carries no token, so its panel closes directly.
  const issuedHasValue = issuedToken !== undefined
  const issuedValue =
    issuedToken === undefined
      ? ''
      : invitationLink === undefined
        ? issuedToken
        : acceptanceUrl(invitationLink(issuedToken))
  const issuedHasBuilder = invitationLink !== undefined

  const issue = () => {
    const address = email.trim()
    if (address === '') {
      return
    }
    // A new attempt owns the scope: the row marked by an earlier failed
    // resend or revoke must not keep a dangling `aria-describedby` once this
    // attempt replaces that failure's text.
    setFailedInvitation(null)
    void invitationFeedback.run(async () => {
      const answer = await issueInvitation.mutateAsync({
        tenant,
        email: address,
        expiresAt: expiryInstant(expiryDays),
        ...(canManageRoles && issueRole !== '' ? { role: issueRole } : {}),
      })
      setEmail('')
      setIssueRole('')
      setIssued({ answer, tenant })
      return t('invitations.issued.confirm', {
        email: answer.invitation.email,
      })
    })
  }

  const requestResend = (invitation: AdminInvitation) => {
    confirm.ask({
      title: t('invitations.resendTitle'),
      message: t('invitations.resendMessage', {
        email: invitation.email,
        time: formatTime(invitation.expires_at),
      }),
      confirmLabel: t('common.confirm'),
      run: () => {
        setFailedInvitation(null)
        void invitationFeedback.run(async () => {
          try {
            // Resend replaces the token on this invitation; it neither
            // extends the deadline nor creates another row.
            const answer = await resendInvitation.mutateAsync({
              tenant,
              id: invitation.id,
            })
            setIssued({ answer, tenant })
            return t('invitations.resent', {
              email: answer.invitation.email,
            })
          } catch (error) {
            setFailedInvitation(invitation.id)
            throw error
          }
        })
      },
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
            // The row leaves the filtered list; keep focus on the section's
            // heading, which stays visible and names the section the result
            // belongs to.
            focusInvitationHeading()
            return t('invitations.revoked', { email: invitation.email })
          } catch (error) {
            setFailedInvitation(invitation.id)
            // A terminal refusal leaves the row out of the current filter
            // too, so focus does not fall back to the body when the re-read
            // drops it. An "all" filter keeps the row, so its menu keeps
            // focus there.
            if (
              statusFilter !== 'all' &&
              errorCodeOf(error) === INVITATION_TERMINAL_CODE
            ) {
              focusInvitationHeading()
            }
            throw error
          }
        })
      },
    })
  }

  const nextInvitationPage = () => {
    if (invitationNextCursor === '' || invitationFeedback.pending) {
      return
    }
    const next = invitationNextCursor
    setInvitationCursorState((previous) => {
      const current =
        previous.tenant === tenant ? previous : firstCursorPage(tenant)
      const grown = [...current.cursors]
      grown[current.page + 1] = next
      return { tenant, cursors: grown, page: current.page + 1 }
    })
  }
  const previousInvitationPage = () => {
    if (invitationPage === 0 || invitationFeedback.pending) {
      return
    }
    setInvitationCursorState((previous) => {
      const current =
        previous.tenant === tenant ? previous : firstCursorPage(tenant)
      return current.page === 0
        ? current
        : { ...current, page: current.page - 1 }
    })
  }

  // A filter change is a new first page and a new scope, exactly as the
  // member filter is: the cursor stack belonged to the previous filter, and
  // a result or row marker written under it would read as describing the
  // filter just left behind.
  const changeInvitationStatusFilter = (next: InvitationStatusFilter) => {
    setStatusFilter(next)
    setInvitationCursorState(firstCursorPage(tenant))
    invitationFeedback.reset()
    setFailedInvitation(null)
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
    return key !== null && i18n.exists(key, { ns: ADMIN_UI_NAMESPACE })
      ? options === undefined
        ? t(key)
        : t(key, options)
      : source
  }

  const memberColumns: DataTableColumn<AdminMember>[] = [
    {
      id: 'displayName',
      header: t('members.column.displayName'),
      // The display name is the row's identity: a short one must never be
      // split mid-token by a squeezed column. It stays on one line and
      // truncates inside the cap. Only the username and email columns carry
      // a copy control; the display name is read, not copied.
      cellWrap: 'nowrap',
      contentMaxWidth: MEMBER_IDENTITY_MAX_WIDTH,
      // A truncated name stays reachable by hover; the copy control belongs
      // to the username and email columns only.
      cell: (member) => (
        <Typography
          variant="body2"
          noWrap
          title={member.display_name === '' ? undefined : member.display_name}
        >
          {member.display_name}
        </Typography>
      ),
    },
    {
      id: 'username',
      header: t('members.column.username'),
      priority: 'high',
      // The column shows the member record's login username as it is: the
      // display name is the identity column beside it, and the account's
      // internal identifier is never shown. The copy control hands over the
      // username -- the unique value an operator can act on. A member the
      // record carries no username for gets the neutral copy rather than a
      // repeat of the display name.
      cellWrap: 'nowrap',
      cellFullValue: (member) =>
        member.username === '' ? undefined : member.username,
      contentMaxWidth: MEMBER_IDENTITY_MAX_WIDTH,
      cell: (member) =>
        member.username === ''
          ? t('common.accountUnavailable')
          : member.username,
    },
    {
      id: 'roles',
      header: t('members.column.roles'),
      // The role list is a primary fact about a member, not a fine-print
      // detail: it stays from the high floor up, so the 1024px desktop
      // viewport (280px sidebar, ~640px of content) still shows it.
      priority: 'high',
      cellWrap: 'wrap',
      // The role names are read, not copied; the copy control stays on the
      // username and email columns.
      contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
      cell: (member) =>
        member.roles.length === 0 ? (
          <Typography variant="body2" color="text.secondary">
            {t('members.roles.none')}
          </Typography>
        ) : (
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5 }}>
            {member.roles.map((role) => (
              <Chip
                key={role}
                size="small"
                variant="outlined"
                label={role}
              />
            ))}
          </Box>
        ),
    },
    {
      id: 'status',
      header: t('members.column.status'),
      priority: 'high',
      cellWrap: 'nowrap',
      cell: (member) => (
        <StatusTag
          label={
            member.active
              ? t('members.status.active')
              : t('members.status.suspended')
          }
          tone={member.active ? 'success' : 'default'}
        />
      ),
    },
    {
      id: 'source',
      header: t('members.column.source'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (member) => memberSourceLabel(member.source),
    },
    {
      id: 'joinedAt',
      header: t('members.column.joinedAt'),
      // Joined-at is ordinary row information beside roles and status, so
      // it stays from the high floor up rather than disappearing on the
      // common laptop width the way a medium or low column would. The
      // timestamp wraps at that floor instead of forcing the table wider.
      priority: 'high',
      cellWrap: 'wrap',
      cell: (member) => formatTime(member.joined_at),
    },
    {
      id: 'actions',
      align: 'right',
      header: t('members.column.actions'),
      cellWrap: 'nowrap',
      cell: (member) => (
        <Button
          type="button"
          size="small"
          variant="outlined"
          aria-label={t('members.manageAria', {
            name:
              identityLabel(member.display_name, member.username) ??
              t('common.accountUnavailable'),
          })}
          onClick={() => openPanel(member)}
        >
          {t('members.manage')}
        </Button>
      ),
    },
  ]

  const invitationColumns: DataTableColumn<AdminInvitation>[] = [
    {
      id: 'email',
      header: t('invitations.column.email'),
      cellWrap: 'nowrap',
      cellFullValue: (invitation) => invitation.email,
      contentMaxWidth: INVITATION_IDENTITY_MAX_WIDTH.email,
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
      // The tag is one short word and stays whole; the caption under a
      // terminal tag names the instant it settled and wraps inside the
      // column instead of forcing the row wider than the sm floor.
      cellWrap: 'wrap',
      cell: (invitation) => {
        const state = invitation.state
        return (
          <StatusTag
            label={statusLabel(state, t)}
            tone={statusTone(state)}
            note={terminalNote(invitation, state, t, formatTime)}
          />
        )
      },
    },
    {
      id: 'role',
      header: t('invitations.column.role'),
      // A role is secondary to the address and its state: the column yields
      // at the narrow floor and returns from the md container tier.
      priority: 'medium',
      cellWrap: 'nowrap',
      contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
      cell: (invitation) =>
        invitation.role === undefined || invitation.role === '' ? null : (
          <Chip size="small" variant="outlined" label={invitation.role} />
        ),
    },
    {
      id: 'invitedBy',
      header: t('invitations.column.invitedBy'),
      priority: 'high',
      // The invitation carries the inviter's display name; the username is
      // only the hover title behind a truncated name, never a second visible
      // value. An invitation a host command issued, or whose inviter is gone,
      // carries neither key and leaves the cell empty.
      cellWrap: 'nowrap',
      contentMaxWidth: INVITATION_IDENTITY_MAX_WIDTH.invitedBy,
      cell: (invitation) => {
        const name = invitation.invited_by_name
        if (name === undefined || name === '') {
          return null
        }
        const full =
          invitation.invited_by_username === undefined ||
          invitation.invited_by_username === ''
            ? name
            : `${name} (${invitation.invited_by_username})`
        return (
          <Typography variant="body2" noWrap title={full}>
            {name}
          </Typography>
        )
      },
    },
    {
      id: 'expires',
      header: t('invitations.column.expires'),
      // Only a pending invitation has a deadline the operator can still act
      // on; terminal rows leave the cell empty. A deadline within a day says
      // how long is left.
      priority: 'high',
      cellWrap: 'wrap',
      cell: (invitation) => {
        if (invitation.state !== 'pending') {
          return null
        }
        const remaining = remainingText(invitation.expires_at, t)
        return (
          <Box sx={{ display: 'flex', flexDirection: 'column' }}>
            <span>{formatTime(invitation.expires_at)}</span>
            {remaining !== undefined && (
              <Typography variant="caption" color="text.secondary">
                {remaining}
              </Typography>
            )}
          </Box>
        )
      },
    },
    {
      id: 'created',
      header: t('invitations.column.created'),
      // The issued time is the row's `created_at` and does not move on a
      // resend; a resent row adds the count and the latest send beneath it.
      priority: 'high',
      cellWrap: 'wrap',
      cell: (invitation) => (
        <Box>
          {formatTime(invitation.created_at)}
          {invitation.send_count > 1 && (
            <Typography
              variant="caption"
              color="text.secondary"
              sx={{ display: 'block', mt: 0.5 }}
            >
              {invitation.send_count - 1 === 1
                ? t('invitations.created.resentOnce', {
                    time: formatTime(invitation.last_sent_at),
                  })
                : t('invitations.created.resent', {
                    count: String(invitation.send_count - 1),
                    time: formatTime(invitation.last_sent_at),
                  })}
            </Typography>
          )}
        </Box>
      ),
    },
    {
      id: 'actions',
      align: 'right',
      header: t('invitations.column.actions'),
      cellWrap: 'nowrap',
      cell: (invitation) => {
        // A terminal invitation has nothing left to act on: its link no
        // longer works, and the server refuses both writes with 409.
        if (invitation.state !== 'pending') {
          return null
        }
        const failed = failedInvitation === invitation.id
        const errorId = `invitation-error-${encodeURIComponent(invitation.id)}`
        const pending = invitationFeedback.pending
        const current =
          issued !== null && issued.answer.invitation.id === invitation.id
        return (
          <Box>
            <Box
              sx={{
                display: 'flex',
                flexWrap: 'wrap',
                alignItems: 'center',
                columnGap: 1,
                rowGap: 0.5,
              }}
            >
              <RowActionMenu
                label={t('invitations.actionsAria', {
                  email: invitation.email,
                })}
                invalid={failed}
                describedBy={failed ? errorId : undefined}
                actions={[
                  {
                    key: 'resend',
                    label: t('invitations.resend'),
                    onSelect: () => requestResend(invitation),
                    disabled: pending,
                  },
                  {
                    key: 'revoke',
                    label: t('invitations.revoke'),
                    onSelect: () => requestRevoke(invitation),
                    disabled: pending,
                  },
                ]}
              />
              {/* The acceptance token is handed out once: only the item just
                  issued or resent has a value to copy. */}
              {current && (
                <CopyButton
                  value={issuedValue}
                  label={t('invitations.copyLink')}
                  visibleLabel={t('invitations.copyLink')}
                  copiedLabel={t('invitations.issued.copied')}
                />
              )}
            </Box>
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

  const memberSearchActive = debouncedMemberSearch !== ''
  const memberEmpty = memberRows !== undefined && memberRows.length === 0
  const invitationsEmpty =
    invitationRows !== undefined && invitationRows.length === 0

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

      <SectionCard
        title={t('members.title')}
        icon={<AccountsIcon size={24} />}
        contentAligned={false}
        sx={{ mb: 3 }}
      >
        {/* The toolbar stays outside the async section: a loading, empty or
            failed list must not swallow the controls that change it. */}
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
            value={memberSearch}
            onChange={(event) => {
              setMemberSearch(event.target.value)
              setMemberCursorState(firstCursorPage(tenant))
            }}
            label={t('members.search.label')}
            placeholder={t('members.search.placeholder')}
            sx={{ minWidth: 260 }}
          />
          {memberSearch !== '' && (
            <Button
              type="button"
              size="small"
              variant="outlined"
              onClick={() => {
                setMemberSearch('')
                setMemberCursorState(firstCursorPage(tenant))
              }}
            >
              {t('members.search.clear')}
            </Button>
          )}
          {/* The role options come from the tenant's role read. A failed read
              keeps the current selection addressable and offers a retry
              rather than presenting an empty role list as the truth. */}
          <TextField
            select
            size="small"
            value={roleFilter}
            onChange={(event) => changeRoleFilter(event.target.value)}
            label={t('members.filter.roleLabel')}
            helperText={
              tenantRoles.isError ? (
                <Box component="span">
                  {t('members.filter.rolesError')}{' '}
                  <Button
                    type="button"
                    size="small"
                    variant="text"
                    onClick={() => void tenantRoles.refetch()}
                  >
                    {t('members.filter.rolesRetry')}
                  </Button>
                </Box>
              ) : undefined
            }
            sx={{ minWidth: 240 }}
          >
            <MenuItem value="">{t('members.filter.anyRole')}</MenuItem>
            {roleFilterActive && !roleListed && (
              <MenuItem value={roleFilter}>{roleFilter}</MenuItem>
            )}
            {roleOptions.map((role) => (
              <MenuItem key={role.role} value={role.role}>
                {role.role}
              </MenuItem>
            ))}
          </TextField>
          {roleFilterActive && (
            <Button
              type="button"
              size="small"
              variant="outlined"
              onClick={() => changeRoleFilter('')}
            >
              {t('members.filter.clear')}
            </Button>
          )}
        </Box>
        <AdminAsyncSection
          pending={members.isPending && memberRows === undefined}
          payload={memberRows}
          error={members.error}
          refreshing={members.isFetching}
          empty={memberEmpty}
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
            title: roleFilterActive
              ? t('members.empty.roleTitle')
              : memberSearchActive
                ? t('members.empty.searchTitle')
                : t('members.empty.title'),
            description: roleFilterActive
              ? t('members.empty.roleDescription', { role: roleFilter })
              : memberSearchActive
                ? t('members.empty.searchDescription', {
                    query: debouncedMemberSearch,
                  })
                : t('members.empty.description'),
          }}
          ssoRelogin={session === null ? undefined : relogin}
          mfaRelogin={session === null ? undefined : relogin}
        >
          {(settled) => (
            <DataTable
              rows={settled}
              columns={memberColumns}
              rowKey={(member) => member.account}
              pinLastColumn
              sx={COMPACT_TABLE_SX}
            />
          )}
        </AdminAsyncSection>
        {memberRows !== undefined && (
          <CursorPager
            page={memberPage}
            hasNext={memberNextCursor !== ''}
            busy={membersFeedback.pending || members.isFetching}
            onPrevious={previousMemberPage}
            onNext={nextMemberPage}
          />
        )}
        <Box ref={membersRegionRef} sx={{ mt: 2 }}>
          <WriteFeedbackRegion
            outcome={membersFeedback.outcome}
            ssoRelogin={session === null ? undefined : relogin}
            mfaRelogin={session === null ? undefined : relogin}
          />
        </Box>
      </SectionCard>

      <SectionCard
        ref={invitationSectionRef}
        title={t('invitations.section.title')}
        icon={<InvitationsIcon size={24} />}
        contentAligned={false}
        sx={{ mb: 3 }}
      >
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
            sx={{ minWidth: 260 }}
          />
          <TextField
            select
            size="small"
            value={String(expiryDays)}
            onChange={(event) => setExpiryDays(Number(event.target.value))}
            label={t('invitations.issue.quickLabel')}
            sx={{ minWidth: 140 }}
          >
            {EXPIRY_CHOICES.map((days) => (
              <MenuItem key={days} value={String(days)}>
                {days === 1
                  ? t('invitations.issue.oneDay')
                  : days === 7
                    ? t('invitations.issue.sevenDays')
                    : t('invitations.issue.thirtyDays')}
              </MenuItem>
            ))}
          </TextField>
          {/* A caller without the tenant role-management permission cannot
              grant a role, so the choice is absent rather than refused. */}
          {canManageRoles && (
            <TextField
              select
              size="small"
              value={issueRole}
              onChange={(event) => setIssueRole(event.target.value)}
              label={t('invitations.issue.roleLabel')}
              sx={{ minWidth: 160 }}
            >
              <MenuItem value="">{t('invitations.issue.roleNone')}</MenuItem>
              {roleOptions.map((role) => (
                <MenuItem key={role.role} value={role.role}>
                  {role.role}
                </MenuItem>
              ))}
            </TextField>
          )}
          <Button
            type="button"
            variant="contained"
            disabled={issueDisabled}
            onClick={issue}
          >
            {t('invitations.issue.submit')}
          </Button>
        </Box>
        {canManageRoles && issueRoleSelected && (
          <Box sx={{ mb: 2 }}>
            {issueGrantsPending && (
              <Typography variant="body2" color="text.secondary">
                {t('invitations.issue.roleChecking')}
              </Typography>
            )}
            {issueGrantsFailed && (
              <>
                <Typography variant="body2" color="error">
                  {t('invitations.issue.roleFailed')}
                </Typography>
                <Button
                  type="button"
                  size="small"
                  variant="outlined"
                  onClick={() => void issueGrants.refetch()}
                >
                  {t('invitations.issue.roleRetry')}
                </Button>
              </>
            )}
            {issueGrantUnconfirmed ? (
              <Typography variant="body2" color="error">
                {t('invitations.issue.roleUnconfirmed')}
              </Typography>
            ) : (
              issueEscalationBlocked && (
                <Typography variant="body2" color="error">
                  {t('invitations.issue.roleBlocked')}
                </Typography>
              )
            )}
          </Box>
        )}

        {issued !== null && (
          <OneTimeValuePanel
            message={
              issued.answer.token === undefined ? (
                // The notice reached the address, so there is no link to pass
                // on and no expiry or risk to describe.
                t('invitations.issued.emailed', {
                  email: issued.answer.invitation.email,
                })
              ) : (
                <>
                  {issuedHasBuilder
                    ? t('invitations.issued.link', {
                        email: issued.answer.invitation.email,
                      })
                    : t('invitations.issued.token', {
                        email: issued.answer.invitation.email,
                      })}
                  <Typography
                    component="span"
                    variant="body2"
                    color="text.secondary"
                    sx={{ display: 'block', mt: 0.5 }}
                  >
                    {t('invitations.issued.expiresAt', {
                      time: formatTime(issued.answer.invitation.expires_at),
                    })}
                  </Typography>
                  <Typography
                    component="span"
                    variant="body2"
                    color="text.secondary"
                    sx={{ display: 'block', mt: 0.5 }}
                  >
                    {t('invitations.issued.risk')}
                  </Typography>
                  <Typography
                    component="span"
                    variant="body2"
                    color={
                      issued.answer.email_delivery === 'failed'
                        ? 'error'
                        : 'text.secondary'
                    }
                    sx={{ display: 'block', mt: 0.5 }}
                  >
                    {t(
                      `invitations.issued.delivery.${issued.answer.email_delivery}`,
                    )}
                  </Typography>
                </>
              )
            }
            {...(issued.answer.token === undefined
              ? {}
              : {
                  value: issuedValue,
                  copyLabel: issuedHasBuilder
                    ? t('invitations.issued.copyLink')
                    : t('invitations.issued.copyToken'),
                })}
            closeLabel={t('invitations.issued.close')}
            closeAriaLabel={t('invitations.issued.closeAria', {
              tenant: issued.tenant,
              email: issued.answer.invitation.email,
            })}
            onClose={() => {
              if (issuedHasValue) {
                setCloseIssuedOpen(true)
              } else {
                setIssued(null)
              }
            }}
          />
        )}

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
            select
            size="small"
            value={statusFilter}
            onChange={(event) =>
              changeInvitationStatusFilter(
                event.target.value as InvitationStatusFilter,
              )
            }
            label={t('invitations.filter.label')}
            sx={{ minWidth: 160 }}
          >
            {INVITATION_STATUS_FILTERS.map((filter) => (
              <MenuItem key={filter} value={filter}>
                {filter === 'all'
                  ? t('invitations.filter.all')
                  : statusLabel(filter, t)}
              </MenuItem>
            ))}
          </TextField>
        </Box>

        <AdminAsyncSection
          pending={invitations.isPending && invitationRows === undefined}
          payload={invitationRows}
          error={invitations.error}
          refreshing={invitations.isFetching}
          empty={invitationsEmpty}
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
            title:
              statusFilter === 'all'
                ? t('invitations.empty.title')
                : t('invitations.empty.filteredTitle'),
            description:
              statusFilter === 'all'
                ? t('invitations.empty.description')
                : t('invitations.empty.filteredDescription', {
                    status: statusLabel(statusFilter, t),
                  }),
          }}
          ssoRelogin={session === null ? undefined : relogin}
          mfaRelogin={session === null ? undefined : relogin}
        >
          {(settled) => (
            <DataTable
              rows={settled}
              columns={invitationColumns}
              rowKey={(invitation) => invitation.id}
              pinLastColumn
              sx={COMPACT_TABLE_SX}
            />
          )}
        </AdminAsyncSection>
        {invitationRows !== undefined && (
          <CursorPager
            page={invitationPage}
            hasNext={invitationNextCursor !== ''}
            busy={invitationFeedback.pending}
            onPrevious={previousInvitationPage}
            onNext={nextInvitationPage}
          />
        )}
        <Box ref={invitationRegionRef} sx={{ mt: 2 }}>
          <WriteFeedbackRegion
            outcome={invitationFeedback.outcome}
            ssoRelogin={session === null ? undefined : relogin}
            mfaRelogin={session === null ? undefined : relogin}
          />
        </Box>
      </SectionCard>

      {livePanelMember !== null && (
        <MemberPanel
          open={panelOpen}
          tenant={tenant}
          member={livePanelMember}
          onClose={requestPanelClose}
          onExited={panelExited}
          onRemoved={memberRemoved}
        />
      )}

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />

      {/* A panel that holds a link is cleared only after the operator
          confirms: closing it is the only moment the sole copy is dropped.
          A panel for a delivered notice holds no link and closes directly. */}
      <ConfirmDialog
        open={closeIssuedOpen}
        title={t('invitations.issued.closeTitle')}
        message={t('invitations.issued.closeMessage')}
        confirmLabel={t('invitations.issued.close')}
        cancelLabel={t('invitations.issued.keepOpen')}
        onConfirm={() => {
          setIssued(null)
          setCloseIssuedOpen(false)
        }}
        onCancel={() => setCloseIssuedOpen(false)}
      />
    </Box>
  )
}
