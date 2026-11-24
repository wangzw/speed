import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Checkbox from '@mui/material/Checkbox'
import Chip from '@mui/material/Chip'
import FormControlLabel from '@mui/material/FormControlLabel'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  ActionButton,
  ConfirmDialog,
  CopyButton,
  DataTable,
  EmptyState,
  errorCodeOf,
  ListSkeleton,
  LONG_TEXT_COLUMN_MAX_WIDTH,
  OneTimeValuePanel,
  PRIMARY_COLUMN_MAX_WIDTH,
  SectionCard,
  useFormatInstant,
  useWriteFeedback,
} from '@speed/ui-kit'
import type {
  ActionButtonUnavailableExplanation,
  DataTableColumn,
  WriteFeedback,
} from '@speed/ui-kit'
import {
  accountIdentityValue,
  accountNotice,
  identityLabel,
  type AccountIdentityValue,
} from './internal/account-identity.js'
import { AccountSelector } from './internal/account-selector.js'
import { AdminAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { CursorPager } from './internal/cursor-pager.js'
import { INVITATION_IDENTITY_MAX_WIDTH } from './internal/invitation-columns.js'
import {
  INVITATION_STATUS_FILTERS,
  remainingText,
  statusLabel,
  statusTone,
  terminalNote,
  type InvitationStatusFilter,
} from './internal/invitation-status.js'
import { RowActionMenu } from './internal/row-action-menu.js'
import { useSectionHeadingFocus } from './internal/section-heading-focus.js'
import { StatusTag } from './internal/status-tag.js'
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
import {
  INVITATION_TERMINAL_CODE,
  useAdminOperations,
} from './operations.js'
import { AdminPageHeader } from './internal/page-header.js'
import type {
  AdminAccount,
  AdminInvitation,
  AdminIssuedInvitation,
  AdminMember,
} from './operations.js'

const TENANT_HINT_ID = 'invitations-tenant-hint'

const MEMBER_PAGE_SIZE = 100

const INVITATION_PAGE_SIZE = 50

const DEFAULT_EXPIRY_DAYS = 7

const EXPIRY_CHOICES = [1, 7, 30] as const

const DAY_MS = 24 * 60 * 60 * 1000

interface IssuedNotice {
  readonly answer: AdminIssuedInvitation
  readonly tenant: string
}

const MEMBER_SOURCES = new Set<string>(['admin', 'initial', 'invite'])
const JIT_SOURCE_PREFIX = 'jit:'

function expiryInstant(days: number): string {
  return new Date(Date.now() + days * DAY_MS).toISOString()
}

/** Props for InvitationsPage: the optional controlled tenant scope. */
export interface InvitationsPageProps {
  /** A controlled tenant; given, the reads and writes name it and the
   * selector is hidden. Empty string is a controlled "no tenant". */
  readonly tenant?: string
  /** The initial tenant when not controlled; wins over the query parameter. */
  readonly defaultTenant?: string
  /** Reports each re-selection made through the page's own selector. */
  readonly onTenantChange?: (tenant: string) => void
  /** Tenant mode (the organization surface): omits the member write form,
   * whose account field reads the platform roster, so the page issues no
   * `/api/v1/system` request. */
  readonly tenantMode?: boolean
}

/** InvitationsPage lists a tenant's invitations and members and drives their
 * writes. */
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
  const ssoExemption = hasFeature('authn.admin.sso')

  const [innerTenant, setInnerTenant] = useState(
    () => defaultTenant ?? tenantFromLocation(),
  )
  const tenant = controlledTenant ?? innerTenant
  const tenantControlled = controlledTenant !== undefined
  const [email, setEmail] = useState('')
  const [expiryDays, setExpiryDays] = useState<number>(DEFAULT_EXPIRY_DAYS)
  const [role, setRole] = useState('')
  const [statusFilter, setStatusFilter] =
    useState<InvitationStatusFilter>('pending')
  const [invitationCursorState, setInvitationCursorState] = useState<{
    readonly tenant: string
    readonly cursors: readonly string[]
    readonly page: number
  }>(() => ({ tenant, cursors: [''], page: 0 }))
  const invitationCursorFrame =
    invitationCursorState.tenant === tenant
      ? invitationCursorState
      : { tenant, cursors: [''], page: 0 }
  const invitationPage = invitationCursorFrame.page
  const invitationCursor =
    invitationCursorFrame.cursors[invitationPage] ?? ''
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
  const [issued, setIssued] = useState<IssuedNotice | null>(null)
  const [closeIssuedOpen, setCloseIssuedOpen] = useState(false)
  const confirm = usePendingConfirm()

  const invitationParams = useMemo(
    () => ({
      limit: INVITATION_PAGE_SIZE,
      ...(statusFilter === 'all' ? {} : { status: statusFilter }),
      ...(invitationCursor === '' ? {} : { after: invitationCursor }),
    }),
    [statusFilter, invitationCursor],
  )
  const invitations = operations.useInvitations(tenant, invitationParams)

  const tenantRoles = operations.useTenantRoles(tenant)
  const canManageRoles =
    tenantRoles.data?.some((candidate) => candidate.can_manage) === true
  useEffect(() => {
    if (!canManageRoles) {
      setRole('')
    }
  }, [canManageRoles])

  const members = operations.useMembers(tenant, {
    limit: MEMBER_PAGE_SIZE,
    ...(memberCursor === '' ? {} : { after: memberCursor }),
  })
  const memberRows = members.data?.members
  // Focus goes to the same control on the next row, else the previous row's,
  // else the list container — never the document body.
  useEffect(() => {
    const removed = removalFocus.current
    if (removed === null) {
      return
    }
    // A failed re-read never confirms the row left. Drop the record so a
    // later read (a page change, the retry) cannot move focus on its own.
    if (members.error !== null) {
      removalFocus.current = null
      return
    }
    if (memberRows === undefined) {
      return
    }
    if (memberRows.some((row) => row.account === removed.account)) {
      return
    }
    removalFocus.current = null
    const list = memberListRef.current
    if (list === null) {
      return
    }
    // Only recover focus the removed control left on the body or the modal
    // paper. A reader who moved elsewhere has already chosen a resting place.
    const active = document.activeElement
    if (
      !(
        active === null ||
        active === document.body ||
        (active instanceof Element && active.getAttribute('role') === 'dialog')
      )
    ) {
      return
    }
    const controls = Array.from(
      list.querySelectorAll<HTMLElement>('[data-member-remove]'),
    )
    const target = controls[removed.index] ?? controls[removed.index - 1]
    if (target !== undefined) {
      target.focus()
      return
    }
    list.focus()
  }, [memberRows, members.error])
  const issueInvitation = operations.useIssueInvitation()
  const revokeInvitation = operations.useRevokeInvitation()
  const resendInvitation = operations.useResendInvitation()
  const removeMember = operations.useRemoveMember()
  const setMemberSsoExempt = operations.useSetMemberSsoExempt()

  const invitationFeedback = useWriteFeedback()
  const membersFeedback = useWriteFeedback()
  const invitationRegionRef = useFailureScroll(invitationFeedback.outcome)
  const membersRegionRef = useFailureScroll(membersFeedback.outcome)
  const {
    sectionRef: invitationSectionRef,
    focusHeading: focusInvitationHeading,
  } = useSectionHeadingFocus()

  const [failedInvitation, setFailedInvitation] = useState<string | null>(null)
  const [failedMember, setFailedMember] = useState<{
    readonly account: string
    readonly action: 'remove' | 'sso'
  } | null>(null)
  const [pendingMemberWrite, setPendingMemberWrite] = useState<{
    readonly account: string
    readonly action: 'remove' | 'sso'
  } | null>(null)
  const [pendingInvitationWrite, setPendingInvitationWrite] = useState<
    | { readonly kind: 'issue' }
    | { readonly kind: 'resend'; readonly id: string }
    | { readonly kind: 'revoke'; readonly id: string }
    | null
  >(null)
  const memberListRef = useRef<HTMLDivElement | null>(null)
  const removalFocus = useRef<{
    readonly account: string
    readonly index: number
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

  const resetPageFeedback = useCallback(() => {
    invitationFeedback.reset()
    membersFeedback.reset()
    setFailedInvitation(null)
    setFailedMember(null)
  }, [invitationFeedback.reset, membersFeedback.reset])
  useEffect(() => {
    resetPageFeedback()
  }, [tenant, resetPageFeedback])
  useEffect(() => {
    setMemberCursorState({ tenant, cursors: [''], page: 0 })
    setInvitationCursorState({ tenant, cursors: [''], page: 0 })
    setRole('')
    setCloseIssuedOpen(false)
    removalFocus.current = null
    setPendingMemberWrite(null)
  }, [tenant])
  const changeTenant = (next: string) => {
    setInnerTenant(next)
    onTenantChange?.(next)
  }

  const changeInvitationStatusFilter = (next: InvitationStatusFilter) => {
    setStatusFilter(next)
    setInvitationCursorState({ tenant, cursors: [''], page: 0 })
    invitationFeedback.reset()
    setFailedInvitation(null)
  }

  const relogin = useCallback(() => {
    void session?.logout().catch(() => undefined)
  }, [session])

  const pageRefresh = usePageRefresh(
    [
      pageRefreshRead('invitations', invitations),
      pageRefreshRead('members', members),
    ],
    resetPageFeedback,
  )

  const issue = () => {
    const address = email.trim()
    const grantedRole = role
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
          ...(canManageRoles && grantedRole !== ''
            ? { role: grantedRole }
            : {}),
        })
        setEmail('')
        setRole('')
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

  const requestRemoveMember = (
    member: AdminMember,
    identity: AccountIdentityValue,
  ) => {
    confirm.ask({
      title: t('members.removeTitle'),
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
        setPendingMemberWrite({ account: member.account, action: 'remove' })
        void membersFeedback
          .run(async () => {
            try {
              await removeMember.mutateAsync({
                tenant,
                account: member.account,
              })
              const rows = memberRows ?? []
              removalFocus.current = {
                account: member.account,
                index: rows.findIndex((row) => row.account === member.account),
              }
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
          .finally(() => setPendingMemberWrite(null))
      },
    })
  }

  const toggleSsoExempt = (
    member: AdminMember,
    identity: AccountIdentityValue,
  ) => {
    const exempt = !member.sso_exempt
    setFailedMember(null)
    setPendingMemberWrite({ account: member.account, action: 'sso' })
    void membersFeedback
      .run(async () => {
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
      .finally(() => setPendingMemberWrite(null))
  }

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
        const status = invitation.state
        return (
          <StatusTag
            label={statusLabel(status, t)}
            tone={statusTone(status)}
            note={terminalNote(invitation, status, t, formatTime)}
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
      cellWrap: 'nowrap',
      contentMaxWidth: INVITATION_IDENTITY_MAX_WIDTH.invitedBy,
      cell: (invitation) =>
        invitation.invited_by_name === undefined ||
        invitation.invited_by_name === '' ? null : (
          <Typography
            variant="body2"
            noWrap
            title={identityLabel(
              invitation.invited_by_name,
              invitation.invited_by_username,
            )}
          >
            {invitation.invited_by_name}
          </Typography>
        ),
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
          <Box>
            {formatTime(invitation.expires_at)}
            {remaining !== undefined && (
              <Typography
                variant="caption"
                color="text.secondary"
                sx={{ display: 'block', mt: 0.5 }}
              >
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
        if (invitation.state !== 'pending') {
          return null
        }
        const failed = failedInvitation === invitation.id
        const errorId = `invitation-error-${encodeURIComponent(invitation.id)}`
        const current =
          issued !== null && issued.answer.invitation.id === invitation.id
        const busy = invitationFeedback.pending
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
                disabled={busy}
                actions={[
                  {
                    key: 'resend',
                    label: t('invitations.resend'),
                    onSelect: () => requestResend(invitation),
                    disabled: busy,
                  },
                  {
                    key: 'revoke',
                    label: t('invitations.revoke'),
                    onSelect: () => requestRevoke(invitation),
                    disabled: busy,
                  },
                ]}
              />
              {current && issuedValue !== '' && (
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

  const memberColumns: DataTableColumn<AdminMember>[] = [
    {
      id: 'displayName',
      header: t('members.column.displayName'),
      cellWrap: 'nowrap',
      cellFullValue: (member) =>
        member.display_name === '' ? undefined : member.display_name,
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (member) => member.display_name,
    },
    {
      id: 'username',
      header: t('members.column.username'),
      priority: 'high',
      cellWrap: 'nowrap',
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
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
          pendingWrite={pendingMemberWrite}
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

  const invitationNextCursor = invitations.data?.next_cursor ?? ''
  const invitationHasNext = invitationNextCursor !== ''
  const nextInvitationPage = () => {
    if (
      !invitationHasNext ||
      invitationNextCursor === '' ||
      invitationFeedback.pending ||
      invitations.isFetching
    ) {
      return
    }
    const next = invitationNextCursor
    setInvitationCursorState((previous) => {
      const current =
        previous.tenant === tenant
          ? previous
          : { tenant, cursors: [''], page: 0 }
      const grown = [...current.cursors]
      grown[current.page + 1] = next
      return { tenant, cursors: grown, page: current.page + 1 }
    })
  }
  const previousInvitationPage = () => {
    if (invitationFeedback.pending || invitations.isFetching) {
      return
    }
    setInvitationCursorState((previous) => {
      const current =
        previous.tenant === tenant
          ? previous
          : { tenant, cursors: [''], page: 0 }
      return current.page === 0
        ? current
        : { ...current, page: current.page - 1 }
    })
  }
  const requestCloseIssued = () => {
    if (issued === null) {
      return
    }
    if (issued.answer.token === undefined) {
      setIssued(null)
      return
    }
    setCloseIssuedOpen(true)
  }

  const issuedToken = issued?.answer.token
  const issuedValue =
    issuedToken === undefined
      ? ''
      : invitationLink === undefined
        ? issuedToken
        : acceptanceUrl(invitationLink(issuedToken))

  let roleHelper: ReactNode
  if (!canManageRoles || !tenantRoles.isError) {
    roleHelper = undefined
  } else {
    roleHelper = (
      <Box component="span">
        {t('invitations.issue.rolesError')}{' '}
        <Button
          type="button"
          size="small"
          variant="text"
          onClick={() => {
            void tenantRoles.refetch()
          }}
        >
          {t('invitations.retry')}
        </Button>
      </Box>
    )
  }

  const issueExplanation: ActionButtonUnavailableExplanation | undefined =
    email.trim() === ''
      ? { unavailableReason: t('invitations.issue.emailRequired') }
      : undefined

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
          <SectionCard
            ref={invitationSectionRef}
            title={t('invitations.section.title')}
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
              {canManageRoles && (
                <TextField
                  select
                  size="small"
                  value={role}
                  onChange={(event) => setRole(event.target.value)}
                  label={t('invitations.issue.roleLabel')}
                  helperText={roleHelper}
                  sx={{ minWidth: 180 }}
                >
                  <MenuItem value="">{t('invitations.issue.roleNone')}</MenuItem>
                  {(tenantRoles.data ?? []).map((candidate) => (
                    <MenuItem key={candidate.role} value={candidate.role}>
                      {candidate.role}
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
            {issued != null && (
              <InvitationLinkNotice
                key={issuedValue === '' ? issued.answer.invitation.id : issuedValue}
                notice={issued}
                value={issuedValue}
                hasBuilder={invitationLink !== undefined}
                onClose={requestCloseIssued}
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
                />
              )}
            </AdminAsyncSection>
            {invitationRows !== undefined && (
              <CursorPager
                page={invitationPage}
                hasNext={invitationHasNext}
                busy={invitationFeedback.pending}
                reading={invitations.isFetching}
                onPrevious={previousInvitationPage}
                onNext={nextInvitationPage}
              />
            )}
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
            <Box
              ref={memberListRef}
              role="group"
              aria-label={t('members.title')}
              tabIndex={-1}
            >
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
                  busy={membersFeedback.pending}
                  reading={members.isFetching}
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
            </Box>
          </SectionCard>
        </>
      )}

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />

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

interface MemberRowActionsProps {
  readonly member: AdminMember
  readonly failed: 'remove' | 'sso' | null
  readonly failureText: string | null
  readonly pending: boolean
  readonly pendingWrite: {
    readonly account: string
    readonly action: 'remove' | 'sso'
  } | null
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

function MemberRowActions(props: MemberRowActionsProps) {
  return (
    <MemberActionsBody {...props} identity={memberRowIdentity(props.member)} />
  )
}

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
  pendingWrite,
  ssoExemption,
  onRemove,
  onToggleSsoExempt,
  identity,
}: MemberRowActionsProps & { readonly identity: AccountIdentityValue }) {
  const { t } = useAdminUiTranslation()
  const errorId = `member-error-${encodeURIComponent(member.account)}`
  const ssoFailed = failed === 'sso'
  const removeFailed = failed === 'remove'
  const ssoBusy =
    pendingWrite !== null &&
    pendingWrite.account === member.account &&
    pendingWrite.action === 'sso'
  const removeBusy =
    pendingWrite !== null &&
    pendingWrite.account === member.account &&
    pendingWrite.action === 'remove'
  return (
    <Box>
      <Box sx={{ display: 'flex', columnGap: 1 }}>
        {ssoExemption && (
          <ActionButton
            type="button"
            size="small"
            aria-invalid={ssoFailed}
            aria-describedby={ssoFailed ? errorId : undefined}
            busy={ssoBusy}
            unavailable={pending && !ssoBusy}
            onClick={() => onToggleSsoExempt(member, identity)}
          >
            {member.sso_exempt
              ? t('members.ssoExemptClear')
              : t('members.ssoExemptSet')}
          </ActionButton>
        )}
        <ActionButton
          type="button"
          size="small"
          tone="danger"
          data-member-remove={member.account}
          aria-invalid={removeFailed}
          aria-describedby={removeFailed ? errorId : undefined}
          busy={removeBusy}
          unavailable={pending && !removeBusy}
          onClick={() => onRemove(member, identity)}
        >
          {t('members.remove')}
        </ActionButton>
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

function acceptanceUrl(link: string): string {
  try {
    return new URL(link, window.location.origin).toString()
  } catch {
    return link
  }
}

function InvitationLinkNotice({
  notice,
  value,
  hasBuilder,
  onClose,
}: {
  readonly notice: IssuedNotice
  readonly value: string
  readonly hasBuilder: boolean
  readonly onClose: () => void
}) {
  const { t } = useAdminUiTranslation()
  const emailed = notice.answer.token === undefined
  return (
    <OneTimeValuePanel
      message={
        emailed ? (
          t('invitations.issued.emailed', {
            email: notice.answer.invitation.email,
          })
        ) : (
          <>
            {hasBuilder
              ? t('invitations.issued.link', {
                  email: notice.answer.invitation.email,
                })
              : t('invitations.issued.token', {
                  email: notice.answer.invitation.email,
                })}
            <Typography
              component="span"
              variant="body2"
              color={
                notice.answer.email_delivery === 'failed' ? 'error' : 'text.secondary'
              }
              sx={{ display: 'block', mt: 0.5 }}
            >
              {t(`invitations.issued.delivery.${notice.answer.email_delivery}`)}
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
        email: notice.answer.invitation.email,
      })}
      onClose={onClose}
    />
  )
}

function MemberWriteForm({
  tenant,
  feedback,
  ssoExemption,
}: {
  readonly tenant: string
  readonly feedback: WriteFeedback
  readonly ssoExemption: boolean
}) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const writeMember = operations.useWriteMember()

  const [account, setAccount] = useState<AdminAccount | null>(null)
  const [source, setSource] = useState('')
  const [active, setActive] = useState(true)
  const [ssoExempt, setSsoExempt] = useState(false)
  const [busy, setBusy] = useState(false)
  const scopePending = feedback.pending && !busy

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
        <ActionButton
          type="button"
          emphasis="primary"
          busy={busy}
          unavailable={account === null || scopePending}
          unavailableReason={
            account === null ? t('members.write.accountRequired') : undefined
          }
          onClick={() => {
            if (account === null) {
              return
            }
            const target = account
            const writtenSource = source
            setBusy(true)
            void feedback
              .run(async () => {
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
              .finally(() => setBusy(false))
          }}
        >
          {t('members.write.submit')}
        </ActionButton>
      </Box>
    </Box>
  )
}
