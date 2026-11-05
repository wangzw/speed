/**
 * The member management panel: one member's summary, role bindings and
 * membership writes.
 *
 * The page opens the panel from a member row's Manage control. The panel
 * reads the member's bindings through the tenant-scoped operations bundle
 * and offers three groups of writes:
 *
 *  - bind a role and unbind an existing one;
 *  - suspend or resume the membership;
 *  - remove the membership, behind the heavier double confirmation.
 *
 * The role group degrades by the caller's tenant permissions, read through
 * @speed/auth-core's permission set:
 *
 *  - without `authz.tenant.role.read` the panel says so and reads no
 *    bindings, rendering no role control at all (fail-closed: an unknown
 *    or failed permission read is this tier);
 *  - with read but not `authz.tenant.role.manage` the bindings stay
 *    readable and every bind/unbind control is withheld;
 *  - with manage, the complete control set renders.
 *
 * Binding a role is checked before the request: the panel reads the
 * selected role's grants and compares them with the caller's own tenant
 * permission set. A grant the caller does not hold (including one the
 * permission catalog cannot map) blocks the bind, so the surface never
 * offers to hand out more than its operator has. A failed grant read keeps
 * the bind blocked and offers a retry rather than defaulting to allowed.
 * Unbinding is never blocked by this check.
 *
 * All writes in the panel share one feedback scope. A failure both lands
 * in the scope and marks the control or binding row it was about; a
 * removal success is not written here -- the page owns the list's scope
 * and writes the message after the panel has animated out, when the page
 * behind it is reachable again.
 */

import { useMemo, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useAuthState, usePermission } from '@speed/auth-core'
import { formatInstant } from '@speed/i18n'
import {
  ListSkeleton,
  SidePanel,
  useWriteFeedback,
} from '@speed/ui-kit'
import type { AdminMember } from '../operations.js'
import type { AdminTenantBinding } from '../organization-operations.js'
import { useOrganizationAdminOperations } from '../organization-operations.js'
import { AdminAsyncSection } from './async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './confirm.js'
import { buildGrantCodeIndex } from './grant-code-index.js'
import { useAdminUiTranslation } from './translation.js'
import {
  failureTextOf,
  useFailureScroll,
  WriteFeedbackRegion,
} from './write-feedback.js'
import { useAdminUiErrorText } from './error-text.js'

/** The role permission tier the caller holds in this tenant. */
type RoleTier = 'none' | 'read' | 'manage'

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
  /**
   * Reports a successful removal with the message the list's scope should
   * carry. The page writes it after the panel has exited.
   */
  readonly onRemoved: (message: string) => void
}

/** The permission tier, read from the session's tenant permission set. */
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
  const { t, i18n } = useAdminUiTranslation()
  const operations = useOrganizationAdminOperations()
  const auth = useAuthState()
  // The permission set itself is what the escalation check compares
  // against; a set that is absent or a session that is not authenticated
  // reads as the empty set, which fails every grant closed.
  const permissionSet =
    auth.state === 'authenticated' ? auth.permissionSets.tenant : null
  const callerPermissions = useMemo(
    () => new Set(permissionSet ?? []),
    [permissionSet],
  )
  const canReadRoles = usePermission('tenant', 'authz.tenant.role.read')
  const canManageRoles = usePermission('tenant', 'authz.tenant.role.manage')
  const tier = roleTierOf(canReadRoles, canManageRoles)
  const selfAccount = auth.principal?.account_id
  const isSelf = selfAccount === member.account

  const [selectedRole, setSelectedRole] = useState('')
  const bindings = operations.useTenantAccountBindings(
    canReadRoles ? tenant : '',
    member.account,
  )
  // The role list and catalog exist only where the role control does; the
  // session's permission answer is the gate, not the read's failure.
  const roles = operations.useTenantRoles(canManageRoles ? tenant : '')
  const permissions = operations.useTenantPermissions(
    canManageRoles ? tenant : '',
  )
  const grants = operations.useTenantGrants(
    canManageRoles ? tenant : '',
    selectedRoleOrUndefined(canManageRoles, selectedRole),
  )

  const [failed, setFailed] = useState<FailedWrite | null>(null)
  const feedback = useWriteFeedback()
  const regionRef = useFailureScroll(feedback.outcome)
  const resolveErrorText = useAdminUiErrorText()
  const failureText = failureTextOf(feedback.outcome, resolveErrorText)
  const confirm = usePendingConfirm()

  const bindRole = operations.useTenantBindRole()
  const unbindRole = operations.useTenantUnbindRole()
  const writeMember = operations.useWriteMember()
  const removeMember = operations.useRemoveMember()

  const displayName =
    member.display_name === '' ? undefined : member.display_name
  const memberName = displayName ?? member.account
  const statusLabel = member.active
    ? t('members.status.active')
    : t('members.status.suspended')

  const roleSelected = selectedRole !== ''
  // `isPending` alone is the loading signal: an errored query also has
  // `data === undefined`, so testing the data would keep the panel in its
  // checking state after the read already failed.
  const grantsPending =
    canManageRoles &&
    roleSelected &&
    (grants.isPending || permissions.isPending)
  const grantsFailed =
    canManageRoles &&
    roleSelected &&
    !grantsPending &&
    (grants.isError || permissions.isError)
  const codeByPair = useMemo(
    () => buildGrantCodeIndex(permissions.data ?? []),
    [permissions.data],
  )
  // Every grant the selected role carries must already be in the caller's
  // set. A pair the catalog cannot name is also refused: the panel cannot
  // prove the caller holds it, so it does not offer to hand it out.
  const escalationBlocked =
    canManageRoles &&
    roleSelected &&
    !grantsPending &&
    !grantsFailed &&
    (grants.data ?? []).some((grant) => {
      const code = codeByPair(grant.resource, grant.action)
      return code === null || !callerPermissions.has(code)
    })
  const bindDisabled =
    feedback.pending ||
    !member.active ||
    !roleSelected ||
    grantsPending ||
    grantsFailed ||
    escalationBlocked

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
    void feedback.run(async () => {
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
  }

  const unbind = (role: string) => {
    startWrite(null)
    void feedback.run(async () => {
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
        startWrite({ kind: 'unbind', role })
        throw error
      }
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
        void feedback.run(async () => {
          try {
            await writeMember.mutateAsync({
              tenant,
              account: member.account,
              active,
            })
            return active
              ? t('members.resumed', { account: member.account })
              : t('members.suspended', { account: member.account })
          } catch (error) {
            startWrite({ kind: active ? 'resume' : 'suspend' })
            throw error
          }
        })
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
        void feedback.run(async () => {
          try {
            await removeMember.mutateAsync({
              tenant,
              account: member.account,
            })
            onRemoved(
              displayName === undefined
                ? t('members.removedUnnamed')
                : t('members.removed', { account: member.account }),
            )
            return null
          } catch (error) {
            startWrite({ kind: 'remove' })
            throw error
          }
        })
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
                <Button
                  type="button"
                  size="small"
                  variant="outlined"
                  aria-invalid={refused}
                  aria-describedby={refused ? errorId : undefined}
                  disabled={feedback.pending}
                  onClick={() => unbind(binding.role)}
                >
                  {t('members.panel.unbind')}
                </Button>
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
        displayName === undefined
          ? t('members.panel.close')
          : t('members.panel.closeAria', { name: displayName })
      }
      onClose={onClose}
      onExited={onExited}
    >
      <Typography component="h3" variant="subtitle1">
        {t('members.panel.summary')}
      </Typography>
      <Box sx={{ mt: 1, mb: 2 }}>
        <Typography variant="body2">
          {t('members.panel.field', {
            label: t('members.panel.account'),
            value: member.account,
          })}
        </Typography>
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
            value: formatInstant(member.joined_at, i18n.language, {
              timeZone: 'UTC',
            }),
          })}
        </Typography>
      </Box>

      <Typography component="h3" variant="subtitle1" sx={{ mb: 1 }}>
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
                  disabled={feedback.pending || roles.isPending}
                  sx={{ minWidth: 200 }}
                >
                  <MenuItem value="">{t('members.panel.roleNone')}</MenuItem>
                  {(roles.data ?? []).map((role) => (
                    <MenuItem key={role.role} value={role.role}>
                      {role.role}
                    </MenuItem>
                  ))}
                </TextField>
                <Button
                  type="button"
                  variant="contained"
                  aria-invalid={failed?.kind === 'bind'}
                  aria-describedby={
                    failed?.kind === 'bind' ? 'member-bind-error' : undefined
                  }
                  disabled={bindDisabled}
                  onClick={bind}
                >
                  {t('members.panel.bind')}
                </Button>
              </Box>
              {!member.active && (
                <Typography
                  variant="body2"
                  color="text.secondary"
                  sx={{ mt: 1 }}
                >
                  {t('members.panel.suspendedBindNote')}
                </Typography>
              )}
              {grantsPending && (
                <Typography
                  variant="body2"
                  color="text.secondary"
                  sx={{ mt: 1 }}
                >
                  {t('members.panel.escalationChecking')}
                </Typography>
              )}
              {grantsFailed && (
                <Box sx={{ mt: 1 }}>
                  <Typography variant="body2" color="error">
                    {t('members.panel.escalationFailed')}
                  </Typography>
                  <Button
                    type="button"
                    size="small"
                    variant="outlined"
                    onClick={() => {
                      void grants.refetch()
                      void permissions.refetch()
                    }}
                  >
                    {t('members.panel.escalationRetry')}
                  </Button>
                </Box>
              )}
              {escalationBlocked && (
                <Typography
                  variant="body2"
                  color="error"
                  sx={{ mt: 1 }}
                >
                  {t('members.panel.escalationBlocked')}
                </Typography>
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
        <Button
          type="button"
          variant="outlined"
          aria-invalid={failed?.kind === 'suspend' || failed?.kind === 'resume'}
          aria-describedby={
            failed?.kind === 'suspend' || failed?.kind === 'resume'
              ? 'member-status-error'
              : undefined
          }
          disabled={feedback.pending || (isSelf && member.active)}
          onClick={() => requestActive(!member.active)}
        >
          {member.active
            ? t('members.panel.suspend')
            : t('members.panel.resume')}
        </Button>
        <Button
          type="button"
          variant="outlined"
          color="error"
          aria-invalid={failed?.kind === 'remove'}
          aria-describedby={
            failed?.kind === 'remove' ? 'member-remove-error' : undefined
          }
          disabled={feedback.pending}
          onClick={requestRemove}
        >
          {t('members.remove')}
        </Button>
      </Box>
      {isSelf && member.active && (
        <Typography
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

/** The empty string while the role control is unavailable, so the grants read stays disabled. */
function selectedRoleOrUndefined(
  canManage: boolean,
  selectedRole: string,
): string | undefined {
  return canManage && selectedRole !== '' ? selectedRole : undefined
}
