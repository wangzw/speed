import { useAuthState, useFeatureState, featurePresence } from '@speed/auth-core'
import type { AppShellNavItem } from '@speed/layout-kit'
import { presentDeclarations, useNavItems } from '@speed/route-kit'
import { organizationAdminRoutes } from '@speed/admin-ui'
import type { AdminRoute, AdminRouteTable } from '@speed/admin-ui'
import {
  getRecordedTenantKind,
  useCurrentTenant,
} from './chrome.js'
import { ORGANIZATION_ADMIN_PREFIX } from './paths.js'
import {
  usePermissionRead,
  useTenantPermissionSet,
} from './platform-permissions.js'
import { useSelfTenantState } from './tenant-self.js'

export const organizationAdminProtectedRoles: readonly string[] = ['tenant-admin']

export const organizationAdminShellRoutes: AdminRouteTable =
  organizationAdminRoutes.map((route) => ({
    ...route,
    path: `${ORGANIZATION_ADMIN_PREFIX}${route.path}`,
  }))

export interface OrganizationAdminEntry {
  readonly tenantName: string
  readonly target: string
  readonly routes: readonly AdminRoute[]
  readonly children: readonly AppShellNavItem[]
}

export type OrganizationAdminAdmission =
  | { readonly status: 'pending' }
  | { readonly status: 'failed' }
  | { readonly status: 'denied' }
  | ({ readonly status: 'allowed' } & OrganizationAdminEntry)

export function useOrganizationAdminPermission(
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

export function useOrganizationAdminAdmission(): OrganizationAdminAdmission {
  const snapshot = useAuthState()
  const tenantName = useCurrentTenant()
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
    return presence.state === 'failed'
      ? { status: 'failed' }
      : { status: 'pending' }
  }
  if (tenantName === null) {
    return self.failed ? { status: 'failed' } : { status: 'pending' }
  }
  if (tenantName === 'system') {
    return { status: 'denied' }
  }
  const row = self.tenants.find((tenant) => tenant.name === tenantName)
  const kind = row?.kind ?? getRecordedTenantKind(tenantName)
  // An unknown kind after a failed read is a fault, not "not a member": settle failed so the surface retries.
  if (kind === null) {
    if (self.failed) {
      return { status: 'failed' }
    }
    return { status: self.settled ? 'denied' : 'pending' }
  }
  if (kind !== 'business') {
    return { status: 'denied' }
  }
  const narrowed = presentDeclarations(organizationAdminShellRoutes, presence)
  if (narrowed.length === 0) {
    return { status: 'denied' }
  }
  if (read.tenantName !== tenantName) {
    return { status: 'pending' }
  }
  if (read.failed) {
    return { status: 'failed' }
  }
  if (read.perms === null) {
    return { status: 'pending' }
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
  return { status: 'allowed', tenantName, target, routes: narrowed, children }
}
