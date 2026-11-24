/**
 * The member management panel: summary, bindings and membership writes; the role
 * group degrades by permission and a bind is blocked on any out-of-set grant.
 */

import { useEffect, useMemo, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useAuthState, usePermission } from '@speed/auth-core'
import {
  ActionButton,
  ListSkeleton,
  SidePanel,
  useFormatInstant,
  useInertSelectMenu,
  useWriteFeedback,
} from '@speed/ui-kit'
import type { ActionButtonUnavailableExplanation } from '@speed/ui-kit'
import type { AdminMember } from '../operations.js'
import type { AdminTenantBinding } from '../organization-operations.js'
import { useOrganizationAdminOperations } from '../organization-operations.js'
import { useOrganizationAdminProtectedRoles } from '../OrganizationAdminProvider.js'
import { identityLabel } from './account-identity.js'
import { AdminAsyncSection } from './async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './confirm.js'
import { useAdminUiTranslation } from './translation.js'
import {
  failureTextOf,
  useFailureScroll,
  WriteFeedbackRegion,
} from './write-feedback.js'
import { useAdminUiErrorText } from './error-text.js'
import { useRoleEscalationCheck } from './role-escalation.js'
import {
  focusAfterRowRemoval,
  useRowRemovalFocus,
} from './focus-after-removal.js'

type RoleTier = 'none' | 'read' | 'manage'

/** The texts that explain why the bind control cannot run. */
const SUSPENDED_BIND_NOTE_ID = 'member-panel-suspended-bind-note'
const ROLE_CHECKING_ID = 'member-panel-role-checking'
const ROLE_FAILED_ID = 'member-panel-role-failed'
const ROLE_UNCONFIRMED_ID = 'member-panel-role-unconfirmed'
const ROLE_BLOCKED_ID = 'member-panel-role-blocked'
/** The note explaining why the current account cannot suspend or remove itself. */
const SELF_NOTE_ID = 'member-panel-self-note'

/** The write a failure belongs to, so the panel can mark that control. */
type FailedWrite =
  | { readonly kind: 'bind' }
  | { readonly kind: 'unbind'; readonly role: string }
  | { readonly kind: 'suspend' }
  | { readonly kind: 'resume' }
  | { readonly kind: 'remove' }

export interface MemberPanelProps {
  /** Whether the panel is shown; the page owns the state. */
  readonly open: boolean
  /** The current tenant every read and write names. */
  readonly tenant: string
  /** The member whose summary, bindings and writes the panel shows. */
  readonly member: AdminMember
  /** Reports every close request; the page flips `open`. */
  readonly onClose: () => void
  /** Reports the exit transition's end; the page drops the member then. */
  readonly onExited: () => void
  /** Reports a successful removal with the message the list's scope should carry. */
  readonly onRemoved: (message: string) => void
}

function roleTierOf(
  canRead: boolean,
  canManage: boolean,
): RoleTier {
  if (!canRead) {
    return 'none'
  }
  return canManage ? 'manage' : 'read'
}

export function MemberPanel({
  open,
  tenant,
  member,
  onClose,
  onExited,
  onRemoved,
}: MemberPanelProps) {
  const { t } = useAdminUiTranslation()
  const operations = useOrganizationAdminOperations()
  const formatTime = useFormatInstant()
  const auth = useAuthState()
  const canReadRoles = usePermission('tenant', 'authz.tenant.role.read')
  const selfAccount = auth.principal?.account_id
  const isSelf = selfAccount === member.account
  // The host names the role a caller may not take away from itself; the panel
  // disables it before the request instead of offering a write bound to fail.
  const protectedRoles = useOrganizationAdminProtectedRoles()
  const roleMenu = useInertSelectMenu()

  const [selectedRole, setSelectedRole] = useState('')
  const {
    canManageRoles,
    roleSelected,
    grants,
    grantsPending,
    grantsFailed,
    grantPermissionUnconfirmed,
    escalationBlocked,
  } = useRoleEscalationCheck(tenant, selectedRole)
  const tier = roleTierOf(canReadRoles, canManageRoles)
  const bindings = operations.useTenantAccountBindings(
    canReadRoles ? tenant : '',
    member.account,
  )
  // The role list exists only where the role control does.
  const roles = operations.useTenantRoles(canManageRoles ? tenant : '')
  // A held role is not offered again; the exclusion reads only a settled
  // bindings answer.
  const boundRoles = useMemo(
    () => new Set((bindings.data ?? []).map((binding) => binding.role)),
    [bindings.data],
  )
  const availableRoles = useMemo(
    () => (roles.data ?? []).filter((role) => !boundRoles.has(role.role)),
    [roles.data, boundRoles],
  )
  const allRolesBound =
    roles.data !== undefined &&
    roles.data.length > 0 &&
    bindings.data !== undefined &&
    availableRoles.length === 0

  const [failed, setFailed] = useState<FailedWrite | null>(null)
  // The control the in-flight write belongs to; the others are unavailable.
  const [pendingWrite, setPendingWrite] = useState<FailedWrite | null>(null)
  // The binding row a landed unbind removed, with the position it held.
  const unbindRemoval = useRef<{ role: string; index: number } | null>(null)
  // Whether the unbind control held focus when its row left.
  const unbindFocus = useRowRemovalFocus()
  // The roles heading, the stable focus target when no binding row is adjacent.
  const rolesHeadingRef = useRef<HTMLHeadingElement | null>(null)
  const feedback = useWriteFeedback()
  const regionRef = useFailureScroll(feedback.outcome)
  const resolveErrorText = useAdminUiErrorText()
  const failureText = failureTextOf(feedback.outcome, resolveErrorText)
  const confirm = usePendingConfirm()

  // A landed unbind moves focus to the next row's control, else the previous
  // row's, else the roles heading; a failed re-read drops the record.
  useEffect(() => {
    const removed = unbindRemoval.current
    if (removed === null) {
      return
    }
    if (bindings.error !== null) {
      unbindRemoval.current = null
      return
    }
    if (bindings.data === undefined) {
      return
    }
    if (bindings.data.some((binding) => binding.role === removed.role)) {
      return
    }
    unbindRemoval.current = null
    focusAfterRowRemoval(
      'data-member-unbind',
      removed.role,
      removed.index,
      unbindFocus.takeHeldFocus(removed.role),
      () => rolesHeadingRef.current?.focus(),
    )
  }, [bindings.data, bindings.error, unbindFocus.takeHeldFocus])

  const bindRole = operations.useTenantBindRole()
  const unbindRole = operations.useTenantUnbindRole()
  const writeMember = operations.useWriteMember()
  const removeMember = operations.useRemoveMember()

  const displayName =
    member.display_name === '' ? undefined : member.display_name
  const composedName = identityLabel(displayName, member.username)
  const memberName = composedName ?? t('common.accountUnavailable')
  const statusLabel = member.active
    ? t('members.status.active')
    : t('members.status.suspended')

  // The bind control's unmet preconditions, in the order an operator meets them.
  const bindExplanation: ActionButtonUnavailableExplanation | undefined =
    !member.active
      ? { unavailableDescribedBy: SUSPENDED_BIND_NOTE_ID }
      : !roleSelected
        ? { unavailableReason: t('members.panel.bindNoRole') }
        : grantsPending
          ? { unavailableDescribedBy: ROLE_CHECKING_ID }
          : grantsFailed
            ? { unavailableDescribedBy: ROLE_FAILED_ID }
            : grantPermissionUnconfirmed
              ? { unavailableDescribedBy: ROLE_UNCONFIRMED_ID }
              : escalationBlocked
                ? { unavailableDescribedBy: ROLE_BLOCKED_ID }
                : undefined

  const bindingFailure =
    failed?.kind === 'unbind' && failureText !== null ? failureText : null
  const bindFailure = failed?.kind === 'bind' ? failureText : null
  const suspendFailure = failed?.kind === 'suspend' ? failureText : null
  const resumeFailure = failed?.kind === 'resume' ? failureText : null
  const removeFailure = failed?.kind === 'remove' ? failureText : null
  const removeConflict =
    failed?.kind === 'remove' &&
    feedback.outcome !== null &&
    feedback.outcome.kind === 'failure' &&
    feedback.outcome.code === 'authn.conflict'

  const startWrite = (next: FailedWrite | null) => {
    setFailed(next)
  }

  const bind = () => {
    if (selectedRole === '') {
      return
    }
    const role = selectedRole
    startWrite(null)
    setPendingWrite({ kind: 'bind' })
    void feedback
      .run(async () => {
        try {
          await bindRole.mutateAsync({
            tenant,
            account: member.account,
            role,
          })
          setSelectedRole('')
          return displayName === undefined
            ? t('members.panel.bindSavedUnnamed', { role })
            : t('members.panel.bindSaved', {
                account: displayName,
                role,
              })
        } catch (error) {
          startWrite({ kind: 'bind' })
          throw error
        }
      })
      .finally(() => setPendingWrite(null))
  }

  const unbind = (role: string) => {
    startWrite(null)
    unbindRemoval.current = {
      role,
      index: bindings.data?.findIndex((row) => row.role === role) ?? -1,
    }
    setPendingWrite({ kind: 'unbind', role })
    void feedback
      .run(async () => {
        try {
          await unbindRole.mutateAsync({
            tenant,
            account: member.account,
            role,
          })
          return displayName === undefined
            ? t('members.panel.bindRemovedUnnamed', { role })
            : t('members.panel.bindRemoved', {
                account: displayName,
                role,
              })
        } catch (error) {
          unbindRemoval.current = null
          startWrite({ kind: 'unbind', role })
          throw error
        }
      })
      .finally(() => setPendingWrite(null))
  }

  /**
   * Ask before unbinding: the role's permissions stop applying at once. The
   * confirmation also names the two refusals the operator cannot see from here:
   * a caller's own protected binding, and the tenant's last tenant-admin binding.
   */
  const requestUnbind = (role: string) => {
    confirm.ask({
      title: t('members.panel.unbindTitle'),
      message: t('members.panel.unbindMessage', { role }),
      confirmLabel: t('common.confirm'),
      run: () => unbind(role),
    })
  }

  const requestActive = (active: boolean) => {
    confirm.ask({
      title: active ? t('members.resumeTitle') : t('members.suspendTitle'),
      message:
        displayName === undefined
          ? active
            ? t('members.resumeMessageUnnamed')
            : t('members.suspendMessageUnnamed')
          : active
            ? t('members.resumeMessage', { name: displayName })
            : t('members.suspendMessage', { name: displayName }),
      confirmLabel: t('common.confirm'),
      run: () => {
        startWrite(null)
        setPendingWrite({ kind: active ? 'resume' : 'suspend' })
        void feedback
          .run(async () => {
            try {
              await writeMember.mutateAsync({
                tenant,
                account: member.account,
                active,
              })
              return active
                ? t('members.resumed', { account: memberName })
                : t('members.suspended', { account: memberName })
            } catch (error) {
              startWrite({ kind: active ? 'resume' : 'suspend' })
              throw error
            }
          })
          .finally(() => setPendingWrite(null))
      },
    })
  }

  const requestRemove = () => {
    const message =
      displayName === undefined
        ? t('members.removeMessageUnnamed')
        : t('members.removeMessage', { name: displayName })
    confirm.ask({
      title: t('members.removeTitle'),
      message: isSelf ? `${message} ${t('members.removeSelfNote')}` : message,
      confirmLabel: t('common.confirm'),
      doubleConfirm: true,
      run: () => {
        startWrite(null)
        setPendingWrite({ kind: 'remove' })
        void feedback
          .run(async () => {
            try {
              await removeMember.mutateAsync({
                tenant,
                account: member.account,
              })
              onRemoved(
                composedName === undefined
                  ? t('members.removedUnnamed')
                  : t('members.removed', { account: composedName }),
              )
              return null
            } catch (error) {
              startWrite({ kind: 'remove' })
              throw error
            }
          })
          .finally(() => setPendingWrite(null))
      },
    })
  }

  const bindingRows = (rows: readonly AdminTenantBinding[]) =>
    rows.length === 0 ? (
      <Typography variant="body2" color="text.secondary">
        {t('members.panel.rolesEmpty')}
      </Typography>
    ) : (
      <Box component="ul" sx={{ listStyle: 'none', m: 0, p: 0 }}>
        {rows.map((binding) => {
          const refused =
            failed?.kind === 'unbind' && failed.role === binding.role
          const errorId = `binding-error-${encodeURIComponent(binding.role)}`
          // The current account cannot take its own protected binding away;
          // disable the control with the reason shown.
          const ownProtected =
            isSelf && protectedRoles.includes(binding.role)
          const ownProtectedId = `member-own-protected-${encodeURIComponent(
            binding.role,
          )}`
          const unbindBusy =
            pendingWrite?.kind === 'unbind' &&
            pendingWrite.role === binding.role
          return (
            <Box
              component="li"
              key={binding.role}
              sx={{
                display: 'flex',
                flexWrap: 'wrap',
                alignItems: 'center',
                columnGap: 1,
                rowGap: 0.5,
                py: 0.75,
                borderBottom: 1,
                borderColor: 'divider',
                '&:last-of-type': { borderBottom: 0 },
              }}
            >
              <Chip size="small" variant="outlined" label={binding.role} />
              {tier === 'manage' && (
                <ActionButton
                  ref={unbindFocus.controlRef(binding.role)}
                  type="button"
                  size="small"
                  aria-invalid={refused}
                  aria-describedby={refused ? errorId : undefined}
                  busy={unbindBusy}
                  unavailable={
                    ownProtected || (pendingWrite !== null && !unbindBusy)
                  }
                  unavailableDescribedBy={
                    ownProtected ? ownProtectedId : undefined
                  }
                  data-member-unbind={binding.role}
                  onClick={() => requestUnbind(binding.role)}
                >
                  {t('members.panel.unbind')}
                </ActionButton>
              )}
              {ownProtected && (
                <Typography
                  id={ownProtectedId}
                  variant="body2"
                  color="text.secondary"
                  sx={{ flexBasis: '100%' }}
                >
                  {t('members.panel.cannotUnbindOwnAdmin')}
                </Typography>
              )}
              {refused && bindingFailure !== null && (
                <Typography
                  id={errorId}
                  variant="caption"
                  color="error"
                  sx={{ flexBasis: '100%' }}
                >
                  {bindingFailure}
                </Typography>
              )}
            </Box>
          )
        })}
      </Box>
    )

  return (
    <SidePanel
      open={open}
      title={t('members.panel.title')}
      description={memberName}
      busy={feedback.pending}
      closeLabel={
        composedName === undefined
          ? t('members.panel.close')
          : t('members.panel.closeAria', { name: composedName })
      }
      onClose={onClose}
      onExited={onExited}
    >
      <Typography component="h3" variant="subtitle1">
        {t('members.panel.summary')}
      </Typography>
      <Box sx={{ mt: 1, mb: 2 }}>
        {member.username !== '' && (
          <Typography variant="body2">
            {t('members.panel.field', {
              label: t('members.panel.username'),
              value: member.username,
            })}
          </Typography>
        )}
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            columnGap: 1,
            mt: 0.5,
          }}
        >
          <Typography variant="body2">
            {t('members.panel.status')}
          </Typography>
          <Chip
            size="small"
            variant="outlined"
            color={member.active ? 'success' : 'default'}
            label={statusLabel}
          />
        </Box>
        <Typography variant="body2" sx={{ mt: 0.5 }}>
          {t('members.panel.field', {
            label: t('members.panel.joinedAt'),
            value: formatTime(member.joined_at),
          })}
        </Typography>
      </Box>

      <Typography
        ref={rolesHeadingRef}
        component="h3"
        variant="subtitle1"
        tabIndex={-1}
        sx={{ mb: 1 }}
      >
        {t('members.panel.rolesLabel')}
      </Typography>
      {tier === 'none' ? (
        <Typography variant="body2" color="text.secondary">
          {t('members.panel.rolesNoRead')}
        </Typography>
      ) : (
        <>
          <AdminAsyncSection
            pending={bindings.isPending && bindings.data === undefined}
            payload={bindings.data}
            error={bindings.error}
            refreshing={bindings.isFetching}
            empty={false}
            headingLevel="h3"
            loading={
              <ListSkeleton
                label={t('members.panel.rolesLoading')}
                rows={[<Skeleton key="a" variant="text" width="48%" />]}
              />
            }
            errorState={{
              title: t('members.panel.rolesErrorTitle'),
              description: t('members.panel.rolesErrorDescription'),
              retryLabel: t('members.panel.rolesRetry'),
              onRetry: () => void bindings.refetch(),
            }}
          >
            {bindingRows}
          </AdminAsyncSection>
          {tier === 'read' && (
            <Typography
              variant="body2"
              color="text.secondary"
              sx={{ mt: 1 }}
            >
              {t('members.panel.rolesReadOnly')}
            </Typography>
          )}
          {tier === 'manage' && (
            <Box sx={{ mt: 2 }}>
              {roles.isError && (
                <Box sx={{ mb: 1 }}>
                  <Typography variant="body2" color="error">
                    {t('members.panel.rolesListError')}
                  </Typography>
                  <Button
                    type="button"
                    size="small"
                    variant="outlined"
                    onClick={() => void roles.refetch()}
                  >
                    {t('members.panel.rolesRetry')}
                  </Button>
                </Box>
              )}
              <Box
                sx={{
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
                  value={selectedRole}
                  onChange={(event) => {
                    startWrite(null)
                    setSelectedRole(event.target.value)
                  }}
                  label={t('members.panel.roleLabel')}
                  // A write in flight makes the choice read-only: it stays
                  // focusable and the bind button beside it holds the focus.
                  slotProps={{
                    input: { readOnly: feedback.pending },
                    select: roleMenu,
                  }}
                  // The roles read and the all-roles-bound state keep the native
                  // `disabled`; neither is caused by an action on this control.
                  disabled={roles.isPending || allRolesBound}
                  sx={{ minWidth: 200 }}
                >
                  <MenuItem value="">{t('members.panel.roleNone')}</MenuItem>
                  {availableRoles.map((role) => (
                    <MenuItem key={role.role} value={role.role}>
                      {role.role}
                    </MenuItem>
                  ))}
                </TextField>
                <ActionButton
                  type="button"
                  emphasis="primary"
                  aria-invalid={failed?.kind === 'bind'}
                  aria-describedby={
                    failed?.kind === 'bind' ? 'member-bind-error' : undefined
                  }
                  busy={pendingWrite?.kind === 'bind'}
                  unavailable={
                    bindExplanation !== undefined ||
                    (pendingWrite !== null && pendingWrite.kind !== 'bind')
                  }
                  {...(bindExplanation ?? {})}
                  onClick={bind}
                >
                  {t('members.panel.bind')}
                </ActionButton>
              </Box>
              {allRolesBound && (
                <Typography
                  variant="body2"
                  color="text.secondary"
                  sx={{ mt: 1 }}
                >
                  {t('members.panel.allRolesBound')}
                </Typography>
              )}
              {!member.active && (
                <Typography
                  id={SUSPENDED_BIND_NOTE_ID}
                  variant="body2"
                  color="text.secondary"
                  sx={{ mt: 1 }}
                >
                  {t('members.panel.suspendedBindNote')}
                </Typography>
              )}
              {grantsPending && (
                <Typography
                  id={ROLE_CHECKING_ID}
                  variant="body2"
                  color="text.secondary"
                  sx={{ mt: 1 }}
                >
                  {t('members.panel.escalationChecking')}
                </Typography>
              )}
              {grantsFailed && (
                <Box sx={{ mt: 1 }}>
                  <Typography
                    id={ROLE_FAILED_ID}
                    variant="body2"
                    color="error"
                  >
                    {t('members.panel.escalationFailed')}
                  </Typography>
                  <Button
                    type="button"
                    size="small"
                    variant="outlined"
                    onClick={() => void grants.refetch()}
                  >
                    {t('members.panel.escalationRetry')}
                  </Button>
                </Box>
              )}
              {grantPermissionUnconfirmed ? (
                <Typography
                  id={ROLE_UNCONFIRMED_ID}
                  variant="body2"
                  color="error"
                  sx={{ mt: 1 }}
                >
                  {t('members.panel.escalationUnconfirmed')}
                </Typography>
              ) : (
                escalationBlocked && (
                  <Typography
                    id={ROLE_BLOCKED_ID}
                    variant="body2"
                    color="error"
                    sx={{ mt: 1 }}
                  >
                    {t('members.panel.escalationBlocked')}
                  </Typography>
                )
              )}
              {bindFailure !== null && (
                <Typography
                  id="member-bind-error"
                  variant="caption"
                  color="error"
                  sx={{ display: 'block', mt: 0.5 }}
                >
                  {bindFailure}
                </Typography>
              )}
            </Box>
          )}
        </>
      )}

      <Typography component="h3" variant="subtitle1" sx={{ mt: 3, mb: 1 }}>
        {t('members.panel.membership')}
      </Typography>
      <Box
        sx={{
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'center',
          columnGap: 1,
          rowGap: 1,
        }}
      >
        <ActionButton
          type="button"
          aria-invalid={failed?.kind === 'suspend' || failed?.kind === 'resume'}
          aria-describedby={
            failed?.kind === 'suspend' || failed?.kind === 'resume'
              ? 'member-status-error'
              : undefined
          }
          busy={pendingWrite?.kind === (member.active ? 'suspend' : 'resume')}
          unavailable={
            (isSelf && member.active) ||
            (pendingWrite !== null &&
              pendingWrite.kind !== (member.active ? 'suspend' : 'resume'))
          }
          unavailableDescribedBy={
            isSelf && member.active ? SELF_NOTE_ID : undefined
          }
          onClick={() => requestActive(!member.active)}
        >
          {member.active
            ? t('members.panel.suspend')
            : t('members.panel.resume')}
        </ActionButton>
        <ActionButton
          type="button"
          tone="danger"
          aria-invalid={failed?.kind === 'remove'}
          aria-describedby={
            failed?.kind === 'remove' ? 'member-remove-error' : undefined
          }
          busy={pendingWrite?.kind === 'remove'}
          unavailable={
            isSelf || (pendingWrite !== null && pendingWrite.kind !== 'remove')
          }
          unavailableDescribedBy={isSelf ? SELF_NOTE_ID : undefined}
          onClick={requestRemove}
        >
          {t('members.remove')}
        </ActionButton>
      </Box>
      {isSelf && (
        <Typography
          id={SELF_NOTE_ID}
          variant="body2"
          color="text.secondary"
          sx={{ mt: 1 }}
        >
          {t('members.panel.cannotSuspendSelf')}
        </Typography>
      )}
      {(suspendFailure !== null || resumeFailure !== null) && (
        <Typography
          id="member-status-error"
          variant="caption"
          color="error"
          sx={{ display: 'block', mt: 0.5 }}
        >
          {suspendFailure ?? resumeFailure}
        </Typography>
      )}
      {removeFailure !== null && (
        <Typography
          id="member-remove-error"
          variant="caption"
          color="error"
          sx={{ display: 'block', mt: 0.5 }}
        >
          {removeFailure}
        </Typography>
      )}

      <Box ref={regionRef} sx={{ mt: 2 }}>
        <WriteFeedbackRegion outcome={feedback.outcome} />
        {removeConflict && (
          <Typography variant="body2" color="error" sx={{ mt: 0.5 }}>
            {t('members.lastAdminConflict')}
          </Typography>
        )}
      </Box>

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </SidePanel>
  )
}
