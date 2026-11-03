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
 * The tenant settings and tenant roles/permissions declarations are absent
 * until their tenant-scoped endpoints and page behaviour exist.
 */

import type { AdminRoute, AdminRouteTable } from './route.js'
import { AdminNavLabel } from './internal/nav-label.js'
import { InvitationsIcon, SsoIcon } from './internal/icons.js'
import { WorkspaceInvitationsPage, WorkspaceSsoPage } from './workspace-pages.js'

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
] satisfies readonly AdminRoute[]
