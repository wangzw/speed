/**
 * Public entry of @speed/admin-ui.
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
 * as adminRoutes) -- and the application's composition layer collects
 * the declarations and renders them through the route layer it chose. This
 * package imports no router and does no path matching: AdminShell
 * carries the platform frame (a banner, a navigation derived from the
 * labelled declarations, and a `main` landmark) and renders the host's
 * routed content.
 *
 * The pages read and write through the console operations bundle the host
 * mounts in AdminOperationsProvider (useAdminOperations): a plain
 * object of react-query read hooks and write mutations. The package ships
 * that bundle -- `adminOperations`, bound to the generated @speed/api-sdk
 * surface -- so a host mounts it unchanged and no page builds a
 * QueryClient or writes a query key. The bundle is the seam through which
 * the /api/v1/system/tenants, /api/v1/system/auth, /api/v1/system, /api/v1/system/settings,
 * /api/v1/system/sms and /api/v1/system/audit operations reach the pages; see
 * operations.tsx for its members and their generated bindings.
 *
 * The same package also ships the organization administration surface: the
 * tenant-scoped half, mounted under a host-chosen prefix (the demo uses
 * /organization/admin) inside @speed/product-shell. It contributes the
 * `organizationAdminRoutes` declarations, the frameless
 * `OrganizationAdminProvider` (current tenant, tenant-scoped operations,
 * session, feature presence, route table, navigation callback, breadcrumb
 * and protected roles), the standalone `OrganizationAdminBreadcrumbProvider`,
 * the `OrganizationAdminOperations` bundle, and the thin page wrappers that
 * hand the current tenant to the shared page components. It renders no
 * frame of its own and imports neither @speed/route-kit,
 * @speed/tenancy-ui nor @speed/product-shell. The organization surface ships
 * the invitations and membership, SSO, tenant roles and permissions and
 * tenant settings wrappers.
 *
 * Every built-in string renders from the bilingual admin-ui namespace
 * (ADMIN_UI_NAMESPACE + adminUiResources, which the host registers
 * alongside ui-kit's); the dialogs and empty/error states render through
 * ui-kit's shared primitives. Helpers shared between the components live
 * in src/internal/ and are deliberately not exported, except the type-only
 * callback types a host annotates its own handlers with and
 * AdminSessionProvider, the provider the shell mounts from its `session`
 * prop.
 */

export { ADMIN_UI_NAMESPACE, adminUiResources } from './resources.js'
export type {
  AdminNavGroup,
  AdminRoute,
  AdminRouteContext,
  AdminRouteParams,
  AdminRouteTable,
} from './route.js'
export { adminNavGroups } from './nav-groups.js'
export { AdminShell, type AdminShellProps } from './AdminShell.js'
export { AdminProvider, type AdminProviderProps } from './AdminProvider.js'
export type { AdminImpersonationStarting } from './internal/impersonation-starting.js'
export type { AdminImpersonationChange } from './internal/impersonation-change.js'
export {
  AdminSessionProvider,
  type AdminSessionProviderProps,
} from './internal/session.js'
export {
  AdminOperationsProvider,
  adminOperations,
  useAdminOperations,
  settingKeyRest,
  type AdminOperations,
  type AdminOperationsProviderProps,
  type AdminTenant,
  type AdminTenantStatus,
  type AdminTenantQuery,
  type AdminSetting,
  type AdminAuditRecord,
  type AdminAuditPage,
  type AdminAuditRecordQuery,
  type AdminAuditRecordsOptions,
  type AdminCreateTenantInput,
  type AdminSetTenantStatusInput,
  type AdminSetTenantProfileInput,
  type AdminSetSettingInput,
  type AdminRemoveSettingInput,
  type AdminPermission,
  type AdminRole,
  type AdminGrant,
  type AdminBinding,
  type AdminWriteRoleInput,
  type AdminRoleRef,
  type AdminGrantRef,
  type AdminBindingRef,
  type AdminAccountBindingsQuery,
  type AdminAccount,
  type AdminAccountPage,
  type AdminAccountFactor,
  type AdminAccountSecret,
  type AdminAccountSecretCreated,
  type AdminAccountSession,
  type AdminImpersonation,
  type AdminAccountRef,
  type AdminAccountQuery,
  type AdminCreateAccountInput,
  type AdminPatchAccountInput,
  type AdminSetAccountStatusInput,
  type AdminSetMFARequiredInput,
  type AdminResetPasswordInput,
  type AdminAddIdentifierInput,
  type AdminCreateSecretInput,
  type AdminRemoveIdentifierInput,
  type AdminRevokeSecretInput,
  type AdminRevokeSessionInput,
  type AdminInvitation,
  type AdminInvitationPage,
  type AdminMember,
  type AdminIssueInvitationInput,
  type AdminIssuedInvitation,
  type AdminInvitationRef,
  type AdminMemberRef,
  type AdminWriteMemberInput,
  type AdminSetMemberSSOExemptInput,
  type AdminSsoProvider,
  type AdminSsoProviderQuery,
  type AdminSsoProviderRef,
  type AdminSsoDomainRef,
  type AdminSsoVerifyToken,
  type AdminCreateSsoProviderInput,
  type AdminWriteSsoProviderInput,
  type AdminClaimSsoDomainInput,
  type AdminSmsTemplate,
  type AdminSmsTemplatePage,
  type AdminRegisterSmsTemplateInput,
  type AdminOfferSmsTemplateInput,
  type AdminSetSmsTemplateEnabledInput,
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
  adminRoutes,
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
  TenantCreatePage,
  type TenantCreatePageProps,
} from './TenantCreatePage.js'
export {
  TenantDetailPage,
  type AdminTenantRelatedPage,
  type TenantDetailPageProps,
} from './TenantDetailPage.js'
export {
  TenantProfilePage,
  type TenantProfilePageProps,
} from './TenantProfilePage.js'
export {
  TenantProfileForm,
  type TenantProfileFormProps,
} from './TenantProfileForm.js'
export {
  displayNameOf,
  hasTenantProfileFieldErrors,
  profileFieldsOf,
  profileWriteOf,
  validateTenantProfileFields,
  TENANT_PROFILE_LIMITS,
  type TenantProfileFieldError,
  type TenantProfileFieldErrors,
  type TenantProfileFields,
  type TenantProfileWriteBody,
} from './TenantProfileFields.js'
export { SettingsPage } from './SettingsPage.js'
export { OrganizationSecurityPage } from './OrganizationSecurityPage.js'
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
export { organizationAdminRoutes } from './organization-routes.js'
export {
  OrganizationInvitationsPage,
  OrganizationPermissionsPage,
  OrganizationProfilePage,
  OrganizationSettingsPage,
  OrganizationSsoPage,
} from './organization-pages.js'
export {
  OrganizationAdminProvider,
  useOrganizationAdminContext,
  useOrganizationAdminOnOrganizationLeft,
  useOrganizationAdminOnTenantUpdated,
  useOrganizationAdminProtectedRoles,
  useOrganizationAdminTenant,
  type OrganizationAdminContextValue,
  type OrganizationAdminProviderProps,
} from './OrganizationAdminProvider.js'
export {
  OrganizationAdminBreadcrumbProvider,
  useOrganizationAdminBreadcrumbs,
  type OrganizationAdminBreadcrumbProviderProps,
} from './OrganizationAdminBreadcrumb.js'
export {
  OrganizationAdminOperationsProvider,
  useOrganizationAdminOperations,
  organizationAdminOperations,
  type AdminTenantBinding,
  type AdminTenantBindingRef,
  type AdminTenantGrant,
  type AdminTenantGrantRef,
  type AdminTenantPermission,
  type AdminTenantOwnerTransferInput,
  type AdminTenantProfile,
  type AdminTenantProfileWriteInput,
  type AdminTenantRef,
  type AdminTenantRemoveSettingInput,
  type AdminTenantRole,
  type AdminTenantRoleRef,
  type AdminTenantSetSettingInput,
  type AdminTenantSetting,
  type AdminTenantSettingSource,
  type AdminTenantSettings,
  type AdminTenantWriteRoleInput,
  type OrganizationAdminOperations,
  type OrganizationAdminOperationsProviderProps,
} from './organization-operations.js'
