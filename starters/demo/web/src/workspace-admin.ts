/**
 * The workspace admin area's route facts and entry derivation.
 *
 * The workspace surface is the tenant-scoped management area: its
 * declarations arrive from @speed/admin-ui relative to a host-chosen root
 * and mount under WORKSPACE_ADMIN_PREFIX (/workspace/admin). The host
 * narrows them with the feature-presence predicate, then derives both
 * entries -- the navigation parent and the home card -- from the same
 * answer, so the two never disagree.
 *
 * Admission is fail-closed at every step. The surface is offered only when
 * the session is authenticated and not impersonating, the current tenant is
 * a business tenant other than `system`, the narrowed declarations are
 * non-empty, and the tenant-domain permission set *for that tenant* holds
 * at least one of their codes. The permission set is read through
 * useTenantPermissionSet, which returns null until the tagged read answers
 * for the current tenant: a pending read, a failed one, and a set read for
 * the tenant the viewer just left all fail closed rather than pairing the
 * new tenant with the previous tenant's permissions.
 */

import { useAuthState, useFeatureState, featurePresence } from '@speed/auth-core'
import type { AppShellNavItem } from '@speed/layout-kit'
import { presentDeclarations, useNavItems } from '@speed/route-kit'
import { workspaceAdminRoutes } from '@speed/admin-ui'
import type { AdminRoute, AdminRouteTable } from '@speed/admin-ui'
import {
  getRecordedTenantKind,
  useCurrentTenantId,
} from './chrome.js'
import { WORKSPACE_ADMIN_PREFIX } from './paths.js'
import {
  usePermissionRead,
  useTenantPermissionSet,
} from './platform-permissions.js'
import { useSelfTenantState } from './tenant-self.js'

/**
 * The workspace declarations mapped onto their prefixed URLs. The workspace
 * root itself has no page: every declaration lives one segment below it.
 */
export const workspaceAdminShellRoutes: AdminRouteTable =
  workspaceAdminRoutes.map((route) => ({
    ...route,
    path: `${WORKSPACE_ADMIN_PREFIX}${route.path}`,
  }))

/** The entry both the navigation parent and the home card render. */
export interface WorkspaceAdminEntry {
  /** The current tenant the surface acts on; every request names it. */
  readonly tenantId: string
  /** The first declaration the caller may open. */
  readonly target: string
  /** The narrowed declarations, prefixed. */
  readonly routes: readonly AdminRoute[]
  /** The route-kit navigation entries for the declarations the caller may
   * open, ready to sit under the parent entry. */
  readonly children: readonly AppShellNavItem[]
}

/**
 * The admission outcome: `pending` while a fact the decision needs is still
 * unknown (the tenant, its kind, the feature document, the tenant
 * permission read), `denied` when a settled fact rules the surface out, and
 * `allowed` only when every condition holds.
 */
export type WorkspaceAdminAdmission =
  | { readonly status: 'pending' }
  | { readonly status: 'denied' }
  | ({ readonly status: 'allowed' } & WorkspaceAdminEntry)

/**
 * Whether the page at the current URL may open. The `permission` a
 * declaration carries is matched against the scoped tenant set; a code the
 * caller does not hold denies the page, and the host sends the viewer to
 * the first page it may open.
 */
export function useWorkspaceAdminPermission(
  permission: string | undefined,
): 'allowed' | 'denied' | 'pending' {
  const permissions = useTenantPermissionSet()
  if (permission === undefined) {
    return 'allowed'
  }
  if (permissions === null) {
    return 'pending'
  }
  return permissions.includes(permission) ? 'allowed' : 'denied'
}

/**
 * The workspace surface's admission and entry for the current render.
 *
 * Hooks are called unconditionally; the returned status carries the
 * decision. Callers render the entry (navigation parent, home card) only on
 * `allowed`, wait on `pending`, and send the viewer home on `denied`.
 */
export function useWorkspaceAdminAdmission(): WorkspaceAdminAdmission {
  const snapshot = useAuthState()
  const tenantId = useCurrentTenantId()
  const self = useSelfTenantState()
  const read = usePermissionRead()
  const presence = featurePresence(useFeatureState())
  const navItems = useNavItems()

  if (snapshot.state !== 'authenticated') {
    return { status: 'denied' }
  }
  if (snapshot.impersonating) {
    return { status: 'denied' }
  }
  if (presence.state !== 'ready') {
    return { status: 'pending' }
  }
  if (tenantId === null) {
    return { status: 'pending' }
  }
  if (tenantId === 'system') {
    return { status: 'denied' }
  }
  // The kind is the host's answer for this tenant: the caller tenant read
  // carries it, and an operation's answer can record it before the list
  // does. A tenant the host knows nothing about is not shown as a business
  // tenant; a list that has not settled yet is still pending.
  const row = self.tenants.find((tenant) => tenant.id === tenantId)
  const kind = row?.kind ?? getRecordedTenantKind(tenantId)
  if (kind === null) {
    return { status: self.settled ? 'denied' : 'pending' }
  }
  if (kind !== 'business') {
    return { status: 'denied' }
  }
  const narrowed = presentDeclarations(workspaceAdminShellRoutes, presence)
  if (narrowed.length === 0) {
    return { status: 'denied' }
  }
  // The tagged read pairs a set with the tenant it was read for. A read
  // issued for another tenant is not an answer for this one: the switch
  // clears and re-reads, so that window stays pending rather than admitting
  // the new tenant with the old tenant's set.
  if (read.tenantId !== tenantId) {
    return { status: read.failed ? 'denied' : 'pending' }
  }
  if (read.perms === null) {
    return { status: read.failed ? 'denied' : 'pending' }
  }
  const permissions = read.perms.tenant
  if (permissions === null || permissions.length === 0) {
    return { status: 'denied' }
  }
  const openable = narrowed.filter(
    (route) =>
      route.permission === undefined ||
      permissions.includes(route.permission),
  )
  if (openable.length === 0) {
    return { status: 'denied' }
  }
  const target =
    openable.find((route) => route.label !== undefined)?.path ??
    openable[0]?.path
  if (target === undefined) {
    return { status: 'denied' }
  }
  const openablePaths = new Set(openable.map((route) => route.path))
  const icons = new Map(
    narrowed.flatMap((route) =>
      route.icon === undefined ? [] : [[route.path, route.icon] as const],
    ),
  )
  // The children come from route-kit's own navigation derivation -- the
  // same entries, hrefs, selection and click handler the rest of the shell
  // uses -- restricted to the declarations the caller may open. The parent
  // is therefore an entry over real links, never a hand-built list.
  const children = navItems
    .filter((item) => openablePaths.has(item.id))
    .map(
      (item): AppShellNavItem => ({
        id: item.id,
        label: item.label,
        icon: icons.get(item.id),
        href: item.href,
        selected: item.selected,
        onClick: item.onClick,
      }),
    )
  return { status: 'allowed', tenantId, target, routes: narrowed, children }
}
