/**
 * The console's page declarations -- the console surface's contribution
 * to the declarative registration seam.
 *
 * Each administration capability contributes one page declaration: a
 * path pattern, the page view, the permission code the page's data is
 * held to and a decorative navigation icon (the console shell derives
 * the platform navigation from the label and icon each declaration
 * carries). The declarations are collected by the application's
 * composition layer and handed to the route layer it chose; this package
 * imports no router and does no path matching, so a declaration is data
 * plus a view and nothing more.
 *
 * A declaration's label is a React element (ConsoleNavLabel) that
 * resolves its text through the console-ui namespace when it renders:
 * the declarations themselves stay plain module-level values -- the shape
 * the registration seam wants -- while their navigation labels still
 * translate reactively with the host's language.
 *
 * The permission codes are the modules' own: the roster's reads are held
 * to tenant.read, the settings reads to settings.read, the audit query to
 * audit.read, the authorization surface to authz.role.read, the account
 * surface to authn.account.read, the session surface to authn.session.read,
 * the impersonation surface to authn.impersonation.read, the invitation
 * and membership surface to authn.membership.manage, the SSO surface to
 * authn.idp.manage and the SMS template surface to sms.template.read.
 * They are data the host reads to compose its own route guard (this
 * package never decides on them); a page whose writes carry a stronger
 * permission renders its write controls and lets the server refuse a
 * caller who lacks it, rather than hiding them -- the server is the
 * authority.
 */

import type { ConsoleRoute, ConsoleRouteTable } from './route.js'
import { ConsoleNavLabel } from './internal/nav-label.js'
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

/**
 * The console landing (the root) renders the dashboard; it reads the
 * platform roster and the audit trail, so it is held to tenant.read (its
 * audit panel refuses on its own when the viewer lacks audit.read).
 */
export const dashboardAdminRoutes: ConsoleRouteTable = [
  {
    id: 'dashboard',
    path: '/',
    // The dashboard sits at the console root, so the matched path is the
    // root its recent-activity rows append the audit detail path to.
    view: (context) => <DashboardPage rootHref={context.path} />,
    label: <ConsoleNavLabel labelKey="dashboard.title" />,
    icon: <DashboardIcon />,
    permission: 'tenant.read',
    requires: ['tenant.admin'],
    group: 'overview',
  },
] satisfies readonly ConsoleRoute[]

/**
 * The console root derived from a matched detail path: the path with its
 * last two segments (the list segment and the id) removed. The console may
 * be mounted under a host-chosen prefix, so the root is computed from the
 * matched path rather than from a literal segment: '/tenants/abc' yields
 * '', '/admin/tenants/abc' yields '/admin'.
 */
function consoleRootOf(path: string): string {
  return path.split('/').slice(0, -2).join('/')
}

/**
 * The tenant pages: the roster and one tenant's detail. The detail
 * declaration is unlabelled (it is reached from a row, not the
 * navigation), and its view carries the roster's path (the matched path
 * with the tenant segment removed) so the detail page can link back, plus
 * the console's root path (the matched path with its last two segments
 * removed), which resolves a published sibling declaration whose path is
 * console-relative.
 */
export const tenantAdminRoutes: ConsoleRouteTable = [
  {
    id: 'tenants',
    path: '/tenants',
    view: (context) => <TenantRosterPage basePath={context.path} />,
    label: <ConsoleNavLabel labelKey="tenants.title" />,
    icon: <TenantsIcon />,
    permission: 'tenant.read',
    requires: ['tenant.admin'],
    group: 'tenants',
  },
  {
    id: 'tenant-detail',
    path: '/tenants/:id',
    view: (context) => (
      <TenantDetailPage
        id={context.params.id ?? ''}
        parentHref={context.path.replace(/\/[^/]+$/, '')}
        rootHref={consoleRootOf(context.path)}
      />
    ),
    permission: 'tenant.read',
    requires: ['tenant.admin'],
  },
] satisfies readonly ConsoleRoute[]

/** The account page's declaration, held to authn.account.read. */
export const accountsAdminRoutes: ConsoleRouteTable = [
  {
    id: 'accounts',
    path: '/accounts',
    view: () => <AccountsPage />,
    label: <ConsoleNavLabel labelKey="accounts.title" />,
    icon: <AccountsIcon />,
    permission: 'authn.account.read',
    requires: ['authn.admin'],
    group: 'identity',
  },
] satisfies readonly ConsoleRoute[]

/** The session page's declaration, held to authn.session.read. */
export const sessionsAdminRoutes: ConsoleRouteTable = [
  {
    id: 'sessions',
    path: '/sessions',
    view: () => <SessionsPage />,
    label: <ConsoleNavLabel labelKey="sessions.title" />,
    icon: <SessionsIcon />,
    permission: 'authn.session.read',
    requires: ['authn.admin.sessions'],
    group: 'identity',
  },
] satisfies readonly ConsoleRoute[]

/** The impersonation page's declaration, held to authn.impersonation.read. */
export const impersonationsAdminRoutes: ConsoleRouteTable = [
  {
    id: 'impersonations',
    path: '/impersonations',
    view: () => <ImpersonationsPage />,
    label: <ConsoleNavLabel labelKey="impersonations.title" />,
    icon: <ImpersonationsIcon />,
    permission: 'authn.impersonation.read',
    requires: ['authn.impersonation', 'authn.admin.impersonations', 'tenant.admin'],
    group: 'identity',
  },
] satisfies readonly ConsoleRoute[]

/** The authorization page's declaration, held to authz.role.read. */
export const permissionsAdminRoutes: ConsoleRouteTable = [
  {
    id: 'permissions',
    path: '/permissions',
    view: () => <PermissionsPage />,
    label: <ConsoleNavLabel labelKey="permissions.title" />,
    icon: <PermissionsIcon />,
    permission: 'authz.role.read',
    requires: ['authz.admin', 'tenant.admin'],
    group: 'identity',
  },
] satisfies readonly ConsoleRoute[]

/** The invitation/membership page's declaration, held to authn.membership.manage. */
export const invitationsAdminRoutes: ConsoleRouteTable = [
  {
    id: 'invitations',
    path: '/invitations',
    view: () => <InvitationsPage />,
    label: <ConsoleNavLabel labelKey="invitations.title" />,
    icon: <InvitationsIcon />,
    permission: 'authn.membership.manage',
    requires: ['authn.invitations', 'authn.admin', 'tenant.admin'],
    group: 'identity',
  },
] satisfies readonly ConsoleRoute[]

/** The SSO page's declaration, held to authn.idp.manage. */
export const ssoAdminRoutes: ConsoleRouteTable = [
  {
    id: 'sso',
    path: '/sso',
    view: () => <SsoPage />,
    label: <ConsoleNavLabel labelKey="sso.title" />,
    icon: <SsoIcon />,
    permission: 'authn.idp.manage',
    requires: ['authn.admin.sso', 'tenant.admin'],
    group: 'identity',
  },
] satisfies readonly ConsoleRoute[]

/** The settings read-write page's declaration, held to settings.read. */
export const settingsAdminRoutes: ConsoleRouteTable = [
  {
    id: 'settings',
    path: '/settings',
    view: () => <SettingsPage />,
    label: <ConsoleNavLabel labelKey="settings.title" />,
    icon: <SettingsIcon />,
    permission: 'settings.read',
    requires: ['settings.admin'],
    group: 'platform',
  },
] satisfies readonly ConsoleRoute[]

/** The SMS-template page's declaration, held to sms.template.read. */
export const smsAdminRoutes: ConsoleRouteTable = [
  {
    id: 'sms',
    path: '/sms',
    view: () => <SmsPage />,
    label: <ConsoleNavLabel labelKey="sms.title" />,
    icon: <SmsIcon />,
    permission: 'sms.template.read',
    requires: ['sms.admin'],
    group: 'platform',
  },
] satisfies readonly ConsoleRoute[]

/**
 * The audit pages: the query and one record's detail. The detail
 * declaration is unlabelled (reached from a row), and its view carries the
 * query's path so the detail page can link back.
 */
export const auditAdminRoutes: ConsoleRouteTable = [
  {
    id: 'audit',
    path: '/audit',
    view: (context) => <AuditPage basePath={context.path} />,
    label: <ConsoleNavLabel labelKey="audit.title" />,
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
] satisfies readonly ConsoleRoute[]

/**
 * The whole console page set: the administration pages in the order they
 * appear in the platform navigation. A host composing the console spreads
 * this into its route table (alongside any pages it adds itself), so the
 * console's own pages are registered in one place.
 */
export const consoleRoutes: ConsoleRouteTable = [
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
