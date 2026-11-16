/**
 * The role-assignment escalation pre-check shared by the member panel's bind
 * control and the invitation issue form's role selector.
 *
 * Both surfaces offer to hand a role to someone else, so both must first
 * prove they are not handing out more than the caller already holds. The
 * check reads the selected role's grants from the server (the engine is the
 * authority on what a grant means; the client never reconstructs it) and
 * compares each grant's permission code with the caller's own tenant set.
 * Three outcomes fail closed: the read is pending, the read failed, or a
 * grant carries no permission code. A grant the caller does not hold blocks
 * the write. The selected role's own grants answer is returned so the
 * calling surface can offer a retry after a failed read.
 */

import { useMemo } from 'react'
import { useAuthState, usePermission } from '@speed/auth-core'
import { useOrganizationAdminOperations } from '../organization-operations.js'

/** What the two role-assigning surfaces need to gate their write. */
export interface RoleEscalationCheck {
  /** Whether the caller holds the tenant role-management permission. */
  readonly canManageRoles: boolean
  /** Whether a role is selected at all. */
  readonly roleSelected: boolean
  /** The selected role's grants read, so the surface can retry it. */
  readonly grants: ReturnType<
    ReturnType<typeof useOrganizationAdminOperations>['useTenantGrants']
  >
  /** The grants read is in flight. */
  readonly grantsPending: boolean
  /** The grants read failed; the surface offers a retry. */
  readonly grantsFailed: boolean
  /** A grant carries no permission code, so it cannot be compared. */
  readonly grantPermissionUnconfirmed: boolean
  /** At least one grant is not in the caller's own permission set. */
  readonly escalationBlocked: boolean
}

/**
 * The escalation pre-check for `selectedRole` in `tenant`. The grants read
 * stays disabled while the caller cannot manage roles or no role is
 * selected.
 */
export function useRoleEscalationCheck(
  tenant: string,
  selectedRole: string,
): RoleEscalationCheck {
  const operations = useOrganizationAdminOperations()
  const auth = useAuthState()
  const canManageRoles = usePermission('tenant', 'authz.tenant.role.manage')
  // The permission set is what the check compares against; an absent set or
  // an unauthenticated session reads as empty, which fails every grant.
  const permissionSet =
    auth.state === 'authenticated' ? auth.permissionSets.tenant : null
  const callerPermissions = useMemo(
    () => new Set(permissionSet ?? []),
    [permissionSet],
  )
  const roleSelected = selectedRole !== ''
  const grants = operations.useTenantGrants(
    canManageRoles ? tenant : '',
    canManageRoles && roleSelected ? selectedRole : undefined,
  )
  // `isPending` alone is the loading signal: an errored query also has
  // `data === undefined`, so testing the data would keep the surface in its
  // checking state after the read already failed.
  const grantsPending = canManageRoles && roleSelected && grants.isPending
  const grantsFailed =
    canManageRoles && roleSelected && !grantsPending && grants.isError
  const grantPermissionUnconfirmed =
    canManageRoles &&
    roleSelected &&
    !grantsPending &&
    !grantsFailed &&
    (grants.data ?? []).some((grant) => grant.permission === '')
  const escalationBlocked =
    canManageRoles &&
    roleSelected &&
    !grantsPending &&
    !grantsFailed &&
    (grants.data ?? []).some(
      (grant) =>
        grant.permission === '' || !callerPermissions.has(grant.permission),
    )
  return {
    canManageRoles,
    roleSelected,
    grants,
    grantsPending,
    grantsFailed,
    grantPermissionUnconfirmed,
    escalationBlocked,
  }
}
