/**
 * The console operations seam: the read hooks and write mutations the
 * console pages consume, shipped over the generated @speed/api-sdk
 * administration operations.
 *
 * The pages read and write through the operations bundle they get from
 * useAdminOperations -- a plain object whose members are the
 * react-query read hooks and write mutations of the administration
 * surfaces the pages render: the tenant roster, settings and audit
 * surfaces over /api/v1/system/tenants, /api/v1/system/settings and /api/v1/system/audit; the
 * account, session, impersonation, invitation and membership and SSO
 * surfaces over /api/v1/auth and /api/v1/system; and the SMS template surface over
 * /api/v1/system/sms. The bundle the package ships (adminOperations) is a thin
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
 * AdminOperationsProvider overrides the shipped bundle: a host that
 * wants different wiring, or a suite that scripts one, mounts its own and
 * useAdminOperations answers with it. With no provider mounted the pages
 * read the shipped adminOperations, so a host that composes the console
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
import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import type { UseMutationResult, UseQueryResult } from '@tanstack/react-query'
import {
  auditAdminRecordsExport,
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
  getAuthzAdminPermissionsListQueryKey,
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
  getTenantAdminTenantsPendingTransferQueryKey,
  getTenantAdminTenantsPendingTransferQueryOptions,
  settingsAdminSettingsRemove,
  settingsAdminSettingsSet,
  smsAdminTemplatesOffer,
  smsAdminTemplatesRegister,
  smsAdminTemplatesRemove,
  smsAdminTemplatesSetEnabled,
  tenantAdminTenantsCancelTransfer,
  tenantAdminTenantsCreate,
  tenantAdminTenantsDelete,
  tenantAdminTenantsProfile,
  tenantAdminTenantsRequestTransfer,
  tenantAdminTenantsSetStatus,
} from '@speed/api-sdk'
import type {
  AuditAdminRecord,
  AuditAdminRecordPage,
  AuditAdminRecordsExportParams,
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
  AuthnAdminMember,
  AuthnAdminMemberList,
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
  TenantAdminTenantsSetStatusBodyStatus,
  TenantTransferView,
} from '@speed/api-sdk'
import { useAdminUiTranslation } from './internal/translation.js'
import { profileWriteOf } from './TenantProfileFields.js'
import type { TenantProfileFields } from './TenantProfileFields.js'

/** The settings layer the console operates on: the global (platform)
 * layer, whose rows only platform staff may read and write. */
const GLOBAL_LAYER = { layer: 'global' } as const

/** One tenant as /api/v1/system/tenants shows it -- the generated row of
 * GET /api/v1/system/tenants, named for the console's own view. */
export type AdminTenant = TenantAdminTenant

/** The state a roster row reads as (the read side stays open -- an answer
 * outside the set renders the generic label rather than a raw value). The
 * generated TenantAdminTenantStatus enum: active, disabled or deleted. */
export type AdminTenantStatus = TenantAdminTenantStatus

/** The state a status write may carry: active or disabled. Deletion is
 * terminal and has no route on this surface, so the write body's enum is
 * narrower than the response's -- the generated set-status body enum, not
 * the response one. */
export type AdminSetTenantStatusValue = TenantAdminTenantsSetStatusBodyStatus

/** One setting as /api/v1/system/settings shows it -- the generated row of
 * GET /api/v1/system/settings. */
export type AdminSetting = SettingsAdminSetting

/** One audit record as /api/v1/system/audit shows it -- the generated row of
 * GET /api/v1/system/audit/records. */
export type AdminAuditRecord = AuditAdminRecord

/** One page of audit records: the rows and the next-page cursor. */
export type AdminAuditPage = AuditAdminRecordPage

/** An audit-record target for a read that stays disabled while no id is chosen. */
export interface AdminAuditRecordQuery {
  /** The record id, or undefined to read nothing. */
  readonly id: string | undefined
}

/** One page of accounts: the rows and the next-page cursor. */
export type AdminAccountPage = AuthnAdminAccountList

/** One page of invitations: the rows and the next-page cursor. */
export type AdminInvitationPage = AuthnInvitePage

/** One page of tenant members: the rows and the next-page cursor. */
export type AdminMemberPage = AuthnAdminMemberList

/** One page of SMS templates: the rows and the next-page cursor. */
export type AdminSmsTemplatePage = SmsAdminTemplatesList200

/** POST /api/v1/system/tenants body: a tenant not registered yet, with the
 * organization profile the registration form carries. The server mints the
 * internal identifier; the request names the tenant by its name only. The
 * four descriptive fields are omitted from the request when empty, because
 * the endpoint treats an absent field as "none" and rejects an empty
 * address. */
export interface AdminCreateTenantInput {
  readonly name: string
  readonly displayName: string
  readonly description: string
  readonly contactEmail: string
  readonly website: string
}

/** POST /api/v1/system/tenants/{tenant}/status body: a new state, one of the
 * writable set active/disabled. */
export interface AdminSetTenantStatusInput {
  readonly name: string
  readonly status: AdminSetTenantStatusValue
}

/** PATCH /api/v1/system/tenants/{tenant}/profile body: the four descriptive
 * fields to write, with the tenant they belong to. The name, state and
 * creation time are not writable. */
export interface AdminSetTenantProfileInput {
  readonly name: string
  readonly fields: TenantProfileFields
}

/** POST /api/v1/system/tenants/{tenant}/owner/requests body: the active
 * member platform staff ask to take ownership. Ownership does not change
 * until the target confirms. */
export interface AdminRequestTenantOwnerInput {
  readonly name: string
  readonly account: string
}

/** DELETE /api/v1/system/tenants/{tenant}/owner/requests/{request} target:
 * the pending request platform staff cancel. */
export interface AdminCancelTenantOwnerTransferInput {
  readonly name: string
  readonly request: string
}

/** One owner-transfer request as the platform surface reports it. */
export type AdminTenantTransfer = TenantTransferView

/** DELETE /api/v1/system/tenants/{tenant} target: the tenant whose tombstone
 * the write lands. */
export interface AdminDeleteTenantInput {
  readonly name: string
}

/** A tenant target for a read that stays disabled while no name is chosen. */
export interface AdminTenantQuery {
  /** The tenant name, or undefined to read nothing. */
  readonly name: string | undefined
}

/**
 * PUT /api/v1/system/settings/:namespace/:key body: the new value's text.
 * `namespace` is the module that declared the item (a module name may
 * contain a dot), and `key` is the rest of the key under it -- the full
 * key is `<namespace>.<key>` -- because that is how the route addresses
 * the item. AdminSetting carries the full key and the namespace; a
 * caller writing a row splits the full key with `settingKeyRest`.
 */
export interface AdminSetSettingInput {
  readonly namespace: string
  readonly key: string
  readonly value: string
}

/** DELETE /api/v1/system/settings/:namespace/:key target (see AdminSetSettingInput). */
export interface AdminRemoveSettingInput {
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

// --- Authorization surface (/api/v1/system) -------------------------------
// The authorization administration surface is tenant-scoped: the role,
// grant and binding routes all carry the tenant as a path segment, so
// every read and write here names the tenant it acts on (the tenant an
// operator picks on the page, not the session's current tenant).

/** One permission the catalog declares (GET /api/v1/system/permissions). */
export type AdminPermission = AuthzAdminPermissionView

/** One tenant role (GET /api/v1/system/tenants/:tenant/roles). */
export type AdminRole = AuthzAdminRoleView

/** One permission grant a role holds (GET .../roles/:role/grants). */
export type AdminGrant = AuthzAdminGrantView

/** One role binding an account holds in a tenant (GET .../bindings). */
export type AdminBinding = AuthzAdminBindingView

/**
 * PUT .../roles/:role body: a role's declared shape. The role name is the key
 * a binding holds and is never renamed, so a write names an existing role or
 * creates one; `description` is the operator's free text. There is no builtin
 * field: the mark is the host's to set when it seeds the role, and the server
 * refuses a role write that carries it.
 */
export interface AdminWriteRoleInput {
  readonly tenant: string
  readonly role: string
  readonly description: string
}

/** A tenant-and-role target (delete a role, list its grants). */
export interface AdminRoleRef {
  readonly tenant: string
  readonly role: string
}

/** A grant target: the role and the permission it grants or revokes. */
export interface AdminGrantRef {
  readonly tenant: string
  readonly role: string
  readonly permission: string
}

/** A binding target: the account and the role bound or unbound. */
export interface AdminBindingRef {
  readonly tenant: string
  readonly account: string
  readonly role: string
}

/** The account whose held bindings are read within a tenant. */
export interface AdminAccountBindingsQuery {
  readonly tenant: string
  /** The account to inspect; empty reads nothing (the read stays disabled). */
  readonly account: string | undefined
}

// --- Account surface (/api/v1/system/auth) --------------------------------------

/** One account as /api/v1/system/auth shows it (GET /api/v1/system/auth/accounts). */
export type AdminAccount = AuthnAdminAccount

/** One registered factor of an account (GET .../accounts/:id/factors). */
export type AdminAccountFactor = AuthnAdminFactor

/** One service secret of an account (GET .../accounts/:id/secrets). */
export type AdminAccountSecret = AuthnAdminSecret

/** The one-time handover a secret creation answers with (POST .../secrets). */
export type AdminAccountSecretCreated = AuthnAdminSecretCreated

/** One session of an account (GET .../accounts/:id/sessions). */
export type AdminAccountSession = AuthnAdminSession

/** One impersonation record (GET /api/v1/system/tenants/{tenant}/impersonations). */
export type AdminImpersonation = AuthnAdminImpersonation

/** An account target (its detail reads and its writes). */
export interface AdminAccountRef {
  readonly id: string
}

/** The impersonation record surface's page request: the tenant the records are
 * filed under, and the page's own paging inputs. */
export interface AdminImpersonationListParams {
  readonly tenant: string
  readonly limit?: number
  readonly after?: string
}

/** DELETE .../tenants/{tenant}/impersonations/:id target. */
export interface AdminImpersonationRef {
  readonly id: string
  readonly tenant: string
}

/** An account target for a read that stays disabled while no id is chosen. */
export interface AdminAccountQuery {
  /** The account id, or undefined to read nothing. */
  readonly id: string | undefined
}

/** DELETE .../accounts/:id/identifiers/:identifier target. */
export interface AdminRemoveIdentifierInput {
  readonly id: string
  readonly identifier: string
}

/** DELETE .../accounts/:id/secrets/:secret target. */
export interface AdminRevokeSecretInput {
  readonly id: string
  readonly secret: string
}

/**
 * POST /api/v1/system/auth/accounts body: the fields a create names. It is the
 * generated request shape itself, so a field the module's fragment adds
 * reaches the page through this type with no parallel model to keep in
 * step.
 */
export type AdminCreateAccountInput = AuthnAdminAccountCreate

/** PATCH /api/v1/system/auth/accounts/:id: the account id beside the fields to change. */
export type AdminPatchAccountInput = AuthnAdminAccountPatch & {
  readonly id: string
}

/** POST .../accounts/:id/status: the account id beside its new state. */
export type AdminSetAccountStatusInput = AuthnAdminAccountStatus & {
  readonly id: string
}

/** POST .../accounts/:id/mfa-required: the account id beside the required flag. */
export type AdminSetMFARequiredInput = AuthnAdminMFARequired & {
  readonly id: string
}

/** POST .../accounts/:id/password: the account id beside the temporary password. */
export type AdminResetPasswordInput = AuthnAdminPasswordReset & {
  readonly id: string
}

/** POST .../accounts/:id/identifiers: the account id beside the identifier to add. */
export type AdminAddIdentifierInput = AuthnAdminIdentifierWrite & {
  readonly id: string
}

/** POST .../accounts/:id/secrets: the account id beside the secret's label and expiry. */
export type AdminCreateSecretInput = AuthnAdminSecretCreate & {
  readonly id: string
}

// --- Invitation and membership surface (tenant-scoped) ------------------

/** One invitation of a tenant (GET .../tenants/:tenant/invitations). */
export type AdminInvitation = AuthnInviteInvitation

/** The one-time handover an invitation issue answers with (POST .../invitations):
 * the invitation view plus the acceptance token, which is handed out once. */
export type AdminIssuedInvitation = AuthnInviteIssued

/** One tenant member (GET .../tenants/:tenant/members). */
export type AdminMember = AuthnAdminMember

/** POST .../invitations body: the address to invite, optional expiry. */
export interface AdminIssueInvitationInput {
  readonly tenant: string
  readonly email: string
  /** RFC 3339 expiry, or undefined for the module's default. */
  readonly expiresAt: string | undefined
}

/** DELETE .../invitations/:id target. */
export interface AdminInvitationRef {
  readonly tenant: string
  readonly id: string
}

/** DELETE .../members/:account target. */
export interface AdminMemberRef {
  readonly tenant: string
  readonly account: string
}

/** PUT .../members/:account body: the membership to write, with its target. */
export type AdminWriteMemberInput = AuthnAdminMemberWrite & {
  readonly tenant: string
  readonly account: string
}

/** PUT .../members/:account/sso-exempt body: the exemption flag, with its target. */
export type AdminSetMemberSSOExemptInput = AuthnAdminMemberSSOExempt & {
  readonly tenant: string
  readonly account: string
}

// --- SSO surface (/api/v1/tenants/:tenant/sso) --------------------------------------

/** One SSO provider (GET .../tenants/:tenant/sso/providers, or .../providers/:id). */
export type AdminSsoProvider = AuthnAdminProvider

/** A provider target for a read that stays disabled while no id is chosen. */
export interface AdminSsoProviderQuery {
  /** The tenant the provider belongs to. */
  readonly tenant: string
  /** The provider id, or undefined to read nothing. */
  readonly id: string | undefined
}

/**
 * A provider write target. The provider's own routes address it by id,
 * but the list it belongs to is keyed by tenant, so a write carries the
 * tenant too and can invalidate that tenant's list.
 */
export interface AdminSsoProviderRef {
  readonly id: string
  readonly tenant: string
}

/** DELETE .../sso/providers/:id/domains/:domain target. */
export interface AdminSsoDomainRef {
  readonly id: string
  readonly tenant: string
  readonly domain: string
}

/** The token a domain claim answers with, to publish in DNS. */
export type AdminSsoVerifyToken = AuthnAdminVerifyToken

/** POST .../tenants/:tenant/sso/providers body: the provider to create. */
export type AdminCreateSsoProviderInput = AuthnAdminProviderWrite & {
  readonly tenant: string
}

/** PUT /api/v1/tenants/{tenant}/sso/providers/:id body: the provider to rewrite, with its tenant. */
export type AdminWriteSsoProviderInput = AuthnAdminProviderWrite & {
  readonly id: string
  readonly tenant: string
}

/** POST .../sso/providers/:id/domains body: the domain to claim, with its provider. */
export type AdminClaimSsoDomainInput = AuthnAdminDomainClaim & {
  readonly id: string
  readonly tenant: string
}

/** Revoking one session: the session's id beside the account it belongs to,
 * so the account's session list can be invalidated. */
export interface AdminRevokeSessionInput {
  readonly accountId: string
  readonly sessionId: string
}

// --- SMS template surface (/api/v1/system/sms) ----------------------------------

/** One SMS template (GET /api/v1/system/sms/templates). */
export type AdminSmsTemplate = SmsAdminTemplate

/** POST /api/v1/system/sms/templates body: take up a template by vendor code. */
export interface AdminRegisterSmsTemplateInput {
  readonly name: string
  readonly params: readonly string[]
  /** The vendor code per language. */
  readonly codes: Readonly<Record<string, string>>
}

/**
 * POST /api/v1/system/sms/templates/submissions body: hand a template to the
 * vendor for review (the code arrives on a later read).
 */
export interface AdminOfferSmsTemplateInput {
  readonly name: string
  readonly content: string
  readonly kind: string
  readonly locale: string
  readonly params: readonly string[]
  readonly remark: string
}

/** POST /api/v1/system/sms/templates/:name/enabled body: the send switch. */
export interface AdminSetSmsTemplateEnabledInput {
  readonly name: string
  readonly enabled: boolean
}

/**
 * The query-level behaviour a caller can ask of the audit-record read.
 * An absent member leaves the generated options and the query client's
 * own defaults in charge.
 */
export interface AdminAuditRecordsOptions {
  /**
   * Keep the previous answer as placeholder data while a read under a
   * different parameter set is in flight. The dashboard's platform-health
   * window moves its start forward -- and so changes the query key -- while
   * the numbers of the window it left stay rendered until the new window
   * settles.
   */
  readonly keepPrevious?: boolean
  /**
   * Whether the query client's automatic re-read when the tab becomes
   * visible applies. The dashboard advances its window start on that signal
   * and re-reads through the new key, so it turns the automatic re-read of
   * the old key off.
   */
  readonly refetchOnWindowFocus?: boolean
  /** The same for the connection coming back. */
  readonly refetchOnReconnect?: boolean
}

/**
 * The console's data operations, in react-query hook shape. Every read and
 * write member is a hook the pages call during render (on the host's
 * QueryClient); the audit export is the one action -- a page calls it on a
 * click, because mounting it as a query would fire an export on render.
 * adminOperations below binds them to the generated @speed/api-sdk
 * operations of the same shape, and a host or suite may bind its own.
 *
 * The members are grouped by administration surface. The reads whose
 * route carries a parameter (a tenant, a role, an account) take that
 * parameter so the query key namespaces by it; a read whose parameter is
 * undefined declares itself disabled rather than firing a malformed
 * request.
 */
export interface AdminOperations {
  /** GET /api/v1/system/tenants -- the whole roster, in the roster's order. */
  readonly useTenants: () => UseQueryResult<readonly AdminTenant[], unknown>
  /** GET /api/v1/system/tenants/{tenant} -- one tenant by its name
   * (disabled while no name). */
  readonly useTenant: (
    query: AdminTenantQuery,
  ) => UseQueryResult<AdminTenant, unknown>
  /** POST /api/v1/system/tenants -- register a tenant not registered yet. */
  readonly useCreateTenant: () => UseMutationResult<
    AdminTenant,
    unknown,
    AdminCreateTenantInput
  >
  /** POST /api/v1/system/tenants/{tenant}/status -- enable or disable a tenant. */
  readonly useSetTenantStatus: () => UseMutationResult<
    void,
    unknown,
    AdminSetTenantStatusInput
  >
  /** PATCH /api/v1/system/tenants/{tenant}/profile -- write a tenant's four
   * descriptive fields. */
  readonly useSetTenantProfile: () => UseMutationResult<
    void,
    unknown,
    AdminSetTenantProfileInput
  >
  /** GET /api/v1/system/tenants/{tenant}/owner/requests/pending -- the
   * organization's pending transfer request, or null. The read stays
   * disabled until a tenant name is chosen. */
  readonly usePendingTenantOwner: (
    name: string | undefined,
    enabled?: boolean,
  ) => UseQueryResult<AdminTenantTransfer | null, unknown>
  /** POST /api/v1/system/tenants/{tenant}/owner/requests -- ask one active
   * member to take ownership. The server refuses a target that is not an
   * active member of the organization (409); ownership changes only when
   * the target confirms. */
  readonly useRequestTenantOwner: () => UseMutationResult<
    AdminTenantTransfer,
    unknown,
    AdminRequestTenantOwnerInput
  >
  /** DELETE /api/v1/system/tenants/{tenant}/owner/requests/{request} --
   * cancel a pending request. Ownership is unchanged. */
  readonly useCancelTenantOwner: () => UseMutationResult<
    void,
    unknown,
    AdminCancelTenantOwnerTransferInput
  >
  /** DELETE /api/v1/system/tenants/{tenant} -- write the organization's
   * tombstone. Deletion is terminal; a tombstone is a 409. */
  readonly useDeleteTenant: () => UseMutationResult<
    void,
    unknown,
    AdminDeleteTenantInput
  >
  /** GET /api/v1/system/settings[/:namespace] -- the declared items' views. */
  readonly useSettings: (
    namespace: string | undefined,
  ) => UseQueryResult<readonly AdminSetting[], unknown>
  /** PUT /api/v1/system/settings/:namespace/:key -- write one item's value. */
  readonly useSetSetting: () => UseMutationResult<
    void,
    unknown,
    AdminSetSettingInput
  >
  /** DELETE /api/v1/system/settings/:namespace/:key -- remove one item's override. */
  readonly useRemoveSetting: () => UseMutationResult<
    void,
    unknown,
    AdminRemoveSettingInput
  >
  /**
   * GET /api/v1/system/audit/records -- one page of audit records, newest first,
   * narrowed by the filters the caller passes (since / until / action /
   * cursor / limit). The read returns the page envelope (the entries and
   * the next cursor), so the page can offer server-side pagination.
   */
  readonly useAuditRecords: (
    params?: AuditAdminRecordsListParams,
    options?: AdminAuditRecordsOptions,
  ) => UseQueryResult<AdminAuditPage, unknown>
  /** GET /api/v1/system/audit/records/:id -- one audit record (disabled while no id). */
  readonly useAuditRecord: (
    query: AdminAuditRecordQuery,
  ) => UseQueryResult<AdminAuditRecord, unknown>
  /**
   * GET /api/v1/system/audit/export -- every record the filters match as
   * one NDJSON Blob. This is an action, not a hook: the page calls it once
   * when the operator asks for a file, and it takes the filters only (no
   * cursor and no page size -- the export is not a page). Optional, so a
   * host bundle that predates the export keeps compiling; a page renders
   * no export control when the member is absent.
   */
  readonly exportAuditRecords?: (
    params?: AuditAdminRecordsExportParams,
  ) => Promise<Blob>

  // --- Authorization surface (/api/v1/system, tenant-scoped) --------------

  /** GET /api/v1/system/permissions -- the whole permission catalog. */
  readonly usePermissions: () => UseQueryResult<
    readonly AdminPermission[],
    unknown
  >
  /** GET /api/v1/system/tenants/:tenant/roles -- a tenant's roles. */
  readonly useRoles: (
    tenant: string,
  ) => UseQueryResult<readonly AdminRole[], unknown>
  /** PUT /api/v1/system/tenants/:tenant/roles/:role -- create or rewrite a role. */
  readonly useWriteRole: () => UseMutationResult<void, unknown, AdminWriteRoleInput>
  /** DELETE /api/v1/system/tenants/:tenant/roles/:role -- remove a role. */
  readonly useRemoveRole: () => UseMutationResult<void, unknown, AdminRoleRef>
  /** GET /api/v1/system/tenants/:tenant/roles/:role/grants -- a role's grants. */
  readonly useGrants: (
    tenant: string,
    role: string | undefined,
  ) => UseQueryResult<readonly AdminGrant[], unknown>
  /** PUT .../roles/:role/grants/:permission -- grant a permission to a role. */
  readonly useGrantPermission: () => UseMutationResult<void, unknown, AdminGrantRef>
  /** DELETE .../roles/:role/grants/:permission -- revoke a permission. */
  readonly useRevokePermission: () => UseMutationResult<void, unknown, AdminGrantRef>
  /** GET /api/v1/system/tenants/:tenant/bindings -- a tenant's bindings. */
  readonly useBindings: (
    tenant: string,
  ) => UseQueryResult<readonly AdminBinding[], unknown>
  /** GET .../bindings/:account -- the bindings an account holds. */
  readonly useAccountBindings: (
    query: AdminAccountBindingsQuery,
  ) => UseQueryResult<readonly AdminBinding[], unknown>
  /** PUT .../bindings/:account/:role -- bind a role to an account. */
  readonly useBindRole: () => UseMutationResult<void, unknown, AdminBindingRef>
  /** DELETE .../bindings/:account/:role -- unbind a role from an account. */
  readonly useUnbindRole: () => UseMutationResult<void, unknown, AdminBindingRef>

  // --- Account surface (/api/v1/system/auth) ------------------------------------

  /**
   * GET /api/v1/system/auth/accounts -- one page of accounts. `enabled` false
   * declares the read disabled (no request) when the params do not name an
   * applicable search.
   */
  readonly useAccounts: (
    params?: AuthnAdminAccountsListParams,
    options?: { readonly enabled?: boolean },
  ) => UseQueryResult<AdminAccountPage, unknown>
  /** GET /api/v1/system/auth/accounts/:id -- one account (disabled while no id). */
  readonly useAccount: (
    query: AdminAccountQuery,
  ) => UseQueryResult<AdminAccount, unknown>
  /** GET .../accounts/:id/factors -- an account's registered factors. */
  readonly useAccountFactors: (
    query: AdminAccountQuery,
  ) => UseQueryResult<readonly AdminAccountFactor[], unknown>
  /** GET .../accounts/:id/secrets -- an account's service secrets. */
  readonly useAccountSecrets: (
    query: AdminAccountQuery,
  ) => UseQueryResult<readonly AdminAccountSecret[], unknown>
  /** GET .../accounts/:id/sessions -- an account's sessions. */
  readonly useAccountSessions: (
    query: AdminAccountQuery,
  ) => UseQueryResult<readonly AdminAccountSession[], unknown>
  /** DELETE /api/v1/system/auth/accounts/:id -- delete an account. */
  readonly useDeleteAccount: () => UseMutationResult<void, unknown, AdminAccountRef>
  /** DELETE .../accounts/:id/identifiers/:identifier -- remove an identifier. */
  readonly useRemoveIdentifier: () => UseMutationResult<
    void,
    unknown,
    AdminRemoveIdentifierInput
  >
  /** POST .../accounts/:id/mfa-reset -- remove an account's factors. */
  readonly useResetMFA: () => UseMutationResult<void, unknown, AdminAccountRef>
  /** POST .../accounts/:id/revoke-sessions -- revoke every session. */
  readonly useRevokeAccountSessions: () => UseMutationResult<
    void,
    unknown,
    AdminAccountRef
  >
  /** DELETE .../accounts/:id/secrets/:secret -- revoke a service secret. */
  readonly useRevokeSecret: () => UseMutationResult<
    void,
    unknown,
    AdminRevokeSecretInput
  >
  /** DELETE /api/v1/system/auth/sessions/:id -- revoke one session by id. */
  readonly useRevokeSession: () => UseMutationResult<
    void,
    unknown,
    AdminRevokeSessionInput
  >
  /** POST /api/v1/system/auth/accounts -- create an account. */
  readonly useCreateAccount: () => UseMutationResult<
    AdminAccount,
    unknown,
    AdminCreateAccountInput
  >
  /** PATCH /api/v1/system/auth/accounts/:id -- change a display name, an attribute set or the protected flag. */
  readonly usePatchAccount: () => UseMutationResult<
    AdminAccount,
    unknown,
    AdminPatchAccountInput
  >
  /** POST .../accounts/:id/status -- enable or disable an account. */
  readonly useSetAccountStatus: () => UseMutationResult<
    AdminAccount,
    unknown,
    AdminSetAccountStatusInput
  >
  /** POST .../accounts/:id/mfa-required -- set whether a factor must be registered. */
  readonly useSetMFARequired: () => UseMutationResult<
    void,
    unknown,
    AdminSetMFARequiredInput
  >
  /** POST .../accounts/:id/password -- set a temporary password that must be changed at next login. */
  readonly useResetPassword: () => UseMutationResult<
    void,
    unknown,
    AdminResetPasswordInput
  >
  /** POST .../accounts/:id/identifiers -- add a login name or email an administrator asserts. */
  readonly useAddIdentifier: () => UseMutationResult<
    void,
    unknown,
    AdminAddIdentifierInput
  >
  /** POST .../accounts/:id/secrets -- mint a service secret, handed over once. */
  readonly useCreateSecret: () => UseMutationResult<
    AdminAccountSecretCreated,
    unknown,
    AdminCreateSecretInput
  >

  // --- Impersonation surface (/api/v1/system/auth) ------------------------------

  /**
   * GET /api/v1/system/tenants/{tenant}/impersonations -- one page of impersonation records.
   * The read is tenant-scoped by a required query parameter, so it stays
   * disabled while the caller names no tenant; `enabled` lets a page hold
   * it further, for a session whose impersonation the gateway would refuse
   * the read of.
   */
  readonly useImpersonations: (
    params: AdminImpersonationListParams,
    options?: { readonly enabled?: boolean },
  ) => UseQueryResult<readonly AdminImpersonation[], unknown>
  /** DELETE /api/v1/system/tenants/{tenant}/impersonations/:id -- terminate an impersonation. */
  readonly useRevokeImpersonation: () => UseMutationResult<
    void,
    unknown,
    AdminImpersonationRef
  >

  // --- Invitation surface (tenant-scoped) -------------------------------

  /** GET .../tenants/:tenant/invitations -- a tenant's invitations. */
  readonly useInvitations: (
    tenant: string,
    params?: AuthnInviteListParams,
  ) => UseQueryResult<AdminInvitationPage, unknown>
  /** POST .../tenants/:tenant/invitations -- invite an address. The answer
   * carries the acceptance token, handed out once to give to the invitee. */
  readonly useIssueInvitation: () => UseMutationResult<
    AdminIssuedInvitation,
    unknown,
    AdminIssueInvitationInput
  >
  /** DELETE .../tenants/:tenant/invitations/:id -- revoke an invitation. */
  readonly useRevokeInvitation: () => UseMutationResult<
    void,
    unknown,
    AdminInvitationRef
  >

  // --- Membership surface (tenant-scoped) -------------------------------

  /** GET .../tenants/:tenant/members -- one page of a tenant's members,
   * with the cursor of the page that follows. */
  readonly useMembers: (
    tenant: string,
    params?: AuthnAdminMembersListParams,
  ) => UseQueryResult<AdminMemberPage, unknown>
  /** DELETE .../tenants/:tenant/members/:account -- remove a membership. */
  readonly useRemoveMember: () => UseMutationResult<void, unknown, AdminMemberRef>
  /** PUT .../tenants/:tenant/members/:account -- add, suspend or resume a membership. */
  readonly useWriteMember: () => UseMutationResult<
    void,
    unknown,
    AdminWriteMemberInput
  >
  /** PUT .../members/:account/sso-exempt -- set or clear the SSO exemption. */
  readonly useSetMemberSsoExempt: () => UseMutationResult<
    void,
    unknown,
    AdminSetMemberSSOExemptInput
  >

  // --- SSO surface (/api/v1/tenants/:tenant/sso) ------------------------------------

  /** GET .../tenants/:tenant/sso/providers -- a tenant's providers. */
  readonly useSsoProviders: (
    tenant: string,
  ) => UseQueryResult<readonly AdminSsoProvider[], unknown>
  /** GET /api/v1/tenants/{tenant}/sso/providers/:id -- one provider (disabled while no id). */
  readonly useSsoProvider: (
    query: AdminSsoProviderQuery,
  ) => UseQueryResult<AdminSsoProvider, unknown>
  /** DELETE /api/v1/tenants/{tenant}/sso/providers/:id -- delete a provider. */
  readonly useDeleteSsoProvider: () => UseMutationResult<
    void,
    unknown,
    AdminSsoProviderRef
  >
  /** DELETE .../providers/:id/domains/:domain -- remove a claimed domain. */
  readonly useRemoveSsoDomain: () => UseMutationResult<
    void,
    unknown,
    AdminSsoDomainRef
  >
  /** POST .../providers/:id/domains/:domain/verify -- verify a claimed domain. */
  readonly useVerifySsoDomain: () => UseMutationResult<
    void,
    unknown,
    AdminSsoDomainRef
  >
  /** POST .../providers/:id/metadata/refresh -- refresh provider metadata. */
  readonly useRefreshSsoMetadata: () => UseMutationResult<
    void,
    unknown,
    AdminSsoProviderRef
  >
  /** POST .../providers/:id/reverify-domains -- re-verify every domain. */
  readonly useReverifySsoDomains: () => UseMutationResult<
    void,
    unknown,
    AdminSsoProviderRef
  >
  /** POST .../tenants/:tenant/sso/providers -- create a provider in a tenant. */
  readonly useCreateSsoProvider: () => UseMutationResult<
    AdminSsoProvider,
    unknown,
    AdminCreateSsoProviderInput
  >
  /** PUT /api/v1/tenants/{tenant}/sso/providers/:id -- rewrite a provider. */
  readonly useUpdateSsoProvider: () => UseMutationResult<
    AdminSsoProvider,
    unknown,
    AdminWriteSsoProviderInput
  >
  /** POST .../providers/:id/domains -- claim a domain, returning the token to publish. */
  readonly useClaimSsoDomain: () => UseMutationResult<
    AdminSsoVerifyToken,
    unknown,
    AdminClaimSsoDomainInput
  >

  // --- SMS template surface (/api/v1/system/sms) --------------------------------

  /** GET /api/v1/system/sms/templates -- one page of templates. */
  readonly useSmsTemplates: (
    params?: SmsAdminTemplatesListParams,
  ) => UseQueryResult<AdminSmsTemplatePage, unknown>
  /** POST /api/v1/system/sms/templates -- take up a template by vendor code. */
  readonly useRegisterSmsTemplate: () => UseMutationResult<
    void,
    unknown,
    AdminRegisterSmsTemplateInput
  >
  /** POST /api/v1/system/sms/templates/submissions -- offer a template for review. */
  readonly useOfferSmsTemplate: () => UseMutationResult<
    void,
    unknown,
    AdminOfferSmsTemplateInput
  >
  /** POST /api/v1/system/sms/templates/:name/enabled -- turn sending on or off. */
  readonly useSetSmsTemplateEnabled: () => UseMutationResult<
    void,
    unknown,
    AdminSetSmsTemplateEnabledInput
  >
  /** DELETE /api/v1/system/sms/templates/:name -- drop a runtime template. */
  readonly useRemoveSmsTemplate: () => UseMutationResult<
    void,
    unknown,
    { readonly name: string }
  >
}

/**
 * The shipped operations bundle: every member is the generated
 * @speed/api-sdk administration operation of the same shape, adapted to
 * the view shapes above. `useAdminOperations` answers with it whenever
 * no AdminOperationsProvider overrides it, so the console pages run on
 * the generated surface with no host-side wiring.
 */
export const adminOperations: AdminOperations = {
  useTenants: () =>
    useQuery({
      ...getTenantAdminTenantsListQueryOptions(),
      select: (page) => page.tenants,
    }),

  useTenant: ({ name }) =>
    useQuery({
      ...getTenantAdminTenantsGetQueryOptions(name ?? ''),
      select: (envelope) => envelope.tenant,
      enabled: name !== undefined && name !== '',
    }),

  useCreateTenant: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminCreateTenantInput) =>
        tenantAdminTenantsCreate({
          name: input.name,
          ...(input.displayName === ''
            ? {}
            : { display_name: input.displayName }),
          ...(input.description === ''
            ? {}
            : { description: input.description }),
          ...(input.contactEmail === ''
            ? {}
            : { contact_email: input.contactEmail }),
          ...(input.website === '' ? {} : { website: input.website }),
        }).then((answer) => answer.tenant),
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsListQueryKey(),
        })
      },
    })
  },

  useSetTenantStatus: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminSetTenantStatusInput) =>
        tenantAdminTenantsSetStatus(input.name, { status: input.status }),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsListQueryKey(),
        })
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsGetQueryKey(input.name),
        })
      },
    })
  },

  useSetTenantProfile: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminSetTenantProfileInput) =>
        tenantAdminTenantsProfile(
          input.name,
          profileWriteOf(input.fields),
        ).then(() => undefined),
      onSuccess: (_answer, input) => {
        // The roster row carries the four fields, so both the list and the
        // detail read are stale after a profile write.
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsListQueryKey(),
        })
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsGetQueryKey(input.name),
        })
      },
    })
  },

  usePendingTenantOwner: (name, enabled = true) => {
    const options = getTenantAdminTenantsPendingTransferQueryOptions(name ?? '')
    return useQuery({
      ...options,
      queryKey: getTenantAdminTenantsPendingTransferQueryKey(name ?? ''),
      select: (envelope) => envelope.request,
      enabled: name !== undefined && name !== '' && enabled,
    })
  },

  useRequestTenantOwner: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminRequestTenantOwnerInput) =>
        tenantAdminTenantsRequestTransfer(input.name, {
          account: input.account,
        }).then((answer) => answer.request),
      onSuccess: (_answer, input) => {
        // The request is recorded but the owner column has not moved. The
        // pending read now carries it; the roster and detail reads stay as
        // they are until the target confirms.
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsPendingTransferQueryKey(input.name),
        })
      },
    })
  },

  useCancelTenantOwner: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminCancelTenantOwnerTransferInput) =>
        tenantAdminTenantsCancelTransfer(input.name, input.request),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsPendingTransferQueryKey(input.name),
        })
      },
    })
  },

  useDeleteTenant: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminDeleteTenantInput) =>
        tenantAdminTenantsDelete(input.name),
      onSuccess: (_answer, input) => {
        // The row is gone from the roster; the detail read still answers the
        // tombstone, so both are re-read rather than dropped.
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsListQueryKey(),
        })
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsGetQueryKey(input.name),
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
      mutationFn: (input: AdminSetSettingInput) =>
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
      mutationFn: (input: AdminRemoveSettingInput) =>
        settingsAdminSettingsRemove(input.namespace, input.key, {
          ...GLOBAL_LAYER,
        }),
      onSuccess: (_answer, input) => {
        invalidateSetting(queryClient, input.namespace)
      },
    })
  },

  useAuditRecords: (params, options) =>
    useQuery({
      ...getAuditAdminRecordsListQueryOptions(params),
      placeholderData:
        options?.keepPrevious === true ? keepPreviousData : undefined,
      refetchOnWindowFocus: options?.refetchOnWindowFocus,
      refetchOnReconnect: options?.refetchOnReconnect,
    }),

  useAuditRecord: ({ id }) =>
    useQuery({
      ...getAuditAdminRecordsGetQueryOptions(id ?? ''),
      enabled: id !== undefined && id !== '',
    }),

  exportAuditRecords: (params) => auditAdminRecordsExport(params),

  // --- Authorization surface (/api/v1/system, tenant-scoped) --------------

  usePermissions: () => {
    // The catalog's descriptions are rendered by the server in the request's
    // language, so the language is part of what the answer is: keying the
    // read on it makes a language switch re-read the catalog instead of
    // leaving the previous language's text cached under the same key.
    const { i18n } = useAdminUiTranslation()
    return useQuery({
      ...getAuthzAdminPermissionsListQueryOptions({
        query: {
          queryKey: [
            ...getAuthzAdminPermissionsListQueryKey(),
            i18n.language,
          ],
        },
      }),
      select: (page) => page.permissions,
    })
  },

  useRoles: (tenant) =>
    useQuery({
      ...getAuthzAdminRolesListQueryOptions(tenant),
      select: (page) => page.roles,
      enabled: tenant !== '',
    }),

  useWriteRole: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminWriteRoleInput) =>
        authzAdminRolesPut(input.tenant, input.role, {
          description: input.description,
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
      mutationFn: (input: AdminRoleRef) =>
        authzAdminRolesDelete(input.tenant, input.role),
      onSuccess: (_answer, input) => {
        // Removing a role revokes its grants and bindings with it (the
        // generated route deletes all three), so the role list, the
        // tenant's binding list and the role's grants are stale. The
        // removal names no account, so every account-binding read under
        // the tenant is stale too: the page may be querying any account.
        void queryClient.invalidateQueries({
          queryKey: getAuthzAdminRolesListQueryKey(input.tenant),
        })
        void queryClient.invalidateQueries({
          queryKey: getAuthzAdminBindingsListQueryKey(input.tenant),
        })
        void queryClient.invalidateQueries({
          queryKey: getAuthzAdminGrantsListQueryKey(input.tenant, input.role),
        })
        invalidateTenantAccountBindings(queryClient, input.tenant)
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
      mutationFn: (input: AdminGrantRef) =>
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
      mutationFn: (input: AdminGrantRef) =>
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
      mutationFn: (input: AdminBindingRef) =>
        authzAdminBindingsPut(input.tenant, input.account, input.role),
      onSuccess: (_answer, input) => {
        invalidateBindings(queryClient, input.tenant, input.account)
      },
    })
  },

  useUnbindRole: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminBindingRef) =>
        authzAdminBindingsDelete(input.tenant, input.account, input.role),
      onSuccess: (_answer, input) => {
        invalidateBindings(queryClient, input.tenant, input.account)
      },
    })
  },

  // --- Account surface (/api/v1/system/auth) ------------------------------------

  useAccounts: (params, options) =>
    useQuery({
      ...getAuthnAdminAccountsListQueryOptions(params),
      select: (page) => page,
      enabled: options?.enabled ?? true,
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
      mutationFn: (input: AdminAccountRef) =>
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
      mutationFn: (input: AdminRemoveIdentifierInput) =>
        authnAdminAccountsRemoveIdentifier(input.id, input.identifier),
      onSuccess: (_answer, input) => {
        // The identifier is not a column of the account rows, but it is
        // what a login-identifier search matches on: removing one can drop
        // the account out of the page the list is showing, symmetric with
        // adding one.
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsGetQueryKey(input.id),
        })
        invalidateAccountLists(queryClient)
      },
    })
  },

  useResetMFA: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminAccountRef) =>
        authnAdminAccountsResetMFA(input.id).then(() => undefined),
      onSuccess: (_answer, input) => {
        // A reset drops every factor and revokes the account's sessions
        // with them, so the factor and session reads are both stale.
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsFactorsQueryKey(input.id),
        })
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsGetQueryKey(input.id),
        })
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminAccountsSessionsQueryKey(input.id),
        })
      },
    })
  },

  useRevokeAccountSessions: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminAccountRef) =>
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
      mutationFn: (input: AdminRevokeSecretInput) =>
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
      mutationFn: (input: AdminRevokeSessionInput) =>
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
      mutationFn: (input: AdminCreateAccountInput) =>
        authnAdminAccountsCreate(input).then((answer) => answer.account),
      onSuccess: () => {
        invalidateAccountLists(queryClient)
      },
    })
  },

  usePatchAccount: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminPatchAccountInput) => {
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
      mutationFn: (input: AdminSetAccountStatusInput) => {
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
        if (input.status === 'disabled') {
          // Disabling an account clears its browser and token sessions, so
          // the account's session read no longer matches the server.
          // Enabling it revives no rows, so it leaves that read alone.
          void queryClient.invalidateQueries({
            queryKey: getAuthnAdminAccountsSessionsQueryKey(input.id),
          })
        }
      },
    })
  },

  useSetMFARequired: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminSetMFARequiredInput) => {
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
      mutationFn: (input: AdminResetPasswordInput) => {
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
      mutationFn: (input: AdminAddIdentifierInput) => {
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
      mutationFn: (input: AdminCreateSecretInput) => {
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

  // --- Impersonation surface (/api/v1/system/auth) ------------------------------

  useImpersonations: (params, options) =>
    useQuery({
      ...getAuthnAdminImpersonationsListQueryOptions(
        params.tenant,
        { limit: params.limit, after: params.after },
      ),
      select: (page) => page.impersonations,
      // The tenant is a required query parameter on the route, so an empty
      // one would fire a request the module refuses as a shape it cannot
      // read; the read stays disabled until the page names a tenant. A
      // page also holds it while its own session is impersonating, where
      // the gateway refuses the read.
      enabled: params.tenant !== '' && (options?.enabled ?? true),
    }),

  useRevokeImpersonation: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminImpersonationRef) =>
        authnAdminImpersonationsRevoke(input.tenant, input.id),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminImpersonationsListQueryKey(input.tenant),
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
      mutationFn: (input: AdminIssueInvitationInput) =>
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
      mutationFn: (input: AdminInvitationRef) =>
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
      // The page envelope is the read's answer: a caller needs the cursor the
      // server reported, not only the rows it happened to receive. A search
      // or a page change keeps the previous rows on screen while the
      // next answer is in flight, so the table does not collapse to a
      // skeleton on every keystroke and every page step.
      placeholderData: keepPreviousData,
      enabled: tenant !== '',
    }),

  useRemoveMember: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminMemberRef) =>
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
      mutationFn: (input: AdminWriteMemberInput) => {
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
      mutationFn: (input: AdminSetMemberSSOExemptInput) => {
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

  // --- SSO surface (/api/v1/tenants/:tenant/sso) ------------------------------------

  useSsoProviders: (tenant) =>
    useQuery({
      ...getAuthnAdminSsoProvidersListQueryOptions(tenant),
      select: (page) => page.providers,
      enabled: tenant !== '',
    }),

  useSsoProvider: ({ tenant, id }) =>
    useQuery({
      ...getAuthnAdminSsoProvidersGetQueryOptions(tenant, id ?? ''),
      select: (envelope) => envelope.provider,
      enabled: id !== undefined && id !== '',
    }),

  useDeleteSsoProvider: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminSsoProviderRef) =>
        authnAdminSsoProvidersDelete(input.tenant, input.id),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: getAuthnAdminSsoProvidersListQueryKey(input.tenant),
        })
        queryClient.removeQueries({
          queryKey: getAuthnAdminSsoProvidersGetQueryKey(input.tenant, input.id),
        })
      },
    })
  },

  useRemoveSsoDomain: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminSsoDomainRef) =>
        authnAdminSsoProvidersRemoveDomain(input.tenant, input.id, input.domain),
      onSuccess: (_answer, input) => {
        invalidateSsoProvider(queryClient, input.id, input.tenant)
      },
    })
  },

  useVerifySsoDomain: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminSsoDomainRef) =>
        authnAdminSsoProvidersVerifyDomain(input.tenant, input.id, input.domain),
      onSuccess: (_answer, input) => {
        invalidateSsoProvider(queryClient, input.id, input.tenant)
      },
    })
  },

  useRefreshSsoMetadata: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminSsoProviderRef) =>
        authnAdminSsoProvidersRefreshMetadata(input.tenant, input.id).then(() => undefined),
      onSuccess: (_answer, input) => {
        invalidateSsoProvider(queryClient, input.id, input.tenant)
      },
    })
  },

  useReverifySsoDomains: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminSsoProviderRef) =>
        authnAdminSsoProvidersReverifyDomains(input.tenant, input.id),
      onSuccess: (_answer, input) => {
        invalidateSsoProvider(queryClient, input.id, input.tenant)
      },
    })
  },

  useCreateSsoProvider: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminCreateSsoProviderInput) => {
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
      mutationFn: (input: AdminWriteSsoProviderInput) => {
        const { id, tenant, ...body } = input
        return authnAdminSsoProvidersUpdate(tenant, id, body).then(
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
      mutationFn: (input: AdminClaimSsoDomainInput) =>
        authnAdminSsoProvidersClaimDomain(input.tenant, input.id, {
          domain: input.domain,
        }),
      onSuccess: (_answer, input) => {
        invalidateSsoProvider(queryClient, input.id, input.tenant)
      },
    })
  },

  // --- SMS template surface (/api/v1/system/sms) --------------------------------

  useSmsTemplates: (params) =>
    useQuery({
      ...getSmsAdminTemplatesListQueryOptions(params),
      select: (page) => page,
    }),

  useRegisterSmsTemplate: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminRegisterSmsTemplateInput) =>
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
      mutationFn: (input: AdminOfferSmsTemplateInput) =>
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
      mutationFn: (input: AdminSetSmsTemplateEnabledInput) =>
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

/** Invalidates every account-binding read under a tenant. A role removal
 * revokes bindings without naming the account a page queries, so the reads
 * are matched by the tenant's generated path rather than by account. */
function invalidateTenantAccountBindings(
  queryClient: ReturnType<typeof useQueryClient>,
  tenant: string,
): void {
  const accountBindings = getAuthzAdminBindingsOfQueryKey(tenant, '')[0]
  void queryClient.invalidateQueries({
    predicate: (query) =>
      typeof query.queryKey[0] === 'string' &&
      query.queryKey[0].startsWith(accountBindings),
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
    queryKey: getAuthnAdminSsoProvidersGetQueryKey(tenant, id),
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

const AdminOperationsContext = createContext<AdminOperations | null>(null)

export interface AdminOperationsProviderProps {
  /** The operations bundle every console page under this provider reads through. */
  readonly operations: AdminOperations
  readonly children: ReactNode
}

/**
 * Overrides the shipped console operations for the pages below it. A host
 * mounts one only to change the wiring; with none mounted the pages read
 * the shipped adminOperations. The value is expected to be stable across
 * renders (a module-level object or a memo), because the pages call its
 * hook members during render.
 */
export function AdminOperationsProvider({
  operations,
  children,
}: AdminOperationsProviderProps) {
  return (
    <AdminOperationsContext.Provider value={operations}>
      {children}
    </AdminOperationsContext.Provider>
  )
}

/**
 * The operations bundle the pages read through: the enclosing provider's
 * when one is mounted, the shipped adminOperations otherwise.
 */
export function useAdminOperations(): AdminOperations {
  const operations = useContext(AdminOperationsContext)
  return operations ?? adminOperations
}
