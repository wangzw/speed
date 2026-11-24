/**
 * OrganizationMembersPage renders an organization's members and invitations:
 * a searchable, cursor-paged member table with a per-row management panel,
 * and an invitation issue form plus a status-filtered list.
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
  ActionButton,
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
  useInertSelectMenu,
  useWriteFeedback,
} from '@speed/ui-kit'
import type {
  ActionButtonUnavailableExplanation,
  DataTableColumn,
} from '@speed/ui-kit'
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

const MEMBER_PAGE_SIZE = 100

const INVITATION_PAGE_SIZE = 50

const DEFAULT_EXPIRY_DAYS = 7

const EXPIRY_CHOICES = [1, 7, 30] as const

// Ids of the role-check paragraphs the issue control points at.
const ISSUE_ROLE_CHECKING_ID = 'invitation-issue-role-checking'
const ISSUE_ROLE_FAILED_ID = 'invitation-issue-role-failed'
const ISSUE_ROLE_UNCONFIRMED_ID = 'invitation-issue-role-unconfirmed'
const ISSUE_ROLE_BLOCKED_ID = 'invitation-issue-role-blocked'

// Tightened padding lets the five high-priority columns fit the 600px sm floor.
const COMPACT_TABLE_SX = {
  '& .MuiTableCell-root': { px: 1 },
} as const

// Below md the two nowrap identity columns cap at 96px: at the 600px sm floor,
// 96px each plus the worst-case "Suspended" status fits; the complete values stay
// in the title and copy control.
const MEMBER_IDENTITY_MAX_WIDTH = {
  belowMd: 96,
  fromMd: PRIMARY_COLUMN_MAX_WIDTH.fromMd,
} as const

const MEMBER_SOURCES = new Set<string>(['admin', 'initial', 'invite'])
const JIT_SOURCE_PREFIX = 'jit:'

interface IssuedNotice {
  readonly answer: AdminIssuedInvitation
  readonly tenant: string
}

// A cursor stack tagged with its tenant; a frame for another tenant derives as
// the first page, so a switch carries no cursor from the tenant left behind.
interface CursorFrame {
  readonly tenant: string
  readonly cursors: readonly string[]
  readonly page: number
}

function firstCursorPage(tenant: string): CursorFrame {
  return { tenant, cursors: [''], page: 0 }
}

const ROLE_QUERY_PARAM = 'role'

function roleFromSearch(search: string): string {
  return new URLSearchParams(search).get(ROLE_QUERY_PARAM)?.trim() ?? ''
}

function roleFromLocation(): string {
  return typeof window === 'undefined'
    ? ''
    : roleFromSearch(window.location.search)
}

// Writes the role into the URL, percent-encoded in place (not form-encoded) so
// names with spaces or ampersands round-trip; an empty role removes the parameter.
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

function expiryInstant(days: number): string {
  return new Date(Date.now() + days * 24 * 60 * 60 * 1000).toISOString()
}

// Resolves a host-relative link against the current origin; an unresolvable value
// is shown as it came.
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
  const formatTime = useFormatInstant()
  const invitationLink = useAdminInvitationLink()
  const resolveErrorText = useAdminUiErrorText()
  const session = useAdminSession()
  // The MFA refusal cannot be cleared locally: ending the session returns the
  // host's session gate to its sign-in surface.
  const relogin = useCallback(() => {
    void session?.logout().catch(() => undefined)
  }, [session])
  const roleFilterMenu = useInertSelectMenu()
  const expiryMenu = useInertSelectMenu()
  const issueRoleMenu = useInertSelectMenu()
  const invitationStatusFilterMenu = useInertSelectMenu()

  const [memberSearch, setMemberSearch] = useState('')
  const debouncedMemberSearch = useDebouncedValue(memberSearch)
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
      // The server filters; the page never narrows the rows it received.
      ...(roleFilter === '' ? {} : { role: roleFilter }),
      ...(memberCursor === '' ? {} : { after: memberCursor }),
    }),
    [debouncedMemberSearch, roleFilter, memberCursor],
  )
  const members = operations.useMembers(tenant, memberParams)
  const memberRows = members.data?.members
  const memberNextCursor = members.data?.next_cursor ?? ''

  useEffect(() => {
    setMemberCursorState(firstCursorPage(tenant))
  }, [tenant])

  const [panelMember, setPanelMember] = useState<AdminMember | null>(null)
  const [panelOpen, setPanelOpen] = useState(false)
  const memberListRef = useRef<HTMLDivElement | null>(null)
  const removalFocus = useRef<{
    readonly account: string
    readonly index: number
  } | null>(null)
  // The panel belongs to its tenant; the derivation below refuses another
  // tenant's snapshot even before this effect commits.
  useEffect(() => {
    setPanelOpen(false)
    setPanelMember(null)
  }, [tenant])
  // The panel follows the list's current row, so a suspend or resume shows the
  // new state; a row no longer listed keeps the snapshot.
  const livePanelMember =
    panelMember === null || panelMember.tenant !== tenant
      ? null
      : (memberRows?.find((row) => row.account === panelMember.account) ??
        panelMember)
  // The notice is written after the panel exits: while the modal is open the
  // page behind it is aria-hidden.
  const removalSuccess = useRef<string | null>(null)

  const membersFeedback = useWriteFeedback()
  const membersRegionRef = useFailureScroll(membersFeedback.outcome)

  const roleFilterActive = roleFilter !== ''
  const roleListed = roleOptions.some((role) => role.role === roleFilter)

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
  // Places focus after a removal closed the panel: the same control on the next
  // row, else the previous, else the list container, so focus never drops to the body.
  const placeFocusAfterRemoval = (
    removed: { readonly account: string; readonly index: number } | null,
  ) => {
    if (removed === null) {
      return
    }
    const list = memberListRef.current
    if (list === null) {
      return
    }
    // Recover focus only when the removal left it on the body, the closing
    // panel, or the removed row's own opener.
    const active = document.activeElement
    const focusWasLost =
      active === null ||
      active === document.body ||
      (active instanceof Element &&
        (active.closest('[role="dialog"]') !== null ||
          active.getAttribute('data-member-manage') === removed.account))
    if (!focusWasLost) {
      return
    }
    const controls = Array.from(
      list.querySelectorAll<HTMLElement>('[data-member-manage]'),
    )
    const stillListed = controls.findIndex(
      (control) => control.dataset.memberManage === removed.account,
    )
    const target =
      stillListed >= 0
        ? (controls[stillListed + 1] ?? controls[stillListed - 1])
        : (controls[removed.index] ?? controls[removed.index - 1])
    if (target !== undefined) {
      target.focus()
      return
    }
    list.focus()
  }
  const panelExited = () => {
    setPanelMember(null)
    const removed = removalFocus.current
    removalFocus.current = null
    placeFocusAfterRemoval(removed)
    const message = removalSuccess.current
    removalSuccess.current = null
    if (message !== null) {
      membersFeedback.succeed(message)
    }
  }
  const memberRemoved = (message: string) => {
    const removed = livePanelMember
    removalFocus.current =
      removed === null
        ? null
        : {
            account: removed.account,
            index:
              memberRows?.findIndex(
                (row) => row.account === removed.account,
              ) ?? -1,
          }
    removalSuccess.current = message
    setPanelOpen(false)
  }

  const nextMemberPage = () => {
    // keepPreviousData leaves the previous envelope on screen, whose next_cursor
    // names the same page; the control waits for the read to settle.
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

  const [email, setEmail] = useState('')
  const [expiryDays, setExpiryDays] = useState<number>(DEFAULT_EXPIRY_DAYS)
  // The choice is tagged with its tenant, so no role name reaches a tenant whose
  // roles read has not answered.
  const [issueRoleState, setIssueRoleState] = useState({
    tenant,
    role: '',
  })
  const issueRole = issueRoleState.tenant === tenant ? issueRoleState.role : ''
  const setIssueRole = (role: string) => setIssueRoleState({ tenant, role })
  const {
    canManageRoles,
    roleSelected: issueRoleSelected,
    grants: issueGrants,
    grantsPending: issueGrantsPending,
    grantsFailed: issueGrantsFailed,
    grantPermissionUnconfirmed: issueGrantUnconfirmed,
    escalationBlocked: issueEscalationBlocked,
  } = useRoleEscalationCheck(tenant, issueRole)
  // Without the role-management permission the control is absent, so a previously
  // chosen role must not be sent when the permission returns.
  useEffect(() => {
    if (!canManageRoles) {
      setIssueRoleState((previous) =>
        previous.role === '' ? previous : { ...previous, role: '' },
      )
    }
  }, [canManageRoles])
  const [statusFilter, setStatusFilter] =
    useState<InvitationStatusFilter>('pending')
  // A revoke moves focus to the invitations heading, which stays while the row
  // may leave the filtered list.
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

  useEffect(() => {
    setInvitationCursorState(firstCursorPage(tenant))
  }, [tenant])

  const [issued, setIssued] = useState<IssuedNotice | null>(null)
  const [pendingInvitationWrite, setPendingInvitationWrite] = useState<
    | { readonly kind: 'issue' }
    | { readonly kind: 'resend'; readonly id: string }
    | { readonly kind: 'revoke'; readonly id: string }
    | null
  >(null)
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
  // Each role state has its own paragraph the control points at; an empty
  // address has none, so the control carries that reason.
  const issueExplanation: ActionButtonUnavailableExplanation | undefined =
    email.trim() === ''
      ? { unavailableReason: t('invitations.issue.emailRequired') }
      : issueGrantsPending
        ? { unavailableDescribedBy: ISSUE_ROLE_CHECKING_ID }
        : issueGrantsFailed
          ? { unavailableDescribedBy: ISSUE_ROLE_FAILED_ID }
          : issueGrantUnconfirmed
            ? { unavailableDescribedBy: ISSUE_ROLE_UNCONFIRMED_ID }
            : issueEscalationBlocked
              ? { unavailableDescribedBy: ISSUE_ROLE_BLOCKED_ID }
              : undefined
  const confirm = usePendingConfirm()

  const resetPageFeedback = useCallback(() => {
    membersFeedback.reset()
    invitationFeedback.reset()
    setFailedInvitation(null)
  }, [membersFeedback.reset, invitationFeedback.reset])

  useEffect(() => {
    resetPageFeedback()
  }, [tenant, resetPageFeedback])

  const issuedToken = issued?.answer.token
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
    setFailedInvitation(null)
    setPendingInvitationWrite({ kind: 'issue' })
    void invitationFeedback
      .run(async () => {
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
      .finally(() => setPendingInvitationWrite(null))
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
        setPendingInvitationWrite({ kind: 'resend', id: invitation.id })
        void invitationFeedback
          .run(async () => {
            try {
              // Resend replaces the token; it neither extends the deadline nor
              // creates another row.
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
          .finally(() => setPendingInvitationWrite(null))
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
        setPendingInvitationWrite({ kind: 'revoke', id: invitation.id })
        void invitationFeedback
          .run(async () => {
            try {
              await revokeInvitation.mutateAsync({
                tenant,
                id: invitation.id,
              })
              focusInvitationHeading()
              return t('invitations.revoked', { email: invitation.email })
            } catch (error) {
              setFailedInvitation(invitation.id)
              // A terminal refusal also drops the row from a filtered list, so
              // focus follows the heading; an "all" filter keeps the row.
              if (
                statusFilter !== 'all' &&
                errorCodeOf(error) === INVITATION_TERMINAL_CODE
              ) {
                focusInvitationHeading()
              }
              throw error
            }
          })
          .finally(() => setPendingInvitationWrite(null))
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

  const changeInvitationStatusFilter = (next: InvitationStatusFilter) => {
    setStatusFilter(next)
    setInvitationCursorState(firstCursorPage(tenant))
    invitationFeedback.reset()
    setFailedInvitation(null)
  }

  // A `jit:<id>` source names its provider; anything else is shown as it came.
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
      // Stays on one line and truncates inside the cap; the display name is read,
      // not copied.
      cellWrap: 'nowrap',
      contentMaxWidth: MEMBER_IDENTITY_MAX_WIDTH,
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
      // The account's internal identifier is never shown; a member with no
      // username gets the neutral copy.
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
      priority: 'high',
      cellWrap: 'wrap',
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
          // Names the row's own control so a removal can hand focus to its neighbour.
          data-member-manage={member.account}
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
      // Only the display name shows; the username is the truncated name's hover
      // title. Missing inviter keys leave the cell empty.
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
      // `created_at` does not move on a resend; the count and latest send sit beneath.
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
        // A terminal invitation's link no longer works, and the server refuses
        // both writes with 409.
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
        {/* The toolbar stays outside the async section, so a loading, empty or
            failed list cannot swallow its controls. */}
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
          {/* A failed role read keeps the selection addressable and offers a retry. */}
          <TextField
            select
            slotProps={{ select: roleFilterMenu }}
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
        {/* The fallback focus target after a removal; it stays mounted through
            the empty and failed branches. */}
        <Box
          ref={memberListRef}
          role="group"
          aria-label={t('members.title')}
          tabIndex={-1}
        >
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
              busy={membersFeedback.pending}
              reading={members.isFetching}
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
            slotProps={{ select: expiryMenu }}
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
          {/* Without the role-management permission the choice is absent. */}
          {canManageRoles && (
            <TextField
              select
              slotProps={{ select: issueRoleMenu }}
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
          <ActionButton
            type="button"
            emphasis="primary"
            busy={pendingInvitationWrite?.kind === 'issue'}
            unavailable={
              issueExplanation !== undefined ||
              (pendingInvitationWrite !== null &&
                pendingInvitationWrite.kind !== 'issue')
            }
            {...(issueExplanation ?? {})}
            onClick={issue}
          >
            {t('invitations.issue.submit')}
          </ActionButton>
        </Box>
        {canManageRoles && issueRoleSelected && (
          <Box sx={{ mb: 2 }}>
            {issueGrantsPending && (
              <Typography
                id={ISSUE_ROLE_CHECKING_ID}
                variant="body2"
                color="text.secondary"
              >
                {t('invitations.issue.roleChecking')}
              </Typography>
            )}
            {issueGrantsFailed && (
              <>
                <Typography
                  id={ISSUE_ROLE_FAILED_ID}
                  variant="body2"
                  color="error"
                >
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
              <Typography
                id={ISSUE_ROLE_UNCONFIRMED_ID}
                variant="body2"
                color="error"
              >
                {t('invitations.issue.roleUnconfirmed')}
              </Typography>
            ) : (
              issueEscalationBlocked && (
                <Typography
                  id={ISSUE_ROLE_BLOCKED_ID}
                  variant="body2"
                  color="error"
                >
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
            slotProps={{ select: invitationStatusFilterMenu }}
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

      {/* Closing a panel that holds a link is the only moment the sole copy is
          dropped, so it confirms; a delivered notice closes directly. */}
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
