/**
 * The organization administration surface's page declarations.
 *
 * Paths are relative to the organization admin root the host mounts them
 * under (`/organization/admin` in the demo). Each declaration carries a stable
 * `id`, a translated navigation label, the tenant-domain permission code
 * the page's data is held to, and the tenant-scoped features it needs. The
 * permission code and the feature list are data the host reads: this
 * package never decides on either, and the host narrows the table with
 * route-kit's `presentDeclarations` before handing it back here.
 *
 * The declarations never name `tenant.admin`: the organization surface reads
 * no roster and holds no tenant selector. The tenant is the host's current
 * tenant, written into every request path by the page wrappers.
 *
 * The roles page is held to the tenant role read code and needs the
 * `authz.tenantadmin` and `authn.admin` features: its account pickers read
 * the tenant's members through the membership endpoints, which the
 * `authn.admin` feature brings. The settings page is held to the tenant
 * settings read code and needs `settings.tenantadmin`. The write codes are
 * not declaration permissions: a page renders its write controls and lets
 * the server refuse a caller who lacks the write permission.
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
    // The profile module's own read code and feature: the page's data is
    // the organization profile, and a deployment without `tenant.profile`
    // serves no route, page or navigation entry.
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
