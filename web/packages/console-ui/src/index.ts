/**
 * Public entry of @speed/console-ui.
 *
 * The platform-staff console component family -- the second shell. It
 * renders the platform's backend administration surfaces as one console
 * frame: the tenant roster (/api/v1/system/tenants), the account, session and
 * impersonation surfaces (/api/v1/system/auth), the authorization surface
 * (/api/v1/system), the invitation and membership surface
 * (/api/v1/tenants/:tenant/...), the SSO surface (/api/v1/tenants/:tenant/sso),
 * the settings read-write surface (/api/v1/system/settings), the SMS template
 * surface (/api/v1/system/sms) and the audit query (/api/v1/system/audit). The console
 * root renders a dashboard, and the tenant roster and the audit query each
 * link a row to that record's detail page. Those endpoints do not resolve a tenant, so the console
 * has no tenant switcher and no tenant-scoped query key: it is not, and
 * cannot be, a page of @speed/tenancy-ui or @speed/product-shell. A page
 * whose route carries the tenant as a path segment (authorization,
 * invitations and membership, SSO) -- and the impersonation page, whose
 * read takes the tenant as a required query argument -- carries a tenant
 * selector; the tenant it names is an argument of the request, not the
 * session's current tenant.
 *
 * The console reuses the declarative registration seam the tenant-facing
 * shell uses. Each administration capability contributes one page
 * declaration -- a path pattern, a view, an optional navigation label and
 * the permission code the page's data is held to (tenantAdminRoutes,
 * accountsAdminRoutes, sessionsAdminRoutes, impersonationsAdminRoutes,
 * permissionsAdminRoutes, invitationsAdminRoutes, ssoAdminRoutes,
 * settingsAdminRoutes, smsAdminRoutes, auditAdminRoutes, or the whole set
 * as consoleRoutes) -- and the application's composition layer collects
 * the declarations and renders them through the route layer it chose. This
 * package imports no router and does no path matching: ConsoleShell
 * carries the platform frame (a banner, a navigation derived from the
 * labelled declarations, and a `main` landmark) and renders the host's
 * routed content.
 *
 * The pages read and write through the console operations bundle the host
 * mounts in ConsoleOperationsProvider (useConsoleOperations): a plain
 * object of react-query read hooks and write mutations. The package ships
 * that bundle -- `consoleOperations`, bound to the generated @speed/api-sdk
 * surface -- so a host mounts it unchanged and no page builds a
 * QueryClient or writes a query key. The bundle is the seam through which
 * the /api/v1/system/tenants, /api/v1/system/auth, /api/v1/system, /api/v1/system/settings,
 * /api/v1/system/sms and /api/v1/system/audit operations reach the pages; see
 * operations.tsx for its members and their generated bindings.
 *
 * Every built-in string renders from the bilingual console-ui namespace
 * (CONSOLE_UI_NAMESPACE + consoleUiResources, which the host registers
 * alongside ui-kit's); the dialogs and empty/error states render through
 * ui-kit's shared primitives. Helpers shared between the components live
 * in src/internal/ and are deliberately not exported, except the type-only
 * callback types a host annotates its own handlers with and
 * ConsoleSessionProvider, the provider the shell mounts from its `session`
 * prop.
 */

export { CONSOLE_UI_NAMESPACE, consoleUiResources } from './resources.js'
export type {
  ConsoleNavGroup,
  ConsoleRoute,
  ConsoleRouteContext,
  ConsoleRouteParams,
  ConsoleRouteTable,
} from './route.js'
export { consoleNavGroups } from './nav-groups.js'
export { ConsoleShell, type ConsoleShellProps } from './ConsoleShell.js'
export { ConsoleProvider, type ConsoleProviderProps } from './ConsoleProvider.js'
export type { ConsoleDescribeRole } from './internal/describe-role.js'
export type { ConsoleImpersonationStarting } from './internal/impersonation-starting.js'
export type { ConsoleImpersonationChange } from './internal/impersonation-change.js'
export {
  ConsoleSessionProvider,
  type ConsoleSessionProviderProps,
} from './internal/session.js'
export {
  ConsoleOperationsProvider,
  consoleOperations,
  useConsoleOperations,
  settingKeyRest,
  type ConsoleOperations,
  type ConsoleOperationsProviderProps,
  type ConsoleTenant,
  type ConsoleTenantStatus,
  type ConsoleTenantQuery,
  type ConsoleSetting,
  type ConsoleAuditRecord,
  type ConsoleAuditPage,
  type ConsoleAuditRecordQuery,
  type ConsoleAuditRecordsOptions,
  type ConsoleCreateTenantInput,
  type ConsoleSetTenantAliasInput,
  type ConsoleSetTenantStatusInput,
  type ConsoleSetSettingInput,
  type ConsoleRemoveSettingInput,
  type ConsolePermission,
  type ConsoleRole,
  type ConsoleGrant,
  type ConsoleBinding,
  type ConsoleWriteRoleInput,
  type ConsoleRoleRef,
  type ConsoleGrantRef,
  type ConsoleBindingRef,
  type ConsoleAccountBindingsQuery,
  type ConsoleAccount,
  type ConsoleAccountPage,
  type ConsoleAccountFactor,
  type ConsoleAccountSecret,
  type ConsoleAccountSecretCreated,
  type ConsoleAccountSession,
  type ConsoleImpersonation,
  type ConsoleAccountRef,
  type ConsoleAccountQuery,
  type ConsoleCreateAccountInput,
  type ConsolePatchAccountInput,
  type ConsoleSetAccountStatusInput,
  type ConsoleSetMFARequiredInput,
  type ConsoleResetPasswordInput,
  type ConsoleAddIdentifierInput,
  type ConsoleCreateSecretInput,
  type ConsoleRemoveIdentifierInput,
  type ConsoleRevokeSecretInput,
  type ConsoleRevokeSessionInput,
  type ConsoleInvitation,
  type ConsoleInvitationPage,
  type ConsoleMember,
  type ConsoleIssueInvitationInput,
  type ConsoleIssuedInvitation,
  type ConsoleInvitationRef,
  type ConsoleMemberRef,
  type ConsoleWriteMemberInput,
  type ConsoleSetMemberSSOExemptInput,
  type ConsoleSsoProvider,
  type ConsoleSsoProviderQuery,
  type ConsoleSsoProviderRef,
  type ConsoleSsoDomainRef,
  type ConsoleSsoVerifyToken,
  type ConsoleCreateSsoProviderInput,
  type ConsoleWriteSsoProviderInput,
  type ConsoleClaimSsoDomainInput,
  type ConsoleSmsTemplate,
  type ConsoleSmsTemplatePage,
  type ConsoleRegisterSmsTemplateInput,
  type ConsoleOfferSmsTemplateInput,
  type ConsoleSetSmsTemplateEnabledInput,
} from './operations.js'
export {
  dashboardAdminRoutes,
  tenantAdminRoutes,
  accountsAdminRoutes,
  sessionsAdminRoutes,
  impersonationsAdminRoutes,
  permissionsAdminRoutes,
  invitationsAdminRoutes,
  ssoAdminRoutes,
  settingsAdminRoutes,
  smsAdminRoutes,
  auditAdminRoutes,
  consoleRoutes,
} from './routes.js'
export {
  DashboardPage,
  KpiCard,
  KpiRow,
  type DashboardPageProps,
} from './DashboardPage.js'
export {
  TenantRosterPage,
  type TenantRosterPageProps,
} from './TenantRosterPage.js'
export {
  TenantDetailPage,
  type ConsoleTenantRelatedPage,
  type TenantDetailPageProps,
} from './TenantDetailPage.js'
export { SettingsPage } from './SettingsPage.js'
export { AuditPage } from './AuditPage.js'
export {
  AuditDetailPage,
  type AuditDetailPageProps,
} from './AuditDetailPage.js'
export {
  PermissionsPage,
  type PermissionsPageProps,
} from './PermissionsPage.js'
export {
  AccountsPage,
  type AccountsPageProps,
  type AccountsPageExtraColumn,
  type AccountsPageAttributeField,
} from './AccountsPage.js'
export { SessionsPage } from './SessionsPage.js'
export {
  ImpersonationsPage,
  type ImpersonationsPageProps,
} from './ImpersonationsPage.js'
export {
  InvitationsPage,
  type InvitationsPageProps,
} from './InvitationsPage.js'
export { SsoPage, type SsoPageProps } from './SsoPage.js'
export { SmsPage } from './SmsPage.js'
