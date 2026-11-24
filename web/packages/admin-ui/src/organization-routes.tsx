/**
 * The organization surface's page declarations; each carries a stable id, a
 * label, and the tenant-domain permission and features as host-read data.
 */

import type { AdminRoute, AdminRouteTable } from './route.js'
import { AdminNavLabel } from './internal/nav-label.js'
import {
  InvitationsIcon,
  PermissionsIcon,
  ProfileIcon,
  SettingsIcon,
  SsoIcon,
} from './internal/icons.js'
import {
  OrganizationInvitationsPage,
  OrganizationPermissionsPage,
  OrganizationProfilePage,
  OrganizationSettingsPage,
  OrganizationSsoPage,
} from './organization-pages.js'

/** The organization surface's page declarations, in navigation order. */
export const organizationAdminRoutes: AdminRouteTable = [
  {
    id: 'organization-profile',
    path: '/profile',
    view: () => <OrganizationProfilePage />,
    label: <AdminNavLabel labelKey="organization.nav.profile" />,
    icon: <ProfileIcon />,
    permission: 'tenant.profile.read',
    requires: ['tenant.profile'],
  },
  {
    id: 'organization-invitations',
    path: '/invitations',
    view: () => <OrganizationInvitationsPage />,
    label: <AdminNavLabel labelKey="organization.nav.invitations" />,
    icon: <InvitationsIcon />,
    permission: 'authn.membership.manage',
    requires: ['authn.admin', 'authn.invitations'],
  },
  {
    id: 'organization-sso',
    path: '/sso',
    view: () => <OrganizationSsoPage />,
    label: <AdminNavLabel labelKey="organization.nav.sso" />,
    icon: <SsoIcon />,
    permission: 'authn.idp.manage',
    requires: ['authn.admin', 'authn.admin.sso'],
  },
  {
    id: 'organization-roles',
    path: '/roles',
    view: () => <OrganizationPermissionsPage />,
    label: <AdminNavLabel labelKey="organization.nav.roles" />,
    icon: <PermissionsIcon />,
    permission: 'authz.tenant.role.read',
    requires: ['authz.tenantadmin', 'authn.admin'],
  },
  {
    id: 'organization-settings',
    path: '/settings',
    view: () => <OrganizationSettingsPage />,
    label: <AdminNavLabel labelKey="organization.nav.settings" />,
    icon: <SettingsIcon />,
    permission: 'settings.tenant.read',
    requires: ['settings.tenantadmin'],
  },
] satisfies readonly AdminRoute[]
