/**
 * The workspace administration surface's page wrappers.
 *
 * Each wrapper is a thin composition: it reads the current tenant the host
 * installed on `WorkspaceAdminProvider` and hands it to the shared page
 * component as a controlled tenant. The page then renders without its
 * tenant selector and addresses every read and write at that tenant's
 * namespace; the page's own layout, feedback scopes and refresh actions are
 * unchanged. The invitations wrapper also turns on tenant mode, so the page
 * resolves member and inviter identities from the tenant's member read and
 * drops the platform-account member write form instead of reaching the
 * platform account surface. The roles wrapper hands over the host's
 * protected role names so the tenant role page renders them read-only.
 *
 * The wrappers exist for the workspace pages whose tenant-scoped surface
 * the package ships: invitations and membership, SSO providers, tenant
 * roles and permissions, and the tenant settings layer.
 */

import type { ReactElement } from 'react'
import {
  useWorkspaceAdminProtectedRoles,
  useWorkspaceAdminTenant,
} from './WorkspaceAdminProvider.js'
import { InvitationsPage } from './InvitationsPage.js'
import { PermissionsPage } from './PermissionsPage.js'
import { SettingsPage } from './SettingsPage.js'
import { SsoPage } from './SsoPage.js'

/** Invitations and membership, scoped to the host's current tenant. */
export function WorkspaceInvitationsPage(): ReactElement {
  const tenant = useWorkspaceAdminTenant()
  return <InvitationsPage tenant={tenant} tenantMode />
}

/** SSO providers, scoped to the host's current tenant. */
export function WorkspaceSsoPage(): ReactElement {
  const tenant = useWorkspaceAdminTenant()
  return <SsoPage tenant={tenant} />
}

/** Tenant roles and permissions, scoped to the host's current tenant. */
export function WorkspacePermissionsPage(): ReactElement {
  const tenant = useWorkspaceAdminTenant()
  const protectedRoles = useWorkspaceAdminProtectedRoles()
  return (
    <PermissionsPage
      tenant={tenant}
      tenantMode
      protectedRoles={protectedRoles}
    />
  )
}

/** The tenant settings layer, scoped to the host's current tenant. */
export function WorkspaceSettingsPage(): ReactElement {
  const tenant = useWorkspaceAdminTenant()
  return <SettingsPage tenant={tenant} tenantMode />
}
