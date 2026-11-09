/**
 * The organization administration surface's page wrappers.
 *
 * Each wrapper is a thin composition: it reads the current tenant the host
 * installed on `OrganizationAdminProvider` and hands it to the page
 * component. The shared pages take it as a controlled tenant and then render
 * without their tenant selector, addressing every read and write at that
 * tenant's namespace; their own layout, feedback scopes and refresh actions
 * are unchanged. The members and invitations wrapper mounts the
 * organization-only page (`OrganizationMembersPage`), whose identities
 * resolve from the tenant's member read and which never reaches the platform
 * account surface. The roles wrapper mounts the organization-only role page
 * and hands over the host's protected role names so it renders them
 * read-only.
 *
 * The wrappers exist for the organization pages whose tenant-scoped surface
 * the package ships: invitations and membership, SSO providers, tenant
 * roles and permissions, and security and access.
 */

import type { ReactElement } from 'react'
import {
  useOrganizationAdminOnOrganizationLeft,
  useOrganizationAdminOnTenantUpdated,
  useOrganizationAdminProtectedRoles,
  useOrganizationAdminTenant,
} from './OrganizationAdminProvider.js'
import { OrganizationMembersPage } from './OrganizationMembersPage.js'
import { OrganizationRolesPage } from './OrganizationRolesPage.js'
import { OrganizationSecurityPage } from './OrganizationSecurityPage.js'
import { SsoPage } from './SsoPage.js'
import { TenantProfilePage } from './TenantProfilePage.js'

/** Members and invitations, scoped to the host's current tenant. */
export function OrganizationInvitationsPage(): ReactElement {
  const tenant = useOrganizationAdminTenant()
  return <OrganizationMembersPage tenant={tenant} />
}

/** The organization's profile, scoped to the host's current tenant. A
 * landed save reaches the host's `onTenantUpdated`, which reloads the
 * caller tenant list the menu and the breadcrumb show; a landed deletion
 * reaches the host's departure action, which leaves the organization and
 * shows the confirmation where it lands. A transfer changes ownership only,
 * so the caller stays and the confirmation stays in the ownership card. */
export function OrganizationProfilePage(): ReactElement {
  const tenant = useOrganizationAdminTenant()
  const onTenantUpdated = useOrganizationAdminOnTenantUpdated()
  const onOrganizationLeft = useOrganizationAdminOnOrganizationLeft()
  return (
    <TenantProfilePage
      tenant={tenant}
      onTenantUpdated={onTenantUpdated}
      onOrganizationLeft={onOrganizationLeft}
    />
  )
}

/** SSO providers, scoped to the host's current tenant. */
export function OrganizationSsoPage(): ReactElement {
  const tenant = useOrganizationAdminTenant()
  return <SsoPage tenant={tenant} />
}

/** Tenant roles and permissions, scoped to the host's current tenant. */
export function OrganizationPermissionsPage(): ReactElement {
  const tenant = useOrganizationAdminTenant()
  const protectedRoles = useOrganizationAdminProtectedRoles()
  return (
    <OrganizationRolesPage tenant={tenant} protectedRoles={protectedRoles} />
  )
}

/** The organization's security and access page, scoped to the host's
 * current tenant. */
export function OrganizationSettingsPage(): ReactElement {
  const tenant = useOrganizationAdminTenant()
  return <OrganizationSecurityPage tenant={tenant} />
}
