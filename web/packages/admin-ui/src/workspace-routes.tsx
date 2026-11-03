/**
 * The workspace administration surface's page declarations.
 *
 * Paths are relative to the workspace admin root the host mounts them
 * under (`/workspace/admin` in the demo). Each declaration carries a stable
 * `id`, a translated navigation label, the tenant-domain permission code
 * the page's data is held to, and the tenant-scoped features it needs. The
 * permission code and the feature list are data the host reads: this
 * package never decides on either, and the host narrows the table with
 * route-kit's `presentDeclarations` before handing it back here.
 *
 * The declarations never name `tenant.admin`: the workspace surface reads
 * no roster and holds no tenant selector. The tenant is the host's current
 * tenant, written into every request path by the page wrappers.
 *
 * The roles page is held to the tenant role read code and needs the
 * `authz.tenantadmin` feature; the settings page is held to the tenant
 * settings read code and needs `settings.tenantadmin`. The write codes are
 * not declaration permissions: a page renders its write controls and lets
 * the server refuse a caller who lacks the write permission.
 */

import type { AdminRoute, AdminRouteTable } from './route.js'
import { AdminNavLabel } from './internal/nav-label.js'
import {
  InvitationsIcon,
  PermissionsIcon,
  SettingsIcon,
  SsoIcon,
} from './internal/icons.js'
import {
  WorkspaceInvitationsPage,
  WorkspacePermissionsPage,
  WorkspaceSettingsPage,
  WorkspaceSsoPage,
} from './workspace-pages.js'

/** The workspace surface's page declarations, in navigation order. */
export const workspaceAdminRoutes: AdminRouteTable = [
  {
    id: 'workspace-invitations',
    path: '/invitations',
    view: () => <WorkspaceInvitationsPage />,
    label: <AdminNavLabel labelKey="workspace.nav.invitations" />,
    icon: <InvitationsIcon />,
    permission: 'authn.membership.manage',
    requires: ['authn.admin', 'authn.invitations'],
  },
  {
    id: 'workspace-sso',
    path: '/sso',
    view: () => <WorkspaceSsoPage />,
    label: <AdminNavLabel labelKey="workspace.nav.sso" />,
    icon: <SsoIcon />,
    permission: 'authn.idp.manage',
    requires: ['authn.admin', 'authn.admin.sso'],
  },
  {
    id: 'workspace-roles',
    path: '/roles',
    view: () => <WorkspacePermissionsPage />,
    label: <AdminNavLabel labelKey="workspace.nav.roles" />,
    icon: <PermissionsIcon />,
    permission: 'authz.tenant.role.read',
    requires: ['authz.tenantadmin'],
  },
  {
    id: 'workspace-settings',
    path: '/settings',
    view: () => <WorkspaceSettingsPage />,
    label: <AdminNavLabel labelKey="workspace.nav.settings" />,
    icon: <SettingsIcon />,
    permission: 'settings.tenant.read',
    requires: ['settings.tenantadmin'],
  },
] satisfies readonly AdminRoute[]
