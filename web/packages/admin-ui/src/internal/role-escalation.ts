/**
 * The role-assignment escalation pre-check: compares the selected role's grants
 * with the caller's tenant set and fails closed on pending, failed or codeless reads.
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
   * The escalation pre-check for `selectedRole` in `tenant`; the grants read
   * stays disabled while the caller cannot manage roles or no role is selected.
   */
export function useRoleEscalationCheck(
  tenant: string,
  selectedRole: string,
): RoleEscalationCheck {
  const operations = useOrganizationAdminOperations()
  const auth = useAuthState()
  const canManageRoles = usePermission('tenant', 'authz.tenant.role.manage')
  // An absent permission set or unauthenticated session reads as empty,
  // which fails every grant.
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
  // `data === undefined`, so testing the data would keep the surface checking.
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
