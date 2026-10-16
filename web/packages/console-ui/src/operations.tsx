/**
 * The console operations seam: the read hooks and write mutations the
 * console pages consume, shipped over the generated @speed/api-sdk
 * administration operations.
 *
 * The pages read and write through the operations bundle they get from
 * useConsoleOperations -- a plain object whose members are the
 * react-query read hooks and write mutations of the administration
 * surfaces the pages render: the tenant roster, settings and audit
 * surfaces over /tenant/admin, /settings/admin and /audit/admin; the
 * account, session, impersonation, invitation and membership and SSO
 * surfaces over /auth and /authz/admin; and the SMS template surface over
 * /sms/admin. The bundle the package ships (consoleOperations) is a thin
 * adapter over those generated operations: the reads run the generated
 * query options through useQuery with a `select` that flattens the page
 * envelope to the view rows, and the writes call the generated mutation
 * functions with one flat input, invalidating the list the write belongs
 * to through the generated key factories. The hooks run on the host's
 * QueryClient (the host supplies it as it supplies the rest of the React
 * tree), the mutations go through the same bound client, and a console
 * page never builds a QueryClient of its own and never hand-writes a
 * query key.
 *
 * ConsoleOperationsProvider overrides the shipped bundle: a host that
 * wants different wiring, or a suite that scripts one, mounts its own and
 * useConsoleOperations answers with it. With no provider mounted the pages
 * read the shipped consoleOperations, so a host that composes the console
 * over a bound @speed/api-sdk client needs no binding of its own.
 *
 * The view types below are the generated response rows themselves of each
 * surface (TenantAdminTenant, SettingsAdminSetting, AuditAdminRecord, the
 * authn and authz admin views, SmsAdminTemplate): the modules' OpenAPI
 * fragments declare their 2xx bodies, so orval emits the row shapes and
 * the console names each for its own surface rather than hand-writing a
 * parallel model. Only the write *inputs* stay hand-written -- they carry
 * route parameters beside the body, which no single generated type covers.
 *
 * The settings reads and writes target the global layer: the console is
 * the platform-staff surface, and the global layer is the platform's --
 * the tenant layer belongs to that tenant's own surface.
 */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { UseMutationResult, UseQueryResult } from '@tanstack/react-query'
import {
  authnAdminAccountsAddIdentifier,
  authnAdminAccountsCreate,
  authnAdminAccountsDelete,
  authnAdminAccountsPatch,
  authnAdminAccountsRemoveIdentifier,
  authnAdminAccountsResetMFA,
  authnAdminAccountsResetPassword,
  authnAdminAccountsRevokeSessions,
  authnAdminAccountsSecretsCreate,
  authnAdminAccountsSecretsRevoke,
  authnAdminAccountsSetMFARequired,
  authnAdminAccountsStatus,
  authnAdminImpersonationsRevoke,
  authnAdminMembersPut,
  authnAdminMembersRemove,
  authnAdminMembersSsoExempt,
  authnAdminSessionsRevoke,
  authnAdminSsoProvidersClaimDomain,
  authnAdminSsoProvidersCreate,
  authnAdminSsoProvidersDelete,
  authnAdminSsoProvidersRefreshMetadata,
  authnAdminSsoProvidersRemoveDomain,
  authnAdminSsoProvidersReverifyDomains,
  authnAdminSsoProvidersUpdate,
  authnAdminSsoProvidersVerifyDomain,
  authnInviteIssue,
  authnInviteRevoke,
  authzAdminBindingsDelete,
  authzAdminBindingsPut,
  authzAdminGrantsDelete,
  authzAdminGrantsPut,
  authzAdminRolesDelete,
  authzAdminRolesPut,
  getAuditAdminRecordsGetQueryOptions,
  getAuditAdminRecordsListQueryOptions,
  getAuthnAdminAccountsFactorsQueryKey,
  getAuthnAdminAccountsFactorsQueryOptions,
  getAuthnAdminAccountsGetQueryKey,
  getAuthnAdminAccountsGetQueryOptions,
  getAuthnAdminAccountsListQueryKey,
  getAuthnAdminAccountsListQueryOptions,
  getAuthnAdminAccountsSecretsListQueryKey,
  getAuthnAdminAccountsSecretsListQueryOptions,
  getAuthnAdminAccountsSessionsQueryKey,
  getAuthnAdminAccountsSessionsQueryOptions,
  getAuthnAdminImpersonationsListQueryKey,
  getAuthnAdminImpersonationsListQueryOptions,
  getAuthnAdminMembersListQueryKey,
  getAuthnAdminMembersListQueryOptions,
  getAuthnAdminSsoProvidersGetQueryKey,
  getAuthnAdminSsoProvidersGetQueryOptions,
  getAuthnAdminSsoProvidersListQueryKey,
  getAuthnAdminSsoProvidersListQueryOptions,
  getAuthnInviteListQueryKey,
  getAuthnInviteListQueryOptions,
  getAuthzAdminBindingsListQueryKey,
  getAuthzAdminBindingsListQueryOptions,
  getAuthzAdminBindingsOfQueryKey,
  getAuthzAdminBindingsOfQueryOptions,
  getAuthzAdminGrantsListQueryKey,
  getAuthzAdminGrantsListQueryOptions,
  getAuthzAdminPermissionsListQueryOptions,
  getAuthzAdminRolesListQueryKey,
  getAuthzAdminRolesListQueryOptions,
  getSettingsAdminSettingsListNamespaceQueryKey,
  getSettingsAdminSettingsListNamespaceQueryOptions,
  getSettingsAdminSettingsListQueryKey,
  getSettingsAdminSettingsListQueryOptions,
  getSmsAdminTemplatesListQueryKey,
  getSmsAdminTemplatesListQueryOptions,
  getTenantAdminTenantsGetQueryKey,
  getTenantAdminTenantsGetQueryOptions,
  getTenantAdminTenantsListQueryKey,
  getTenantAdminTenantsListQueryOptions,
  settingsAdminSettingsRemove,
  settingsAdminSettingsSet,
  smsAdminTemplatesOffer,
  smsAdminTemplatesRegister,
  smsAdminTemplatesRemove,
  smsAdminTemplatesSetEnabled,
  tenantAdminTenantsCreate,
  tenantAdminTenantsSetAlias,
  tenantAdminTenantsSetStatus,
} from '@speed/api-sdk'
import type {
  AuditAdminRecord,
  AuditAdminRecordPage,
  AuditAdminRecordsListParams,
  AuthnAdminAccount,
  AuthnAdminAccountCreate,
  AuthnAdminAccountList,
  AuthnAdminAccountPatch,
  AuthnAdminAccountStatus,
  AuthnAdminAccountsListParams,
  AuthnAdminDomainClaim,
  AuthnAdminFactor,
  AuthnAdminIdentifierWrite,
  AuthnAdminImpersonation,
  AuthnAdminImpersonationsListParams,
  AuthnAdminMember,
  AuthnAdminMemberSSOExempt,
  AuthnAdminMemberWrite,
  AuthnAdminMembersListParams,
  AuthnAdminMFARequired,
  AuthnAdminPasswordReset,
  AuthnAdminProvider,
  AuthnAdminProviderWrite,
  AuthnAdminSecret,
  AuthnAdminSecretCreate,
  AuthnAdminSecretCreated,
  AuthnAdminSession,
  AuthnAdminVerifyToken,
  AuthnInviteInvitation,
  AuthnInviteIssued,
  AuthnInviteListParams,
  AuthnInvitePage,
  AuthzAdminBindingView,
  AuthzAdminGrantView,
  AuthzAdminPermissionView,
  AuthzAdminRoleView,
  SettingsAdminSetting,
  SmsAdminTemplate,
  SmsAdminTemplatesList200,
  SmsAdminTemplatesListParams,
  TenantAdminTenant,
  TenantAdminTenantStatus,
} from '@speed/api-sdk'

/** The settings layer the console operates on: the global (platform)
 * layer, whose rows only platform staff may read and write. */
const GLOBAL_LAYER = { layer: 'global' } as const

/** One tenant as /tenant/admin shows it -- the generated row of
 * GET /tenant/admin/tenants, named for the console's own view. */
export type ConsoleTenant = TenantAdminTenant

/** The roster's closed status set: the only values a status write may
 * carry (the read side stays open -- an answer outside the set renders
 * the generic label rather than a raw value). The generated
 * TenantAdminTenantStatus enum. */
export type ConsoleTenantStatus = TenantAdminTenantStatus

/** One setting as /settings/admin shows it -- the generated row of
 * GET /settings/admin/settings. */
export type ConsoleSetting = SettingsAdminSetting

/** One audit record as /audit/admin shows it -- the generated row of
 * GET /audit/admin/records. */
export type ConsoleAuditRecord = AuditAdminRecord

/** One page of audit records: the rows and the next-page cursor. */
export type ConsoleAuditPage = AuditAdminRecordPage

/** An audit-record target for a read that stays disabled while no id is chosen. */
export interface ConsoleAuditRecordQuery {
  /** The record id, or undefined to read nothing. */
  readonly id: string | undefined
}

/** One page of accounts: the rows and the next-page cursor. */
export type ConsoleAccountPage = AuthnAdminAccountList

/** One page of invitations: the rows and the next-page cursor. */
export type ConsoleInvitationPage = AuthnInvitePage

/** One page of SMS templates: the rows and the next-page cursor. */
export type ConsoleSmsTemplatePage = SmsAdminTemplatesList200

/** POST /tenant/admin/tenants body: a tenant not registered yet. */
export interface ConsoleCreateTenantInput {
  readonly id: string
  readonly alias: string
}

/** PATCH /tenant/admin/tenants/:id body: a new alias. */
export interface ConsoleSetTenantAliasInput {
  readonly id: string
  readonly alias: string
}

/** POST /tenant/admin/tenants/:id/status body: a new state. */
export interface ConsoleSetTenantStatusInput {
  readonly id: string
  readonly status: ConsoleTenantStatus
}

/** A tenant target for a read that stays disabled while no id is chosen. */
export interface ConsoleTenantQuery {
  /** The tenant id, or undefined to read nothing. */
  readonly id: string | undefined
}

/**
 * PUT /settings/admin/settings/:namespace/:key body: the new value's text.
 * `namespace` is the module that declared the item (a module name may
 * contain a dot), and `key` is the rest of the key under it -- the full
 * key is `<namespace>.<key>` -- because that is how the route addresses
 * the item. ConsoleSetting carries the full key and the namespace; a
 * caller writing a row splits the full key with `settingKeyRest`.
 */
export interface ConsoleSetSettingInput {
  readonly namespace: string
  readonly key: string
  readonly value: string
}

/** DELETE /settings/admin/settings/:namespace/:key target (see ConsoleSetSettingInput). */
export interface ConsoleRemoveSettingInput {
  readonly namespace: string
  readonly key: string
}

/**
 * The `:key` route segment of a setting whose full key is `<namespace>.<key>`:
 * the rest of the key under the namespace. The namespace may itself contain
 * a dot, so the split is by the known namespace prefix, not the first dot.
 */
export function settingKeyRest(
  fullKey: string,
  namespace: string,
): string {
  const prefix = `${namespace}.`
  return fullKey.startsWith(prefix)
    ? fullKey.slice(prefix.length)
    : fullKey
}

// --- Authorization surface (/authz/admin) -------------------------------
// The authorization administration surface is tenant-scoped: the role,
// grant and binding routes all carry the tenant as a path segment, so
// every read and write here names the tenant it acts on (the tenant an
// operator picks on the page, not the session's current tenant).

/** One permission the catalog declares (GET /authz/admin/permissions). */
export type ConsolePermission = AuthzAdminPermissionView

/** One tenant role (GET /authz/admin/tenants/:tenant/roles). */
export type ConsoleRole = AuthzAdminRoleView

/** One permission grant a role holds (GET .../roles/:role/grants). */
export type ConsoleGrant = AuthzAdminGrantView

/** One role binding an account holds in a tenant (GET .../bindings). */
export type ConsoleBinding = AuthzAdminBindingView

/** PUT .../roles/:role body: a role's declared shape. */
export interface ConsoleWriteRoleInput {
  readonly tenant: string
  readonly role: string
  readonly builtin: boolean
  readonly descriptionKey: string
}

/** A tenant-and-role target (delete a role, list its grants). */
export interface ConsoleRoleRef {
  readonly tenant: string
  readonly role: string
}

/** A grant target: the role and the permission it grants or revokes. */
export interface ConsoleGrantRef {
  readonly tenant: string
  readonly role: string
  readonly permission: string
}

/** A binding target: the account and the role bound or unbound. */
export interface ConsoleBindingRef {
  readonly tenant: string
  readonly account: string
  readonly role: string
}

/** The account whose held bindings are read within a tenant. */
export interface ConsoleAccountBindingsQuery {
  readonly tenant: string
  /** The account to inspect; empty reads nothing (the read stays disabled). */
  readonly account: string | undefined
}

// --- Account surface (/auth/admin) --------------------------------------

/** One account as /auth/admin shows it (GET /auth/admin/accounts). */
export type ConsoleAccount = AuthnAdminAccount

/** One registered factor of an account (GET .../accounts/:id/factors). */
export type ConsoleAccountFactor = AuthnAdminFactor

/** One service secret of an account (GET .../accounts/:id/secrets). */
export type ConsoleAccountSecret = AuthnAdminSecret

/** The one-time handover a secret creation answers with (POST .../secrets). */
export type ConsoleAccountSecretCreated = AuthnAdminSecretCreated

/** One session of an account (GET .../accounts/:id/sessions). */
export type ConsoleAccountSession = AuthnAdminSession

/** One impersonation record (GET /auth/admin/impersonations). */
export type ConsoleImpersonation = AuthnAdminImpersonation

/** An account target (its detail reads and its writes). */
export interface ConsoleAccountRef {
  readonly id: string
}

/** An account target for a read that stays disabled while no id is chosen. */
export interface ConsoleAccountQuery {
  /** The account id, or undefined to read nothing. */
  readonly id: string | undefined
}

/** DELETE .../accounts/:id/identifiers/:identifier target. */
export interface ConsoleRemoveIdentifierInput {
  readonly id: string
  readonly identifier: string
}

/** DELETE .../accounts/:id/secrets/:secret target. */
export interface ConsoleRevokeSecretInput {
  readonly id: string
  readonly secret: string
}

/**
 * POST /auth/admin/accounts body: the fields a create names. It is the
 * generated request shape itself, so a field the module's fragment adds
 * reaches the page through this type with no parallel model to keep in
 * step.
 */
export type ConsoleCreateAccountInput = AuthnAdminAccountCreate

/** PATCH /auth/admin/accounts/:id: the account id beside the fields to change. */
export type ConsolePatchAccountInput = AuthnAdminAccountPatch & {
  readonly id: string
}

/** POST .../accounts/:id/status: the account id beside its new state. */
export type ConsoleSetAccountStatusInput = AuthnAdminAccountStatus & {
  readonly id: string
}

/** POST .../accounts/:id/mfa-required: the account id beside the required flag. */
export type ConsoleSetMFARequiredInput = AuthnAdminMFARequired & {
  readonly id: string
}

/** POST .../accounts/:id/password: the account id beside the temporary password. */
export type ConsoleResetPasswordInput = AuthnAdminPasswordReset & {
  readonly id: string
}

/** POST .../accounts/:id/identifiers: the account id beside the identifier to add. */
export type ConsoleAddIdentifierInput = AuthnAdminIdentifierWrite & {
  readonly id: string
}

/** POST .../accounts/:id/secrets: the account id beside the secret's label and expiry. */
export type ConsoleCreateSecretInput = AuthnAdminSecretCreate & {
  readonly id: string
}

// --- Invitation and membership surface (tenant-scoped) ------------------

/** One invitation of a tenant (GET .../tenants/:tenant/invitations). */
export type ConsoleInvitation = AuthnInviteInvitation

/** The one-time handover an invitation issue answers with (POST .../invitations):
 * the invitation view plus the acceptance token, which is handed out once. */
export type ConsoleIssuedInvitation = AuthnInviteIssued

/** One tenant member (GET .../tenants/:tenant/members). */
export type ConsoleMember = AuthnAdminMember

/** POST .../invitations body: the address to invite, optional expiry. */
export interface ConsoleIssueInvitationInput {
  readonly tenant: string
  readonly email: string
  /** RFC 3339 expiry, or undefined for the module's default. */
  readonly expiresAt: string | undefined
}

/** DELETE .../invitations/:id target. */
export interface ConsoleInvitationRef {
  readonly tenant: string
  readonly id: string
}

/** DELETE .../members/:account target. */
export interface ConsoleMemberRef {
  readonly tenant: string
  readonly account: string
}

/** PUT .../members/:account body: the membership to write, with its target. */
export type ConsoleWriteMemberInput = AuthnAdminMemberWrite & {
  readonly tenant: string
  readonly account: string
}

/** PUT .../members/:account/sso-exempt body: the exemption flag, with its target. */
export type ConsoleSetMemberSSOExemptInput = AuthnAdminMemberSSOExempt & {
  readonly tenant: string
  readonly account: string
}

// --- SSO surface (/auth/admin/sso) --------------------------------------

/** One SSO provider (GET .../tenants/:tenant/sso/providers, or .../providers/:id). */
export type ConsoleSsoProvider = AuthnAdminProvider

/** A provider target for a read that stays disabled while no id is chosen. */
export interface ConsoleSsoProviderQuery {
  /** The provider id, or undefined to read nothing. */
  readonly id: string | undefined
}

/**
 * A provider write target. The provider's own routes address it by id,
 * but the list it belongs to is keyed by tenant, so a write carries the
 * tenant too and can invalidate that tenant's list.
 */
export interface ConsoleSsoProviderRef {
  readonly id: string
  readonly tenant: string
}

/** DELETE .../sso/providers/:id/domains/:domain target. */
export interface ConsoleSsoDomainRef {
  readonly id: string
  readonly tenant: string
  readonly domain: string
}

/** The token a domain claim answers with, to publish in DNS. */
export type ConsoleSsoVerifyToken = AuthnAdminVerifyToken

/** POST .../tenants/:tenant/sso/providers body: the provider to create. */
export type ConsoleCreateSsoProviderInput = AuthnAdminProviderWrite & {
  readonly tenant: string
}

/** PUT /auth/admin/sso/providers/:id body: the provider to rewrite, with its tenant. */
export type ConsoleWriteSsoProviderInput = AuthnAdminProviderWrite & {
  readonly id: string
  readonly tenant: string
}

/** POST .../sso/providers/:id/domains body: the domain to claim, with its provider. */
export type ConsoleClaimSsoDomainInput = AuthnAdminDomainClaim & {
  readonly id: string
  readonly tenant: string
}

/** Revoking one session: the session's id beside the account it belongs to,
 * so the account's session list can be invalidated. */
export interface ConsoleRevokeSessionInput {
  readonly accountId: string
  readonly sessionId: string
}

// --- SMS template surface (/sms/admin) ----------------------------------

/** One SMS template (GET /sms/admin/templates). */
export type ConsoleSmsTemplate = SmsAdminTemplate

/** POST /sms/admin/templates body: take up a template by vendor code. */
export interface ConsoleRegisterSmsTemplateInput {
  readonly name: string
  readonly params: readonly string[]
  /** The vendor code per language. */
  readonly codes: Readonly<Record<string, string>>
}

/**
 * POST /sms/admin/templates/submissions body: hand a template to the
 * vendor for review (the code arrives on a later read).
 */
export interface ConsoleOfferSmsTemplateInput {
  readonly name: string
  readonly content: string
  readonly kind: string
  readonly locale: string
  readonly params: readonly string[]
  readonly remark: string
}

/** POST /sms/admin/templates/:name/enabled body: the send switch. */
export interface ConsoleSetSmsTemplateEnabledInput {
  readonly name: string
  readonly enabled: boolean
}

/**
 * The console's data operations, in react-query hook shape. Every member
 * is a hook the pages call during render (on the host's QueryClient);
 * consoleOperations below binds them to the generated @speed/api-sdk
 * operations of the same shape, and a host or suite may bind its own.
 *
 * The members are grouped by administration surface. The reads whose
 * route carries a parameter (a tenant, a role, an account) take that
 * parameter so the query key namespaces by it; a read whose parameter is
 * undefined declares itself disabled rather than firing a malformed
 * request.
 */
export interface ConsoleOperations {
  /** GET /tenant/admin/tenants -- the whole roster, in the roster's order. */
  readonly useTenants: () => UseQueryResult<readonly ConsoleTenant[], unknown>
  /** GET /tenant/admin/tenants/:id -- one tenant (disabled while no id). */
  readonly useTenant: (
    query: ConsoleTenantQuery,
  ) => UseQueryResult<ConsoleTenant, unknown>
  /** POST /tenant/admin/tenants -- register a tenant not registered yet. */
  readonly useCreateTenant: () => UseMutationResult<
    ConsoleTenant,
    unknown,
    ConsoleCreateTenantInput
  >
  /** PATCH /tenant/admin/tenants/:id -- rename a tenant's alias. */
  readonly useSetTenantAlias: () => UseMutationResult<
    void,
    unknown,
    ConsoleSetTenantAliasInput
  >
  /** POST /tenant/admin/tenants/:id/status -- enable or disable a tenant. */
  readonly useSetTenantStatus: () => UseMutationResult<
    void,
    unknown,
    ConsoleSetTenantStatusInput
  >
  /** GET /settings/admin/settings[/:namespace] -- the declared items' views. */
  readonly useSettings: (
    namespace: string | undefined,
  ) => UseQueryResult<readonly ConsoleSetting[], unknown>
  /** PUT /settings/admin/settings/:namespace/:key -- write one item's value. */
  readonly useSetSetting: () => UseMutationResult<
    void,
    unknown,
    ConsoleSetSettingInput
  >
  /** DELETE /settings/admin/settings/:namespace/:key -- remove one item's override. */
  readonly useRemoveSetting: () => UseMutationResult<
    void,
    unknown,
    ConsoleRemoveSettingInput
  >
  /**
   * GET /audit/admin/records -- one page of audit records, newest first,
   * narrowed by the filters the caller passes (since / until / action /
   * cursor / limit). The read returns the page envelope (the entries and
   * the next cursor), so the page can offer server-side pagination.
   */
  readonly useAuditRecords: (
    params?: AuditAdminRecordsListParams,
  ) => UseQueryResult<ConsoleAuditPage, unknown>
  /** GET /audit/admin/records/:id -- one audit record (disabled while no id). */
  readonly useAuditRecord: (
    query: ConsoleAuditRecordQuery,
  ) => UseQueryResult<ConsoleAuditRecord, unknown>

  // --- Authorization surface (/authz/admin, tenant-scoped) --------------

  /** GET /authz/admin/permissions -- the whole permission catalog. */
  readonly usePermissions: () => UseQueryResult<
    readonly ConsolePermission[],
    unknown
  >
  /** GET /authz/admin/tenants/:tenant/roles -- a tenant's roles. */
  readonly useRoles: (
    tenant: string,
  ) => UseQueryResult<readonly ConsoleRole[], unknown>
  /** PUT /authz/admin/tenants/:tenant/roles/:role -- create or rewrite a role. */
  readonly useWriteRole: () => UseMutationResult<void, unknown, ConsoleWriteRoleInput>
  /** DELETE /authz/admin/tenants/:tenant/roles/:role -- remove a role. */
  readonly useRemoveRole: () => UseMutationResult<void, unknown, ConsoleRoleRef>
  /** GET /authz/admin/tenants/:tenant/roles/:role/grants -- a role's grants. */
  readonly useGrants: (
    tenant: string,
    role: string | undefined,
  ) => UseQueryResult<readonly ConsoleGrant[], unknown>
  /** PUT .../roles/:role/grants/:permission -- grant a permission to a role. */
  readonly useGrantPermission: () => UseMutationResult<void, unknown, ConsoleGrantRef>
  /** DELETE .../roles/:role/grants/:permission -- revoke a permission. */
  readonly useRevokePermission: () => UseMutationResult<void, unknown, ConsoleGrantRef>
  /** GET /authz/admin/tenants/:tenant/bindings -- a tenant's bindings. */
  readonly useBindings: (
    tenant: string,
  ) => UseQueryResult<readonly ConsoleBinding[], unknown>
  /** GET .../bindings/:account -- the bindings an account holds. */
  readonly useAccountBindings: (
    query: ConsoleAccountBindingsQuery,
  ) => UseQueryResult<readonly ConsoleBinding[], unknown>
  /** PUT .../bindings/:account/:role -- bind a role to an account. */
  readonly useBindRole: () => UseMutationResult<void, unknown, ConsoleBindingRef>
  /** DELETE .../bindings/:account/:role -- unbind a role from an account. */
  readonly useUnbindRole: () => UseMutationResult<void, unknown, ConsoleBindingRef>

  // --- Account surface (/auth/admin) ------------------------------------

  /** GET /auth/admin/accounts -- one page of accounts. */
  readonly useAccounts: (
    params?: AuthnAdminAccountsListParams,
  ) => UseQueryResult<ConsoleAccountPage, unknown>
  /** GET /auth/admin/accounts/:id -- one account (disabled while no id). */
  readonly useAccount: (
    query: ConsoleAccountQuery,
  ) => UseQueryResult<ConsoleAccount, unknown>
  /** GET .../accounts/:id/factors -- an account's registered factors. */
  readonly useAccountFactors: (
    query: ConsoleAccountQuery,
  ) => UseQueryResult<readonly ConsoleAccountFactor[], unknown>
  /** GET .../accounts/:id/secrets -- an account's service secrets. */
  readonly useAccountSecrets: (
    query: ConsoleAccountQuery,
  ) => UseQueryResult<readonly ConsoleAccountSecret[], unknown>
  /** GET .../accounts/:id/sessions -- an account's sessions. */
  readonly useAccountSessions: (
    query: ConsoleAccountQuery,
  ) => UseQueryResult<readonly ConsoleAccountSession[], unknown>
  /** DELETE /auth/admin/accounts/:id -- delete an account. */
  readonly useDeleteAccount: () => UseMutationResult<void, unknown, ConsoleAccountRef>
  /** DELETE .../accounts/:id/identifiers/:identifier -- remove an identifier. */
  readonly useRemoveIdentifier: () => UseMutationResult<
    void,
    unknown,
    ConsoleRemoveIdentifierInput
  >
  /** POST .../accounts/:id/mfa-reset -- remove an account's factors. */
  readonly useResetMFA: () => UseMutationResult<void, unknown, ConsoleAccountRef>
  /** POST .../accounts/:id/revoke-sessions -- revoke every session. */
  readonly useRevokeAccountSessions: () => UseMutationResult<
    void,
    unknown,
    ConsoleAccountRef
  >
  /** DELETE .../accounts/:id/secrets/:secret -- revoke a service secret. */
  readonly useRevokeSecret: () => UseMutationResult<
    void,
    unknown,
    ConsoleRevokeSecretInput
  >
  /** DELETE /auth/admin/sessions/:id -- revoke one session by id. */
  readonly useRevokeSession: () => UseMutationResult<
    void,
    unknown,
    ConsoleRevokeSessionInput
  >
  /** POST /auth/admin/accounts -- create an account. */
  readonly useCreateAccount: () => UseMutationResult<
    ConsoleAccount,
    unknown,
    ConsoleCreateAccountInput
  >
  /** PATCH /auth/admin/accounts/:id -- change a display name, an attribute set or the protected flag. */
  readonly usePatchAccount: () => UseMutationResult<
    ConsoleAccount,
    unknown,
    ConsolePatchAccountInput
  >
  /** POST .../accounts/:id/status -- enable or disable an account. */
  readonly useSetAccountStatus: () => UseMutationResult<
    ConsoleAccount,
    unknown,
    ConsoleSetAccountStatusInput
  >
  /** POST .../accounts/:id/mfa-required -- set whether a factor must be registered. */
  readonly useSetMFARequired: () => UseMutationResult<
    void,
    unknown,
    ConsoleSetMFARequiredInput
  >
  /** POST .../accounts/:id/password -- set a temporary password that must be changed at next login. */
  readonly useResetPassword: () => UseMutationResult<
    void,
    unknown,
    ConsoleResetPasswordInput
  >
  /** POST .../accounts/:id/identifiers -- add a login name or email an administrator asserts. */
  readonly useAddIdentifier: () => UseMutationResult<
    void,
    unknown,
    ConsoleAddIdentifierInput
  >
  /** POST .../accounts/:id/secrets -- mint a service secret, handed over once. */
  readonly useCreateSecret: () => UseMutationResult<
    ConsoleAccountSecretCreated,
    unknown,
    ConsoleCreateSecretInput
  >

  // --- Impersonation surface (/auth/admin) ------------------------------

  /**
   * GET /auth/admin/impersonations -- one page of impersonation records.
   * The read is tenant-scoped by a required query parameter, so it stays
   * disabled while the caller names no tenant.
   */
  readonly useImpersonations: (
    params: AuthnAdminImpersonationsListParams,
  ) => UseQueryResult<readonly ConsoleImpersonation[], unknown>
  /** DELETE /auth/admin/impersonations/:id -- terminate an impersonation. */
  readonly useRevokeImpersonation: () => UseMutationResult<
    void,
    unknown,
    ConsoleAccountRef
  >

  // --- Invitation surface (tenant-scoped) -------------------------------

  /** GET .../tenants/:tenant/invitations -- a tenant's invitations. */
  readonly useInvitations: (
    tenant: string,
    params?: AuthnInviteListParams,
  ) => UseQueryResult<ConsoleInvitationPage, unknown>
  /** POST .../tenants/:tenant/invitations -- invite an address. The answer
   * carries the acceptance token, handed out once to give to the invitee. */
  readonly useIssueInvitation: () => UseMutationResult<
    ConsoleIssuedInvitation,
    unknown,
    ConsoleIssueInvitationInput
  >
  /** DELETE .../tenants/:tenant/invitations/:id -- revoke an invitation. */
  readonly useRevokeInvitation: () => UseMutationResult<
    void,
    unknown,
    ConsoleInvitationRef
  >

  // --- Membership surface (tenant-scoped) -------------------------------

  /** GET .../tenants/:tenant/members -- a tenant's members. */
  readonly useMembers: (
    tenant: string,
    params?: AuthnAdminMembersListParams,
  ) => UseQueryResult<readonly ConsoleMember[], unknown>
  /** DELETE .../tenants/:tenant/members/:account -- remove a membership. */
  readonly useRemoveMember: () => UseMutationResult<void, unknown, ConsoleMemberRef>
  /** PUT .../tenants/:tenant/members/:account -- add, suspend or resume a membership. */
  readonly useWriteMember: () => UseMutationResult<
    void,
    unknown,
    ConsoleWriteMemberInput
  >
  /** PUT .../members/:account/sso-exempt -- set or clear the SSO exemption. */
  readonly useSetMemberSsoExempt: () => UseMutationResult<
    void,
    unknown,
    ConsoleSetMemberSSOExemptInput
  >

  // --- SSO surface (/auth/admin/sso) ------------------------------------

  /** GET .../tenants/:tenant/sso/providers -- a tenant's providers. */
  readonly useSsoProviders: (
    tenant: string,
  ) => UseQueryResult<readonly ConsoleSsoProvider[], unknown>
  /** GET /auth/admin/sso/providers/:id -- one provider (disabled while no id). */
  readonly useSsoProvider: (
    query: ConsoleSsoProviderQuery,
  ) => UseQueryResult<ConsoleSsoProvider, unknown>
  /** DELETE /auth/admin/sso/providers/:id -- delete a provider. */
  readonly useDeleteSsoProvider: () => UseMutationResult<
    void,
    unknown,
    ConsoleSsoProviderRef
  >
  /** DELETE .../providers/:id/domains/:domain -- remove a claimed domain. */
  readonly useRemoveSsoDomain: () => UseMutationResult<
    void,
    unknown,
    ConsoleSsoDomainRef
  >
  /** POST .../providers/:id/domains/:domain/verify -- verify a claimed domain. */
  readonly useVerifySsoDomain: () => UseMutationResult<
    void,
    unknown,
    ConsoleSsoDomainRef
  >
  /** POST .../providers/:id/metadata/refresh -- refresh provider metadata. */
  readonly useRefreshSsoMetadata: () => UseMutationResult<
    void,
    unknown,
    ConsoleSsoProviderRef
  >
  /** POST .../providers/:id/reverify-domains -- re-verify every domain. */
  readonly useReverifySsoDomains: () => UseMutationResult<
    void,
    unknown,
    ConsoleSsoProviderRef
  >
  /** POST .../tenants/:tenant/sso/providers -- create a provider in a tenant. */
  readonly useCreateSsoProvider: () => UseMutationResult<
    ConsoleSsoProvider,
    unknown,
    ConsoleCreateSsoProviderInput
  >
  /** PUT /auth/admin/sso/providers/:id -- rewrite a provider. */
  readonly useUpdateSsoProvider: () => UseMutationResult<
    ConsoleSsoProvider,
    unknown,
    ConsoleWriteSsoProviderInput
  >
  /** POST .../providers/:id/domains -- claim a domain, returning the token to publish. */
  readonly useClaimSsoDomain: () => UseMutationResult<
    ConsoleSsoVerifyToken,
    unknown,
    ConsoleClaimSsoDomainInput
  >

  // --- SMS template surface (/sms/admin) --------------------------------

  /** GET /sms/admin/templates -- one page of templates. */
  readonly useSmsTemplates: (
    params?: SmsAdminTemplatesListParams,
  ) => UseQueryResult<ConsoleSmsTemplatePage, unknown>
  /** POST /sms/admin/templates -- take up a template by vendor code. */
  readonly useRegisterSmsTemplate: () => UseMutationResult<
    void,
    unknown,
    ConsoleRegisterSmsTemplateInput
  >
  /** POST /sms/admin/templates/submissions -- offer a template for review. */
  readonly useOfferSmsTemplate: () => UseMutationResult<
    void,
    unknown,
    ConsoleOfferSmsTemplateInput
  >
  /** POST /sms/admin/templates/:name/enabled -- turn sending on or off. */
  readonly useSetSmsTemplateEnabled: () => UseMutationResult<
    void,
    unknown,
    ConsoleSetSmsTemplateEnabledInput
  >
  /** DELETE /sms/admin/templates/:name -- drop a runtime template. */
  readonly useRemoveSmsTemplate: () => UseMutationResult<
    void,
    unknown,
    { readonly name: string }
  >
}

/**
 * The shipped operations bundle: every member is the generated
 * @speed/api-sdk administration operation of the same shape, adapted to
 * the view shapes above. `useConsoleOperations` answers with it whenever
 * no ConsoleOperationsProvider overrides it, so the console pages run on
 * the generated surface with no host-side wiring.
 */
export const consoleOperations: ConsoleOperations = {
  useTenants: () =>
    useQuery({
      ...getTenantAdminTenantsListQueryOptions(),
      select: (page) => page.tenants,
    }),

  useTenant: ({ id }) =>
    useQuery({
      ...getTenantAdminTenantsGetQueryOptions(id ?? ''),
      select: (envelope) => envelope.tenant,
      enabled: id !== undefined && id !== '',
    }),

  useCreateTenant: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleCreateTenantInput) =>
        tenantAdminTenantsCreate({ id: input.id, alias: input.alias }).then(
          (answer) => answer.tenant,
        ),
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsListQueryKey(),
        })
      },
    })
  },

  useSetTenantAlias: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleSetTenantAliasInput) =>
        tenantAdminTenantsSetAlias(input.id, { alias: input.alias }),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsListQueryKey(),
        })
        // The tenant's own detail read is a separate key that the list key
        // does not prefix, so it is invalidated on its own: a rename or a
        // status change made on the detail page refreshes that page's read.
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsGetQueryKey(input.id),
        })
      },
    })
  },

  useSetTenantStatus: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleSetTenantStatusInput) =>
        tenantAdminTenantsSetStatus(input.id, { status: input.status }),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsListQueryKey(),
        })
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsGetQueryKey(input.id),
        })
      },
    })
  },

  // The filter picks between the all-namespaces read and the one-namespace
  // read; both queries are declared every render and exactly one is enabled,
  // so the pages never call a hook conditionally (the rules of hooks) and an
  // unfiltered read never fires the per-namespace request.
  useSettings: (namespace) => {
    const all = useQuery({
      ...getSettingsAdminSettingsListQueryOptions({ ...GLOBAL_LAYER }),
      enabled: namespace === undefined,
    })
    const one = useQuery({
      ...getSettingsAdminSettingsListNamespaceQueryOptions(namespace ?? '', {
        ...GLOBAL_LAYER,
      }),
      enabled: namespace !== undefined,
    })
    return namespace === undefined ? all : one
  },

  useSetSetting: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleSetSettingInput) =>
        settingsAdminSettingsSet(
          input.namespace,
          input.key,
          { value: input.value },
          { ...GLOBAL_LAYER },
        ),
      onSuccess: (_answer, input) => {
        invalidateSetting(queryClient, input.namespace)
      },
    })
  },

  useRemoveSetting: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleRemoveSettingInput) =>
        settingsAdminSettingsRemove(input.namespace, input.key, {
          ...GLOBAL_LAYER,
        }),
      onSuccess: (_answer, input) => {
        invalidateSetting(queryClient, input.namespace)
      },
    })
  },

  useAuditRecords: (params) =>
    useQuery({ ...getAuditAdminRecordsListQueryOptions(params) }),

  useAuditRecord: ({ id }) =>
    useQuery({
      ...getAuditAdminRecordsGetQueryOptions(id ?? ''),
      enabled: id !== undefined && id !== '',
    }),

  // --- Authorization surface (/authz/admin, tenant-scoped) --------------

  usePermissions: () =>
    useQuery({
      ...getAuthzAdminPermissionsListQueryOptions(),
      select: (page) => page.permissions,
    }),

  useRoles: (tenant) =>
    useQuery({
      ...getAuthzAdminRolesListQueryOptions(tenant),
      select: (page) => page.roles,
      enabled: tenant !== '',
    }),

  useWriteRole: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleWriteRoleInput) =>
        authzAdminRolesPut(input.tenant, input.role, {
          builtin: input.builtin,
          description_key: input.descriptionKey,
        }),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthzAdminRolesListQueryKey(input.tenant),
        })
      },
    })
  },

  useRemoveRole: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleRoleRef) =>
        authzAdminRolesDelete(input.tenant, input.role),
      onSuccess: (_answer, input) => {
        // Removing a role revokes its grants and bindings with it (the
        // generated route deletes all three), so both readers are stale.
        void queryClient.invalidateQueries({
          queryKey: getAuthzAdminRolesListQueryKey(input.tenant),
        })
        void queryClient.invalidateQueries({
          queryKey: getAuthzAdminBindingsListQueryKey(input.tenant),
        })
        void queryClient.invalidateQueries({
          queryKey: getAuthzAdminGrantsListQueryKey(input.tenant, input.role),
        })
      },
    })
  },

  useGrants: (tenant, role) =>
    useQuery({
      ...getAuthzAdminGrantsListQueryOptions(tenant, role ?? ''),
      select: (page) => page.grants,
      enabled: role !== undefined && role !== '',
    }),

  useGrantPermission: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleGrantRef) =>
        authzAdminGrantsPut(input.tenant, input.role, input.permission),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthzAdminGrantsListQueryKey(input.tenant, input.role),
        })
      },
    })
  },

  useRevokePermission: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleGrantRef) =>
        authzAdminGrantsDelete(input.tenant, input.role, input.permission),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthzAdminGrantsListQueryKey(input.tenant, input.role),
        })
      },
    })
  },

  useBindings: (tenant) =>
    useQuery({
      ...getAuthzAdminBindingsListQueryOptions(tenant),
      select: (page) => page.bindings,
      enabled: tenant !== '',
    }),

  useAccountBindings: ({ tenant, account }) =>
    useQuery({
      ...getAuthzAdminBindingsOfQueryOptions(tenant, account ?? ''),
      select: (page) => page.bindings,
      enabled: account !== undefined && account !== '',
    }),

  useBindRole: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleBindingRef) =>
        authzAdminBindingsPut(input.tenant, input.account, input.role),
      onSuccess: (_answer, input) => {
        invalidateBindings(queryClient, input.tenant, input.account)
      },
    })
  },

  useUnbindRole: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleBindingRef) =>
        authzAdminBindingsDelete(input.tenant, input.account, input.role),
      onSuccess: (_answer, input) => {
        invalidateBindings(queryClient, input.tenant, input.account)
      },
    })
  },

  // --- Account surface (/auth/admin) ------------------------------------

  useAccounts: (params) =>
    useQuery({
      ...getAuthnAdminAccountsListQueryOptions(params),
      select: (page) => page,
    }),

  useAccount: ({ id }) =>
    useQuery({
      ...getAuthnAdminAccountsGetQueryOptions(id ?? ''),
      select: (envelope) => envelope.account,
      enabled: id !== undefined && id !== '',
    }),

  useAccountFactors: ({ id }) =>
    useQuery({
      ...getAuthnAdminAccountsFactorsQueryOptions(id ?? ''),
      select: (page) => page.factors,
      enabled: id !== undefined && id !== '',
    }),

  useAccountSecrets: ({ id }) =>
    useQuery({
      ...getAuthnAdminAccountsSecretsListQueryOptions(id ?? ''),
      select: (page) => page.secrets,
      enabled: id !== undefined && id !== '',
    }),

  useAccountSessions: ({ id }) =>
    useQuery({
      ...getAuthnAdminAccountsSessionsQueryOptions(id ?? ''),
      select: (page) => page.sessions,
      enabled: id !== undefined && id !== '',
    }),

  useDeleteAccount: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleAccountRef) =>
        authnAdminAccountsDelete(input.id),
      onSuccess: (_answer, input) => {
        invalidateAccountLists(queryClient)
        queryClient.removeQueries({
          queryKey: getAuthnAdminAccountsGetQueryKey(input.id),
        })
      },
    })
  },

  useRemoveIdentifier: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleRemoveIdentifierInput) =>
        authnAdminAccountsRemoveIdentifier(input.id, input.identifier),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsGetQueryKey(input.id),
        })
      },
    })
  },

  useResetMFA: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleAccountRef) =>
        authnAdminAccountsResetMFA(input.id).then(() => undefined),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsFactorsQueryKey(input.id),
        })
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsGetQueryKey(input.id),
        })
      },
    })
  },

  useRevokeAccountSessions: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleAccountRef) =>
        authnAdminAccountsRevokeSessions(input.id).then(() => undefined),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsSessionsQueryKey(input.id),
        })
      },
    })
  },

  useRevokeSecret: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleRevokeSecretInput) =>
        authnAdminAccountsSecretsRevoke(input.id, input.secret),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsSecretsListQueryKey(input.id),
        })
      },
    })
  },

  useRevokeSession: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleRevokeSessionInput) =>
        authnAdminSessionsRevoke(input.sessionId),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsSessionsQueryKey(input.accountId),
        })
      },
    })
  },

  useCreateAccount: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleCreateAccountInput) =>
        authnAdminAccountsCreate(input).then((answer) => answer.account),
      onSuccess: () => {
        invalidateAccountLists(queryClient)
      },
    })
  },

  usePatchAccount: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsolePatchAccountInput) => {
        const { id, ...patch } = input
        return authnAdminAccountsPatch(id, patch).then(
          (answer) => answer.account,
        )
      },
      onSuccess: (_answer, input) => {
        invalidateAccountLists(queryClient)
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsGetQueryKey(input.id),
        })
      },
    })
  },

  useSetAccountStatus: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleSetAccountStatusInput) => {
        const { id, ...body } = input
        return authnAdminAccountsStatus(id, body).then(
          (answer) => answer.account,
        )
      },
      onSuccess: (_answer, input) => {
        invalidateAccountLists(queryClient)
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsGetQueryKey(input.id),
        })
      },
    })
  },

  useSetMFARequired: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleSetMFARequiredInput) => {
        const { id, ...body } = input
        return authnAdminAccountsSetMFARequired(id, body)
      },
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsGetQueryKey(input.id),
        })
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsListQueryKey(),
        })
      },
    })
  },

  useResetPassword: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleResetPasswordInput) => {
        const { id, ...body } = input
        return authnAdminAccountsResetPassword(id, body)
      },
      onSuccess: (_answer, input) => {
        // The module revokes every session of the account on a reset.
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsSessionsQueryKey(input.id),
        })
      },
    })
  },

  useAddIdentifier: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleAddIdentifierInput) => {
        const { id, ...body } = input
        return authnAdminAccountsAddIdentifier(id, body)
      },
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsGetQueryKey(input.id),
        })
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsListQueryKey(),
        })
      },
    })
  },

  useCreateSecret: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleCreateSecretInput) => {
        const { id, ...body } = input
        return authnAdminAccountsSecretsCreate(id, body)
      },
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsSecretsListQueryKey(input.id),
        })
      },
    })
  },

  // --- Impersonation surface (/auth/admin) ------------------------------

  useImpersonations: (params) =>
    useQuery({
      ...getAuthnAdminImpersonationsListQueryOptions(params),
      select: (page) => page.impersonations,
      // The tenant is a required query parameter on the route, so an empty
      // one would fire a request the module refuses as a shape it cannot
      // read; the read stays disabled until the page names a tenant.
      enabled: params.tenant !== '',
    }),

  useRevokeImpersonation: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleAccountRef) =>
        authnAdminImpersonationsRevoke(input.id),
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminImpersonationsListQueryKey(),
        })
      },
    })
  },

  // --- Invitation surface (tenant-scoped) -------------------------------

  useInvitations: (tenant, params) =>
    useQuery({
      ...getAuthnInviteListQueryOptions(tenant, params),
      select: (page) => page,
      enabled: tenant !== '',
    }),

  useIssueInvitation: () => {
    const queryClient = useQueryClient()
    return useMutation({
      // The answer carries the acceptance token, which the page reads to
      // show the invitee's link; it is handed out only once, so it must not
      // be discarded here.
      mutationFn: (input: ConsoleIssueInvitationInput) =>
        authnInviteIssue(input.tenant, {
          email: input.email,
          ...(input.expiresAt === undefined
            ? {}
            : { expires_at: input.expiresAt }),
        }),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnInviteListQueryKey(input.tenant),
        })
      },
    })
  },

  useRevokeInvitation: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleInvitationRef) =>
        authnInviteRevoke(input.tenant, input.id),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnInviteListQueryKey(input.tenant),
        })
      },
    })
  },

  // --- Membership surface (tenant-scoped) -------------------------------

  useMembers: (tenant, params) =>
    useQuery({
      ...getAuthnAdminMembersListQueryOptions(tenant, params),
      select: (page) => page.members,
      enabled: tenant !== '',
    }),

  useRemoveMember: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleMemberRef) =>
        authnAdminMembersRemove(input.tenant, input.account),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminMembersListQueryKey(input.tenant),
        })
      },
    })
  },

  useWriteMember: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleWriteMemberInput) => {
        const { tenant, account, ...body } = input
        return authnAdminMembersPut(tenant, account, body)
      },
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminMembersListQueryKey(input.tenant),
        })
      },
    })
  },

  useSetMemberSsoExempt: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleSetMemberSSOExemptInput) => {
        const { tenant, account, ...body } = input
        return authnAdminMembersSsoExempt(tenant, account, body)
      },
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminMembersListQueryKey(input.tenant),
        })
      },
    })
  },

  // --- SSO surface (/auth/admin/sso) ------------------------------------

  useSsoProviders: (tenant) =>
    useQuery({
      ...getAuthnAdminSsoProvidersListQueryOptions(tenant),
      select: (page) => page.providers,
      enabled: tenant !== '',
    }),

  useSsoProvider: ({ id }) =>
    useQuery({
      ...getAuthnAdminSsoProvidersGetQueryOptions(id ?? ''),
      select: (envelope) => envelope.provider,
      enabled: id !== undefined && id !== '',
    }),

  useDeleteSsoProvider: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleSsoProviderRef) =>
        authnAdminSsoProvidersDelete(input.id),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminSsoProvidersListQueryKey(input.tenant),
        })
        queryClient.removeQueries({
          queryKey: getAuthnAdminSsoProvidersGetQueryKey(input.id),
        })
      },
    })
  },

  useRemoveSsoDomain: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleSsoDomainRef) =>
        authnAdminSsoProvidersRemoveDomain(input.id, input.domain),
      onSuccess: (_answer, input) => {
        invalidateSsoProvider(queryClient, input.id, input.tenant)
      },
    })
  },

  useVerifySsoDomain: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleSsoDomainRef) =>
        authnAdminSsoProvidersVerifyDomain(input.id, input.domain),
      onSuccess: (_answer, input) => {
        invalidateSsoProvider(queryClient, input.id, input.tenant)
      },
    })
  },

  useRefreshSsoMetadata: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleSsoProviderRef) =>
        authnAdminSsoProvidersRefreshMetadata(input.id).then(() => undefined),
      onSuccess: (_answer, input) => {
        invalidateSsoProvider(queryClient, input.id, input.tenant)
      },
    })
  },

  useReverifySsoDomains: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleSsoProviderRef) =>
        authnAdminSsoProvidersReverifyDomains(input.id),
      onSuccess: (_answer, input) => {
        invalidateSsoProvider(queryClient, input.id, input.tenant)
      },
    })
  },

  useCreateSsoProvider: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleCreateSsoProviderInput) => {
        const { tenant, ...body } = input
        return authnAdminSsoProvidersCreate(tenant, body).then(
          (answer) => answer.provider,
        )
      },
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminSsoProvidersListQueryKey(input.tenant),
        })
      },
    })
  },

  useUpdateSsoProvider: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleWriteSsoProviderInput) => {
        const { id, ...body } = input
        return authnAdminSsoProvidersUpdate(id, body).then(
          (answer) => answer.provider,
        )
      },
      onSuccess: (_answer, input) => {
        invalidateSsoProvider(queryClient, input.id, input.tenant)
      },
    })
  },

  useClaimSsoDomain: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleClaimSsoDomainInput) =>
        authnAdminSsoProvidersClaimDomain(input.id, {
          domain: input.domain,
        }),
      onSuccess: (_answer, input) => {
        invalidateSsoProvider(queryClient, input.id, input.tenant)
      },
    })
  },

  // --- SMS template surface (/sms/admin) --------------------------------

  useSmsTemplates: (params) =>
    useQuery({
      ...getSmsAdminTemplatesListQueryOptions(params),
      select: (page) => page,
    }),

  useRegisterSmsTemplate: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleRegisterSmsTemplateInput) =>
        smsAdminTemplatesRegister({
          name: input.name,
          params: [...input.params],
          codes: { ...input.codes },
        }).then(() => undefined),
      onSuccess: () => {
        invalidateSmsTemplates(queryClient)
      },
    })
  },

  useOfferSmsTemplate: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleOfferSmsTemplateInput) =>
        smsAdminTemplatesOffer({
          name: input.name,
          content: input.content,
          kind: input.kind,
          locale: input.locale,
          params: [...input.params],
          remark: input.remark,
        }).then(() => undefined),
      onSuccess: () => {
        invalidateSmsTemplates(queryClient)
      },
    })
  },

  useSetSmsTemplateEnabled: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleSetSmsTemplateEnabledInput) =>
        smsAdminTemplatesSetEnabled(input.name, { enabled: input.enabled }),
      onSuccess: () => {
        invalidateSmsTemplates(queryClient)
      },
    })
  },

  useRemoveSmsTemplate: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: { readonly name: string }) =>
        smsAdminTemplatesRemove(input.name),
      onSuccess: () => {
        invalidateSmsTemplates(queryClient)
      },
    })
  },
}

/** Invalidates both binding readers a binding write leaves stale: the
 * tenant's whole list and the written account's own held bindings. */
function invalidateBindings(
  queryClient: ReturnType<typeof useQueryClient>,
  tenant: string,
  account: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: getAuthzAdminBindingsListQueryKey(tenant),
  })
  void queryClient.invalidateQueries({
    queryKey: getAuthzAdminBindingsOfQueryKey(tenant, account),
  })
}

/** Invalidates every parametrized page of the accounts list: the generated
 * key factory with no params is a prefix of every parametrized read. */
function invalidateAccountLists(
  queryClient: ReturnType<typeof useQueryClient>,
): void {
  void queryClient.invalidateQueries({
    queryKey: getAuthnAdminAccountsListQueryKey(),
  })
}

/** Invalidates a provider's detail read and the tenant's provider list: an
 * SSO write changes the provider's own row and its list entry together. */
function invalidateSsoProvider(
  queryClient: ReturnType<typeof useQueryClient>,
  id: string,
  tenant: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: getAuthnAdminSsoProvidersGetQueryKey(id),
  })
  void queryClient.invalidateQueries({
    queryKey: getAuthnAdminSsoProvidersListQueryKey(tenant),
  })
}

/** Invalidates every parametrized page of the SMS template list. */
function invalidateSmsTemplates(
  queryClient: ReturnType<typeof useQueryClient>,
): void {
  void queryClient.invalidateQueries({
    queryKey: getSmsAdminTemplatesListQueryKey(),
  })
}

/** Invalidates both settings reads a write can leave stale: the
 * all-namespaces list and the written item's own namespace list. The keys
 * come from the generated factories, so they cannot drift from the reads. */
function invalidateSetting(
  queryClient: ReturnType<typeof useQueryClient>,
  namespace: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: getSettingsAdminSettingsListQueryKey({ ...GLOBAL_LAYER }),
  })
  void queryClient.invalidateQueries({
    queryKey: getSettingsAdminSettingsListNamespaceQueryKey(namespace, {
      ...GLOBAL_LAYER,
    }),
  })
}

const ConsoleOperationsContext = createContext<ConsoleOperations | null>(null)

export interface ConsoleOperationsProviderProps {
  /** The operations bundle every console page under this provider reads through. */
  readonly operations: ConsoleOperations
  readonly children: ReactNode
}

/**
 * Overrides the shipped console operations for the pages below it. A host
 * mounts one only to change the wiring; with none mounted the pages read
 * the shipped consoleOperations. The value is expected to be stable across
 * renders (a module-level object or a memo), because the pages call its
 * hook members during render.
 */
export function ConsoleOperationsProvider({
  operations,
  children,
}: ConsoleOperationsProviderProps) {
  return (
    <ConsoleOperationsContext.Provider value={operations}>
      {children}
    </ConsoleOperationsContext.Provider>
  )
}

/**
 * The operations bundle the pages read through: the enclosing provider's
 * when one is mounted, the shipped consoleOperations otherwise.
 */
export function useConsoleOperations(): ConsoleOperations {
  const operations = useContext(ConsoleOperationsContext)
  return operations ?? consoleOperations
}
