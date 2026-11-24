/** Public entry of @speed/admin-ui: the console family and the organization surface. */

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
  type AdminRequestTenantOwnerInput,
  type AdminCancelTenantOwnerTransferInput,
  type AdminTenantTransfer,
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
  type AdminTenantOwnerRequestInput,
  type AdminTenantOwnerRequestRef,
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
