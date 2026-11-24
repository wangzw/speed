/**
 * The organization surface's page wrappers: each reads the host's current
 * tenant and hands it to a shared or organization-only page component.
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

/**
 * The organization's profile, scoped to the host's current tenant. A landed save
 * reaches `onTenantUpdated`; a landed deletion reaches `onOrganizationLeft`.
 */
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

/** The organization's security and access page, scoped to the host's current tenant. */
export function OrganizationSettingsPage(): ReactElement {
  const tenant = useOrganizationAdminTenant()
  return <OrganizationSecurityPage tenant={tenant} />
}
