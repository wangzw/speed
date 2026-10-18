/**
 * Public entry of @speed/console-ui.
 *
 * The platform-staff console component family -- the second shell. It
 * renders the platform's backend administration surfaces as one console
 * frame: the tenant roster (/tenant/admin), the account, session and
 * impersonation surfaces (/auth/admin), the authorization surface
 * (/authz/admin), the invitation and membership surface
 * (/auth/admin/tenants/:tenant/...), the SSO surface (/auth/admin/sso),
 * the settings read-write surface (/settings/admin), the SMS template
 * surface (/sms/admin) and the audit query (/audit/admin). The console
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
 * the /tenant/admin, /auth/admin, /authz/admin, /settings/admin,
 * /sms/admin and /audit/admin operations reach the pages; see
 * operations.tsx for its members and their generated bindings.
 *
 * Every built-in string renders from the bilingual console-ui namespace
 * (CONSOLE_UI_NAMESPACE + consoleUiResources, which the host registers
 * alongside ui-kit's); the dialogs and empty/error states render through
 * ui-kit's shared primitives. Helpers shared between the components live
 * in src/internal/ and are deliberately not exported.
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
export { DashboardPage } from './DashboardPage.js'
export { TenantRosterPage } from './TenantRosterPage.js'
export {
  TenantDetailPage,
  type TenantDetailPageProps,
} from './TenantDetailPage.js'
export { SettingsPage } from './SettingsPage.js'
export { AuditPage } from './AuditPage.js'
export {
  AuditDetailPage,
  type AuditDetailPageProps,
} from './AuditDetailPage.js'
export { PermissionsPage } from './PermissionsPage.js'
export { AccountsPage } from './AccountsPage.js'
export { SessionsPage } from './SessionsPage.js'
export { ImpersonationsPage } from './ImpersonationsPage.js'
export { InvitationsPage } from './InvitationsPage.js'
export { SsoPage } from './SsoPage.js'
export { SmsPage } from './SmsPage.js'
