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
 * platform account surface.
 *
 * The wrappers exist for the pages whose tenant-scoped surface is
 * implemented today: invitations and membership, and SSO providers. The
 * tenant settings and tenant roles/permissions wrappers belong to the
 * batches that add their tenant-scoped endpoints and behaviour; they are
 * not exported here, and the design's table records them as not
 * implemented.
 */

import type { ReactElement } from 'react'
import { useWorkspaceAdminTenant } from './WorkspaceAdminProvider.js'
import { InvitationsPage } from './InvitationsPage.js'
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
