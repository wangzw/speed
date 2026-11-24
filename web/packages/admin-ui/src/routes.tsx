/**
 * The console's page declarations: path, view, permission code and icon, with
 * labels resolved through the admin-ui namespace; the codes are host-read data.
 */

import type { AdminRoute, AdminRouteTable } from './route.js'
import { AdminNavLabel } from './internal/nav-label.js'
import {
  AccountsIcon,
  AuditIcon,
  DashboardIcon,
  ImpersonationsIcon,
  InvitationsIcon,
  PermissionsIcon,
  SessionsIcon,
  SettingsIcon,
  SmsIcon,
  SsoIcon,
  TenantsIcon,
} from './internal/icons.js'
import { DashboardPage } from './DashboardPage.js'
import { TenantRosterPage } from './TenantRosterPage.js'
import { TenantCreatePage } from './TenantCreatePage.js'
import { TenantDetailPage } from './TenantDetailPage.js'
import { SettingsPage } from './SettingsPage.js'
import { AuditPage } from './AuditPage.js'
import { AuditDetailPage } from './AuditDetailPage.js'
import { PermissionsPage } from './PermissionsPage.js'
import { AccountsPage } from './AccountsPage.js'
import { SessionsPage } from './SessionsPage.js'
import { ImpersonationsPage } from './ImpersonationsPage.js'
import { InvitationsPage } from './InvitationsPage.js'
import { SsoPage } from './SsoPage.js'
import { SmsPage } from './SmsPage.js'

/** The console landing: the dashboard at the root, held to tenant.read. */
export const dashboardAdminRoutes: AdminRouteTable = [
  {
    id: 'dashboard',
    path: '/',
    // The dashboard sits at the console root; its rows append the audit detail path to it.
    view: (context) => <DashboardPage rootHref={context.path} />,
    label: <AdminNavLabel labelKey="dashboard.title" />,
    icon: <DashboardIcon />,
    permission: 'tenant.read',
    requires: ['tenant.admin'],
    group: 'overview',
  },
] satisfies readonly AdminRoute[]

/**
 * The console root from a matched detail path: the last two segments removed,
 * rather than a literal, since the console may be mounted under any prefix.
 */
function adminRootOf(path: string): string {
  return path.split('/').slice(0, -2).join('/')
}

/**
 * The tenant pages: roster, standalone registration form and one tenant's
 * detail. The registration declaration precedes the detail pattern so
 * '/tenants/new' is not read as a tenant name; both are unlabelled.
 */
export const tenantAdminRoutes: AdminRouteTable = [
  {
    id: 'tenants',
    path: '/tenants',
    view: (context) => <TenantRosterPage basePath={context.path} />,
    label: <AdminNavLabel labelKey="tenants.title" />,
    icon: <TenantsIcon />,
    permission: 'tenant.read',
    requires: ['tenant.admin'],
    group: 'tenants',
  },
  {
    id: 'tenant-create',
    path: '/tenants/new',
    view: (context) => (
      <TenantCreatePage basePath={context.path.replace(/\/new$/, '')} />
    ),
    permission: 'tenant.read',
    requires: ['tenant.admin'],
  },
  {
    id: 'tenant-detail',
    path: '/tenants/:name',
    view: (context) => (
      <TenantDetailPage
        name={context.params.name ?? ''}
        parentHref={context.path.replace(/\/[^/]+$/, '')}
        rootHref={adminRootOf(context.path)}
      />
    ),
    permission: 'tenant.read',
    requires: ['tenant.admin'],
  },
] satisfies readonly AdminRoute[]

/** The account page's declaration, held to authn.account.read. */
export const accountsAdminRoutes: AdminRouteTable = [
  {
    id: 'accounts',
    path: '/accounts',
    view: () => <AccountsPage />,
    label: <AdminNavLabel labelKey="accounts.title" />,
    icon: <AccountsIcon />,
    permission: 'authn.account.read',
    requires: ['authn.admin'],
    group: 'identity',
  },
] satisfies readonly AdminRoute[]

/** The session page's declaration, held to authn.session.read. */
export const sessionsAdminRoutes: AdminRouteTable = [
  {
    id: 'sessions',
    path: '/sessions',
    view: () => <SessionsPage />,
    label: <AdminNavLabel labelKey="sessions.title" />,
    icon: <SessionsIcon />,
    permission: 'authn.session.read',
    requires: ['authn.admin.sessions'],
    group: 'identity',
  },
] satisfies readonly AdminRoute[]

/** The impersonation page's declaration, held to authn.impersonation.read. */
export const impersonationsAdminRoutes: AdminRouteTable = [
  {
    id: 'impersonations',
    path: '/impersonations',
    view: () => <ImpersonationsPage />,
    label: <AdminNavLabel labelKey="impersonations.title" />,
    icon: <ImpersonationsIcon />,
    permission: 'authn.impersonation.read',
    requires: ['authn.impersonation', 'authn.admin.impersonations', 'tenant.admin'],
    group: 'identity',
  },
] satisfies readonly AdminRoute[]

/** The authorization page's declaration, held to authz.role.read. */
export const permissionsAdminRoutes: AdminRouteTable = [
  {
    id: 'permissions',
    path: '/permissions',
    view: () => <PermissionsPage />,
    label: <AdminNavLabel labelKey="permissions.title" />,
    icon: <PermissionsIcon />,
    permission: 'authz.role.read',
    requires: ['authz.admin', 'tenant.admin'],
    group: 'identity',
  },
] satisfies readonly AdminRoute[]

/** The invitation/membership page's declaration, held to authn.membership.manage. */
export const invitationsAdminRoutes: AdminRouteTable = [
  {
    id: 'invitations',
    path: '/invitations',
    view: () => <InvitationsPage />,
    label: <AdminNavLabel labelKey="invitations.title" />,
    icon: <InvitationsIcon />,
    permission: 'authn.membership.manage',
    requires: ['authn.invitations', 'authn.admin', 'tenant.admin'],
    group: 'identity',
  },
] satisfies readonly AdminRoute[]

/** The SSO page's declaration, held to authn.idp.manage. */
export const ssoAdminRoutes: AdminRouteTable = [
  {
    id: 'sso',
    path: '/sso',
    view: () => <SsoPage />,
    label: <AdminNavLabel labelKey="sso.title" />,
    icon: <SsoIcon />,
    permission: 'authn.idp.manage',
    requires: ['authn.admin.sso', 'tenant.admin'],
    group: 'identity',
  },
] satisfies readonly AdminRoute[]

/** The settings read-write page's declaration, held to settings.read. */
export const settingsAdminRoutes: AdminRouteTable = [
  {
    id: 'settings',
    path: '/settings',
    view: () => <SettingsPage />,
    label: <AdminNavLabel labelKey="settings.title" />,
    icon: <SettingsIcon />,
    permission: 'settings.read',
    requires: ['settings.admin'],
    group: 'platform',
  },
] satisfies readonly AdminRoute[]

/** The SMS-template page's declaration, held to sms.template.read. */
export const smsAdminRoutes: AdminRouteTable = [
  {
    id: 'sms',
    path: '/sms',
    view: () => <SmsPage />,
    label: <AdminNavLabel labelKey="sms.title" />,
    icon: <SmsIcon />,
    permission: 'sms.template.read',
    requires: ['sms.admin'],
    group: 'platform',
  },
] satisfies readonly AdminRoute[]

/**
 * The audit pages: the query and one record's detail. The detail is unlabelled
 * and its view carries the query's path so it can link back.
 */
export const auditAdminRoutes: AdminRouteTable = [
  {
    id: 'audit',
    path: '/audit',
    view: (context) => <AuditPage basePath={context.path} />,
    label: <AdminNavLabel labelKey="audit.title" />,
    icon: <AuditIcon />,
    permission: 'audit.read',
    requires: ['audit.admin'],
    group: 'audit',
  },
  {
    id: 'audit-detail',
    path: '/audit/:id',
    view: (context) => (
      <AuditDetailPage
        id={context.params.id ?? ''}
        parentHref={context.path.replace(/\/[^/]+$/, '')}
      />
    ),
    permission: 'audit.read',
    requires: ['audit.admin'],
  },
] satisfies readonly AdminRoute[]

/** The whole console page set, in platform navigation order. */
export const adminRoutes: AdminRouteTable = [
  ...dashboardAdminRoutes,
  ...tenantAdminRoutes,
  ...accountsAdminRoutes,
  ...sessionsAdminRoutes,
  ...impersonationsAdminRoutes,
  ...permissionsAdminRoutes,
  ...invitationsAdminRoutes,
  ...ssoAdminRoutes,
  ...settingsAdminRoutes,
  ...smsAdminRoutes,
  ...auditAdminRoutes,
]
