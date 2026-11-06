/**
 * A scripted console operations bundle for the admin-ui suites.
 *
 * The console pages read and write through the AdminOperations bundle
 * the host mounts (see src/operations.tsx). In production the host binds
 * the generated @speed/api-sdk operations; here the suite binds real
 * react-query hooks over a scripted backend, so the pages exercise
 * genuine react-query states -- the first-load pending branch, a settled
 * empty answer, a failed read, and a write whose success invalidates the
 * list it belongs to -- without an HTTP transport.
 *
 * The query keys live here, in test code, because the production keys
 * live inside the generated hooks; the scripted backend is the seam's
 * only stand-in. The impersonation record read is the exception: it builds
 * the generated factory's key, because the page drops that entry from the
 * cache and must name the key production's read builds. A backend
 * function a test scripts to reject drives the corresponding failure
 * branch.
 *
 * The backend is grouped by administration surface, mirroring the bundle:
 * the tenant roster and settings (platform-side, tenant-free), the audit
 * query, the tenant-scoped authorization surface, the account, session
 * and impersonation surfaces, the tenant-scoped invitation and membership
 * surfaces, the SSO surface and the SMS template surface. Every default
 * settles successfully (empty reads, resolved writes); a test overrides
 * the member it exercises.
 */

import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { getAuthnAdminImpersonationsListQueryKey } from '@speed/api-sdk'
import type { AuditAdminRecordsExportParams } from '@speed/api-sdk'
import type {
  AdminAccount,
  AdminAccountBindingsQuery,
  AdminAccountFactor,
  AdminAccountPage,
  AdminAccountQuery,
  AdminAccountRef,
  AdminImpersonationRef,
  AdminAccountSecret,
  AdminAccountSecretCreated,
  AdminAccountSession,
  AdminAddIdentifierInput,
  AdminAuditPage,
  AdminAuditRecord,
  AdminAuditRecordQuery,
  AdminBinding,
  AdminBindingRef,
  AdminClaimSsoDomainInput,
  AdminCreateAccountInput,
  AdminCreateSecretInput,
  AdminCreateSsoProviderInput,
  AdminCreateTenantInput,
  AdminGrant,
  AdminPatchAccountInput,
  AdminResetPasswordInput,
  AdminSetAccountStatusInput,
  AdminSetMFARequiredInput,
  AdminSetMemberSSOExemptInput,
  AdminSsoVerifyToken,
  AdminWriteMemberInput,
  AdminWriteSsoProviderInput,
  AdminGrantRef,
  AdminImpersonation,
  AdminInvitationPage,
  AdminInvitationRef,
  AdminIssueInvitationInput,
  AdminIssuedInvitation,
  AdminMember,
  AdminMemberPage,
  AdminMemberRef,
  AdminOfferSmsTemplateInput,
  AdminOperations,
  AdminPermission,
  AdminRegisterSmsTemplateInput,
  AdminRemoveIdentifierInput,
  AdminRemoveSettingInput,
  AdminRevokeSecretInput,
  AdminRevokeSessionInput,
  AdminRole,
  AdminRoleRef,
  AdminSetSettingInput,
  AdminSetSmsTemplateEnabledInput,
  AdminSetTenantStatusInput,
  AdminSetting,
  AdminSmsTemplatePage,
  AdminSsoDomainRef,
  AdminSsoProvider,
  AdminSsoProviderRef,
  AdminSsoProviderQuery,
  AdminTenant,
  AdminTenantQuery,
  AdminWriteRoleInput,
} from '../src/operations.js'
import type {
  AdminTenantBinding,
  AdminTenantBindingRef,
  AdminTenantGrant,
  AdminTenantGrantRef,
  AdminTenantPermission,
  AdminTenantRemoveSettingInput,
  AdminTenantRole,
  AdminTenantRoleBindingPage,
  AdminTenantRoleBindingsQuery,
  AdminTenantRoleRef,
  AdminTenantSetSettingInput,
  AdminTenantSettings,
  AdminTenantWriteRoleInput,
  OrganizationAdminOperations,
} from '../src/organization-operations.js'
import { tenantQueryKey } from '@speed/auth-core'

/** The scripted backend the operations bundle calls, grouped by surface. */
export interface AdminBackend {
  // Tenant roster + settings + audit (the platform-side three).
  listTenants: () => Promise<readonly AdminTenant[]>
  getTenant: (query: AdminTenantQuery) => Promise<AdminTenant>
  createTenant: (input: AdminCreateTenantInput) => Promise<AdminTenant>
  setTenantStatus: (input: AdminSetTenantStatusInput) => Promise<void>
  listSettings: (
    namespace: string | undefined,
  ) => Promise<readonly AdminSetting[]>
  setSetting: (input: AdminSetSettingInput) => Promise<void>
  removeSetting: (input: AdminRemoveSettingInput) => Promise<void>
  listRecords: (
    params?: Parameters<AdminOperations['useAuditRecords']>[0],
  ) => Promise<AdminAuditPage>
  getRecord: (query: AdminAuditRecordQuery) => Promise<AdminAuditRecord>
  exportRecords: (params?: AuditAdminRecordsExportParams) => Promise<Blob>

  // Authorization surface (tenant-scoped).
  listPermissions: () => Promise<readonly AdminPermission[]>
  listRoles: (tenant: string) => Promise<readonly AdminRole[]>
  writeRole: (input: AdminWriteRoleInput) => Promise<void>
  removeRole: (input: AdminRoleRef) => Promise<void>
  listGrants: (tenant: string, role: string) => Promise<readonly AdminGrant[]>
  grantPermission: (input: AdminGrantRef) => Promise<void>
  revokePermission: (input: AdminGrantRef) => Promise<void>
  listBindings: (tenant: string) => Promise<readonly AdminBinding[]>
  listAccountBindings: (
    query: AdminAccountBindingsQuery,
  ) => Promise<readonly AdminBinding[]>
  bindRole: (input: AdminBindingRef) => Promise<void>
  unbindRole: (input: AdminBindingRef) => Promise<void>

  // Account surface.
  listAccounts: (
    params?: Parameters<AdminOperations['useAccounts']>[0],
  ) => Promise<AdminAccountPage>
  getAccount: (query: AdminAccountQuery) => Promise<AdminAccount>
  listAccountFactors: (query: AdminAccountQuery) => Promise<readonly AdminAccountFactor[]>
  listAccountSecrets: (query: AdminAccountQuery) => Promise<readonly AdminAccountSecret[]>
  listAccountSessions: (query: AdminAccountQuery) => Promise<readonly AdminAccountSession[]>
  deleteAccount: (input: AdminAccountRef) => Promise<void>
  removeIdentifier: (input: AdminRemoveIdentifierInput) => Promise<void>
  resetMFA: (input: AdminAccountRef) => Promise<void>
  revokeAccountSessions: (input: AdminAccountRef) => Promise<void>
  revokeSecret: (input: AdminRevokeSecretInput) => Promise<void>
  revokeSession: (input: AdminRevokeSessionInput) => Promise<void>
  createAccount: (input: AdminCreateAccountInput) => Promise<AdminAccount>
  patchAccount: (input: AdminPatchAccountInput) => Promise<AdminAccount>
  setAccountStatus: (input: AdminSetAccountStatusInput) => Promise<AdminAccount>
  setMFARequired: (input: AdminSetMFARequiredInput) => Promise<void>
  resetPassword: (input: AdminResetPasswordInput) => Promise<void>
  addIdentifier: (input: AdminAddIdentifierInput) => Promise<void>
  createSecret: (
    input: AdminCreateSecretInput,
  ) => Promise<AdminAccountSecretCreated>

  // Impersonation surface.
  listImpersonations: (
    params: Parameters<AdminOperations['useImpersonations']>[0],
  ) => Promise<readonly AdminImpersonation[]>
  revokeImpersonation: (input: AdminImpersonationRef) => Promise<void>

  // Invitation + membership surfaces (tenant-scoped). The invitation read
  // takes the same page parameters production's bundle forwards (limit,
  // after, status); a test that only cares about the bare list may omit the
  // second argument, and the page still sees a settled first page.
  listInvitations: (
    tenant: string,
    params?: Parameters<AdminOperations['useInvitations']>[1],
  ) => Promise<AdminInvitationPage>
  issueInvitation: (
    input: AdminIssueInvitationInput,
  ) => Promise<AdminIssuedInvitation>
  revokeInvitation: (input: AdminInvitationRef) => Promise<void>
  /**
   * A test may answer with the server's page envelope (to exercise the next
   * cursor) or with the bare rows this fixture used before paging existed;
   * the hook normalizes both to the envelope the production read returns.
   */
  listMembers: (
    tenant: string,
    params?: Parameters<AdminOperations['useMembers']>[1],
  ) => Promise<AdminMemberPage | readonly AdminMember[]>
  removeMember: (input: AdminMemberRef) => Promise<void>
  writeMember: (input: AdminWriteMemberInput) => Promise<void>
  setMemberSsoExempt: (input: AdminSetMemberSSOExemptInput) => Promise<void>

  // SSO surface.
  listSsoProviders: (tenant: string) => Promise<readonly AdminSsoProvider[]>
  getSsoProvider: (query: AdminSsoProviderQuery) => Promise<AdminSsoProvider>
  deleteSsoProvider: (input: AdminSsoProviderRef) => Promise<void>
  removeSsoDomain: (input: AdminSsoDomainRef) => Promise<void>
  verifySsoDomain: (input: AdminSsoDomainRef) => Promise<void>
  refreshSsoMetadata: (input: AdminSsoProviderRef) => Promise<void>
  reverifySsoDomains: (input: AdminSsoProviderRef) => Promise<void>
  createSsoProvider: (input: AdminCreateSsoProviderInput) => Promise<AdminSsoProvider>
  updateSsoProvider: (input: AdminWriteSsoProviderInput) => Promise<AdminSsoProvider>
  claimSsoDomain: (input: AdminClaimSsoDomainInput) => Promise<AdminSsoVerifyToken>

  // Tenant management surfaces (the organization bundle's own members).
  listTenantSettings: (tenant: string) => Promise<AdminTenantSettings>
  writeTenantSetting: (input: AdminTenantSetSettingInput) => Promise<void>
  removeTenantSetting: (input: AdminTenantRemoveSettingInput) => Promise<void>
  listTenantRoles: (tenant: string) => Promise<readonly AdminTenantRole[]>
  writeTenantRole: (input: AdminTenantWriteRoleInput) => Promise<void>
  removeTenantRole: (input: AdminTenantRoleRef) => Promise<void>
  listTenantPermissions: (
    tenant: string,
  ) => Promise<readonly AdminTenantPermission[]>
  listTenantGrants: (
    tenant: string,
    role: string,
  ) => Promise<readonly AdminTenantGrant[]>
  listTenantRoleBindings: (
    tenant: string,
    role: string,
    params?: AdminTenantRoleBindingsQuery,
  ) => Promise<AdminTenantRoleBindingPage>
  grantTenantPermission: (input: AdminTenantGrantRef) => Promise<void>
  revokeTenantPermission: (input: AdminTenantGrantRef) => Promise<void>
  listTenantBindings: (tenant: string) => Promise<readonly AdminTenantBinding[]>
  listTenantAccountBindings: (
    tenant: string,
    account: string,
  ) => Promise<readonly AdminTenantBinding[]>
  bindTenantRole: (input: AdminTenantBindingRef) => Promise<void>
  unbindTenantRole: (input: AdminTenantBindingRef) => Promise<void>

  // SMS template surface.
  listSmsTemplates: () => Promise<AdminSmsTemplatePage>
  registerSmsTemplate: (input: AdminRegisterSmsTemplateInput) => Promise<void>
  offerSmsTemplate: (input: AdminOfferSmsTemplateInput) => Promise<void>
  setSmsTemplateEnabled: (input: AdminSetSmsTemplateEnabledInput) => Promise<void>
  removeSmsTemplate: (name: string) => Promise<void>
}

/**
 * A working in-memory backend for the common case, with the named
 * behaviour overridden per test. Everything defaults to a settled
 * successful answer (empty reads and resolved writes); a test that wants
 * a failure passes a rejecting function for the member it exercises.
 */
export function makeAdminBackend(
  overrides: Partial<AdminBackend> = {},
): AdminBackend {
  const emptyPage = <T,>(): { entries: T[]; next: string } => ({
    entries: [],
    next: '',
  })
  return {
    listTenants: () => Promise.resolve([]),
    getTenant: () => Promise.resolve(tenantFixture()),
    createTenant: (input) =>
      Promise.resolve(
        tenantFixture({ id: input.id, alias: input.alias, status: 'active' }),
      ),
    setTenantStatus: () => Promise.resolve(),
    listSettings: () => Promise.resolve([]),
    setSetting: () => Promise.resolve(),
    removeSetting: () => Promise.resolve(),
    listRecords: () => Promise.resolve(emptyPage<AdminAuditRecord>()),
    getRecord: () => Promise.resolve(recordFixture()),
    exportRecords: () => Promise.resolve(new Blob([])),

    listPermissions: () => Promise.resolve([]),
    listRoles: () => Promise.resolve([]),
    writeRole: () => Promise.resolve(),
    removeRole: () => Promise.resolve(),
    listGrants: () => Promise.resolve([]),
    grantPermission: () => Promise.resolve(),
    revokePermission: () => Promise.resolve(),
    listBindings: () => Promise.resolve([]),
    listAccountBindings: () => Promise.resolve([]),
    bindRole: () => Promise.resolve(),
    unbindRole: () => Promise.resolve(),

    listAccounts: () => Promise.resolve({ accounts: [], next_cursor: '' }),
    getAccount: () => Promise.resolve(accountFixture()),
    listAccountFactors: () => Promise.resolve([]),
    listAccountSecrets: () => Promise.resolve([]),
    listAccountSessions: () => Promise.resolve([]),
    deleteAccount: () => Promise.resolve(),
    removeIdentifier: () => Promise.resolve(),
    resetMFA: () => Promise.resolve(),
    revokeAccountSessions: () => Promise.resolve(),
    revokeSecret: () => Promise.resolve(),
    revokeSession: () => Promise.resolve(),
    createAccount: () => Promise.resolve(accountFixture()),
    patchAccount: () => Promise.resolve(accountFixture()),
    setAccountStatus: () => Promise.resolve(accountFixture()),
    setMFARequired: () => Promise.resolve(),
    resetPassword: () => Promise.resolve(),
    addIdentifier: () => Promise.resolve(),
    createSecret: () =>
      Promise.resolve({
        id: 'secret-1',
        label: 'label',
        secret: 'one-time',
        expires_at: '0001-01-01T00:00:00Z',
      }),

    listImpersonations: () => Promise.resolve([]),
    revokeImpersonation: () => Promise.resolve(),

    listInvitations: () => Promise.resolve({ invitations: [], next_cursor: '' }),
    issueInvitation: () => Promise.resolve(issuedInvitationFixture()),
    revokeInvitation: () => Promise.resolve(),
    listMembers: () => Promise.resolve({ members: [], next_cursor: '' }),
    removeMember: () => Promise.resolve(),
    writeMember: () => Promise.resolve(),
    setMemberSsoExempt: () => Promise.resolve(),

    listSsoProviders: () => Promise.resolve([]),
    getSsoProvider: () => Promise.resolve(providerFixture()),
    deleteSsoProvider: () => Promise.resolve(),
    removeSsoDomain: () => Promise.resolve(),
    verifySsoDomain: () => Promise.resolve(),
    refreshSsoMetadata: () => Promise.resolve(),
    reverifySsoDomains: () => Promise.resolve(),
    createSsoProvider: () => Promise.resolve(providerFixture()),
    updateSsoProvider: () => Promise.resolve(providerFixture()),
    claimSsoDomain: () => Promise.resolve({ verify_token: 'token' }),

    listTenantSettings: () =>
      Promise.resolve({
        settings: [],
        writable: true,
        read_only_reason: '',
      }),
    writeTenantSetting: () => Promise.resolve(),
    removeTenantSetting: () => Promise.resolve(),
    listTenantRoles: () => Promise.resolve([]),
    writeTenantRole: () => Promise.resolve(),
    removeTenantRole: () => Promise.resolve(),
    listTenantPermissions: () => Promise.resolve([]),
    listTenantGrants: () => Promise.resolve([]),
    listTenantRoleBindings: () =>
      Promise.resolve({ bindings: [], next_cursor: '' }),
    grantTenantPermission: () => Promise.resolve(),
    revokeTenantPermission: () => Promise.resolve(),
    listTenantBindings: () => Promise.resolve([]),
    listTenantAccountBindings: () => Promise.resolve([]),
    bindTenantRole: () => Promise.resolve(),
    unbindTenantRole: () => Promise.resolve(),

    listSmsTemplates: () => Promise.resolve({ templates: [], next_cursor: '' }),
    registerSmsTemplate: () => Promise.resolve(),
    offerSmsTemplate: () => Promise.resolve(),
    setSmsTemplateEnabled: () => Promise.resolve(),
    removeSmsTemplate: () => Promise.resolve(),
    ...overrides,
  }
}

/** A minimal account row for the default detail read. */
export function accountFixture(
  overrides: Partial<AdminAccount> = {},
): AdminAccount {
  return {
    id: 'acc-1',
    kind: 'user',
    status: 'active',
    display_name: 'Operator',
    mfa_required: false,
    protected: false,
    password_set: true,
    identifiers: [],
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

/** A minimal tenant row for the default detail read. */
export function tenantFixture(
  overrides: Partial<AdminTenant> = {},
): AdminTenant {
  return {
    id: 'acme',
    alias: 'Acme',
    ownerAccount: 'acc-1',
    status: 'active',
    contactEmail: '',
    createdAt: '2026-10-15T12:00:00Z',
    description: '',
    displayName: '',
    website: '',
    ...overrides,
  }
}

/** A minimal audit record for the default detail read. */
export function recordFixture(
  overrides: Partial<AdminAuditRecord> = {},
): AdminAuditRecord {
  return {
    id: 'rec-1',
    tenant: 'acme',
    occurred_at: '2026-10-15T12:00:00Z',
    actor: 'acc-1',
    source: 'console',
    action: 'tenant.read',
    object: 'acme',
    outcome: 'allowed',
    purpose: 'administration',
    detail: {},
    ...overrides,
  }
}

/** A minimal SSO provider row for the default detail read. */
export function providerFixture(
  overrides: Partial<AdminSsoProvider> = {},
): AdminSsoProvider {
  return {
    id: 'idp-1',
    tenant: 'acme',
    protocol: 'oidc',
    name: 'Acme IdP',
    enabled: true,
    provisioning: '',
    trust_idp_mfa: false,
    allow_idp_initiated: false,
    allow_sha1: false,
    merge_existing: false,
    version: 1,
    domains: [],
    ...overrides,
  }
}

/** The issued invitation an issue answers with: the pending row plus the
 * one-time token. */
export function issuedInvitationFixture(
  overrides: Partial<AdminIssuedInvitation> = {},
): AdminIssuedInvitation {
  return {
    invitation: {
      id: 'inv-1',
      tenant: 'acme',
      email: 'ada@example.com',
      status: 'pending',
      invited_by: 'staff-1',
      expires_at: '2099-01-01T00:00:00Z',
      accepted_at: '0001-01-01T00:00:00Z',
      created_at: '2026-10-15T12:00:00Z',
    },
    token: 'spd_inv_test_token',
    email_delivery: 'sent',
    ...overrides,
  }
}

const TENANTS_KEY = ['console-test', 'tenants'] as const
function tenantKey(id: string) {
  return ['console-test', 'tenant', id] as const
}
const AUDIT_KEY = ['console-test', 'audit'] as const
function auditRecordKey(id: string) {
  return ['console-test', 'audit-record', id] as const
}
function settingsKey(namespace: string | undefined) {
  return ['console-test', 'settings', namespace ?? null] as const
}
const PERMISSIONS_KEY = ['console-test', 'permissions'] as const
function rolesKey(tenant: string) {
  return ['console-test', 'roles', tenant] as const
}
function grantsKey(tenant: string, role: string) {
  return ['console-test', 'grants', tenant, role] as const
}
function bindingsKey(tenant: string) {
  return ['console-test', 'bindings', tenant] as const
}
function accountBindingsKey(tenant: string, account: string) {
  return ['console-test', 'bindings-of', tenant, account] as const
}
const ACCOUNTS_KEY = ['console-test', 'accounts'] as const
function accountKey(id: string) {
  return ['console-test', 'account', id] as const
}
function factorsKey(id: string) {
  return ['console-test', 'factors', id] as const
}
function secretsKey(id: string) {
  return ['console-test', 'secrets', id] as const
}
function sessionsKey(id: string) {
  return ['console-test', 'sessions', id] as const
}
function invitationsKey(tenant: string) {
  return ['console-test', 'invitations', tenant] as const
}
function membersKey(tenant: string) {
  return ['console-test', 'members', tenant] as const
}
function ssoProvidersKey(tenant: string) {
  return ['console-test', 'sso-providers', tenant] as const
}
function ssoProviderKey(id: string) {
  return ['console-test', 'sso-provider', id] as const
}
const SMS_TEMPLATES_KEY = ['console-test', 'sms-templates'] as const

/** The organization bundle's tenant-scoped keys: auth-core's tenant prefix
 * over the surface's own name, so a switch cannot reuse another tenant's
 * rows. */
function tenantSettingsKey(tenant: string) {
  return tenantQueryKey(tenant, 'console-test', 'tenant-settings')
}
function tenantRolesKey(tenant: string) {
  return tenantQueryKey(tenant, 'console-test', 'tenant-roles')
}
function tenantPermissionsKey(tenant: string) {
  return tenantQueryKey(tenant, 'console-test', 'tenant-permissions')
}
function tenantGrantsKey(tenant: string, role: string) {
  return tenantQueryKey(tenant, 'console-test', 'tenant-grants', role)
}
function tenantRoleBindingsKey(tenant: string, role: string) {
  return tenantQueryKey(tenant, 'console-test', 'tenant-role-bindings', role)
}
function tenantBindingsKey(tenant: string) {
  return tenantQueryKey(tenant, 'console-test', 'tenant-bindings')
}
function tenantAccountBindingsKey(tenant: string, account: string) {
  return tenantQueryKey(tenant, 'console-test', 'tenant-bindings', account)
}

/**
 * Builds a AdminOperations whose hooks run real react-query queries and
 * mutations over the given backend. Call it in the test body and pass the
 * result to renderWithProviders; keep the returned object stable across a
 * render (a module-level const or a value captured before render).
 *
 * The result also carries the organization bundle's tenant-scoped settings and
 * role members, so it is assignable to `OrganizationAdminOperations` and a
 * organization page test gets those surfaces from the same scripted backend.
 */
export function createAdminOperations(
  backend: AdminBackend,
): AdminOperations & OrganizationAdminOperations {
  return {
    useTenants: () =>
      useQuery({ queryKey: TENANTS_KEY, queryFn: () => backend.listTenants() }),
    useTenant: (query: AdminTenantQuery) =>
      useQuery({
        queryKey: tenantKey(query.id ?? ''),
        queryFn: () => backend.getTenant(query),
        enabled: query.id !== undefined && query.id !== '',
      }),
    useCreateTenant: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminCreateTenantInput) =>
          backend.createTenant(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: TENANTS_KEY })
        },
      })
    },
    useSetTenantStatus: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminSetTenantStatusInput) =>
          backend.setTenantStatus(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: TENANTS_KEY })
          void client.invalidateQueries({ queryKey: tenantKey(input.id) })
        },
      })
    },
    useSettings: (namespace: string | undefined) =>
      useQuery({
        queryKey: settingsKey(namespace),
        queryFn: () => backend.listSettings(namespace),
      }),
    useSetSetting: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminSetSettingInput) => backend.setSetting(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: ['console-test', 'settings'] })
        },
      })
    },
    useRemoveSetting: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminRemoveSettingInput) =>
          backend.removeSetting(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: ['console-test', 'settings'] })
        },
      })
    },
    useAuditRecords: (params, options) =>
      useQuery({
        queryKey: [...AUDIT_KEY, params ?? null],
        queryFn: () => backend.listRecords(params),
        placeholderData:
          options?.keepPrevious === true ? keepPreviousData : undefined,
        refetchOnWindowFocus: options?.refetchOnWindowFocus,
        refetchOnReconnect: options?.refetchOnReconnect,
      }),
    useAuditRecord: (query: AdminAuditRecordQuery) =>
      useQuery({
        queryKey: auditRecordKey(query.id ?? ''),
        queryFn: () => backend.getRecord(query),
        enabled: query.id !== undefined && query.id !== '',
      }),
    exportAuditRecords: (params) => backend.exportRecords(params),

    usePermissions: () =>
      useQuery({
        queryKey: PERMISSIONS_KEY,
        queryFn: () => backend.listPermissions(),
      }),
    useRoles: (tenant: string) =>
      useQuery({
        queryKey: rolesKey(tenant),
        queryFn: () => backend.listRoles(tenant),
        enabled: tenant !== '',
      }),
    useWriteRole: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminWriteRoleInput) => backend.writeRole(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: rolesKey(input.tenant) })
        },
      })
    },
    useRemoveRole: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminRoleRef) => backend.removeRole(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: rolesKey(input.tenant) })
          void client.invalidateQueries({ queryKey: bindingsKey(input.tenant) })
          void client.invalidateQueries({
            queryKey: grantsKey(input.tenant, input.role),
          })
        },
      })
    },
    useGrants: (tenant: string, role: string | undefined) =>
      useQuery({
        queryKey: grantsKey(tenant, role ?? ''),
        queryFn: () => backend.listGrants(tenant, role ?? ''),
        enabled: role !== undefined && role !== '',
      }),
    useGrantPermission: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminGrantRef) => backend.grantPermission(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({
            queryKey: grantsKey(input.tenant, input.role),
          })
        },
      })
    },
    useRevokePermission: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminGrantRef) => backend.revokePermission(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({
            queryKey: grantsKey(input.tenant, input.role),
          })
        },
      })
    },
    useBindings: (tenant: string) =>
      useQuery({
        queryKey: bindingsKey(tenant),
        queryFn: () => backend.listBindings(tenant),
        enabled: tenant !== '',
      }),
    useAccountBindings: (query: AdminAccountBindingsQuery) =>
      useQuery({
        queryKey: accountBindingsKey(query.tenant, query.account ?? ''),
        queryFn: () => backend.listAccountBindings(query),
        enabled: query.account !== undefined && query.account !== '',
      }),
    useBindRole: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminBindingRef) => backend.bindRole(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: bindingsKey(input.tenant) })
          void client.invalidateQueries({
            queryKey: accountBindingsKey(input.tenant, input.account),
          })
        },
      })
    },
    useUnbindRole: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminBindingRef) => backend.unbindRole(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: bindingsKey(input.tenant) })
          void client.invalidateQueries({
            queryKey: accountBindingsKey(input.tenant, input.account),
          })
        },
      })
    },

    useAccounts: (params, options) =>
      useQuery({
        queryKey: [...ACCOUNTS_KEY, params ?? null],
        queryFn: () => backend.listAccounts(params),
        enabled: options?.enabled ?? true,
      }),
    useAccount: (query: AdminAccountQuery) =>
      useQuery({
        queryKey: accountKey(query.id ?? ''),
        queryFn: () => backend.getAccount(query),
        enabled: query.id !== undefined && query.id !== '',
      }),
    useAccountFactors: (query: AdminAccountQuery) =>
      useQuery({
        queryKey: factorsKey(query.id ?? ''),
        queryFn: () => backend.listAccountFactors(query),
        enabled: query.id !== undefined && query.id !== '',
      }),
    useAccountSecrets: (query: AdminAccountQuery) =>
      useQuery({
        queryKey: secretsKey(query.id ?? ''),
        queryFn: () => backend.listAccountSecrets(query),
        enabled: query.id !== undefined && query.id !== '',
      }),
    useAccountSessions: (query: AdminAccountQuery) =>
      useQuery({
        queryKey: sessionsKey(query.id ?? ''),
        queryFn: () => backend.listAccountSessions(query),
        enabled: query.id !== undefined && query.id !== '',
      }),
    useDeleteAccount: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminAccountRef) => backend.deleteAccount(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: ACCOUNTS_KEY })
        },
      })
    },
    useRemoveIdentifier: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminRemoveIdentifierInput) =>
          backend.removeIdentifier(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: accountKey(input.id) })
        },
      })
    },
    useResetMFA: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminAccountRef) => backend.resetMFA(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: factorsKey(input.id) })
          void client.invalidateQueries({ queryKey: accountKey(input.id) })
        },
      })
    },
    useRevokeAccountSessions: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminAccountRef) =>
          backend.revokeAccountSessions(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: sessionsKey(input.id) })
        },
      })
    },
    useRevokeSecret: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminRevokeSecretInput) =>
          backend.revokeSecret(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: secretsKey(input.id) })
        },
      })
    },
    useRevokeSession: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminRevokeSessionInput) =>
          backend.revokeSession(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: sessionsKey(input.accountId) })
        },
      })
    },
    useCreateAccount: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminCreateAccountInput) =>
          backend.createAccount(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: ACCOUNTS_KEY })
        },
      })
    },
    usePatchAccount: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminPatchAccountInput) =>
          backend.patchAccount(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: ACCOUNTS_KEY })
          void client.invalidateQueries({ queryKey: accountKey(input.id) })
        },
      })
    },
    useSetAccountStatus: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminSetAccountStatusInput) =>
          backend.setAccountStatus(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: ACCOUNTS_KEY })
          void client.invalidateQueries({ queryKey: accountKey(input.id) })
        },
      })
    },
    useSetMFARequired: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminSetMFARequiredInput) =>
          backend.setMFARequired(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: accountKey(input.id) })
        },
      })
    },
    useResetPassword: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminResetPasswordInput) =>
          backend.resetPassword(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: sessionsKey(input.id) })
        },
      })
    },
    useAddIdentifier: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminAddIdentifierInput) =>
          backend.addIdentifier(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: accountKey(input.id) })
        },
      })
    },
    useCreateSecret: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminCreateSecretInput) =>
          backend.createSecret(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: secretsKey(input.id) })
        },
      })
    },

    useImpersonations: (params, options) =>
      useQuery({
        // The generated key factory, because the page acts on this read's
        // cache directly: the entry it drops on ending an impersonation
        // must be the one production's read builds.
        queryKey: getAuthnAdminImpersonationsListQueryKey(params.tenant, { limit: params.limit, after: params.after }),
        queryFn: () => backend.listImpersonations(params),
        enabled: params.tenant !== '' && (options?.enabled ?? true),
      }),
    useRevokeImpersonation: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminImpersonationRef) =>
          backend.revokeImpersonation(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({
            queryKey: getAuthnAdminImpersonationsListQueryKey(input.tenant),
          })
        },
      })
    },

    useInvitations: (tenant: string, params) =>
      useQuery({
        // A caller that passes page parameters (the organization members
        // page's status filter and cursor) keys by them too, so a filter
        // change is a new read rather than a reused first page. A caller
        // that passes none keeps the bare key the platform page always used.
        queryKey:
          params === undefined
            ? invitationsKey(tenant)
            : [...invitationsKey(tenant), params],
        queryFn: () =>
          params === undefined
            ? backend.listInvitations(tenant)
            : backend.listInvitations(tenant, params),
        enabled: tenant !== '',
      }),
    useIssueInvitation: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminIssueInvitationInput) =>
          backend.issueInvitation(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: invitationsKey(input.tenant) })
        },
      })
    },
    useRevokeInvitation: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminInvitationRef) =>
          backend.revokeInvitation(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: invitationsKey(input.tenant) })
        },
      })
    },

    useMembers: (tenant, params) =>
      useQuery({
        queryKey: [...membersKey(tenant), params ?? null],
        queryFn: async (): Promise<AdminMemberPage> => {
          const answer = await backend.listMembers(tenant, params)
          return 'members' in answer
            ? answer
            : { members: [...answer], next_cursor: '' }
        },
        placeholderData: keepPreviousData,
        enabled: tenant !== '',
      }),
    useRemoveMember: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminMemberRef) => backend.removeMember(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: membersKey(input.tenant) })
        },
      })
    },
    useWriteMember: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminWriteMemberInput) => backend.writeMember(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: membersKey(input.tenant) })
        },
      })
    },
    useSetMemberSsoExempt: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminSetMemberSSOExemptInput) =>
          backend.setMemberSsoExempt(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: membersKey(input.tenant) })
        },
      })
    },

    useSsoProviders: (tenant: string) =>
      useQuery({
        queryKey: ssoProvidersKey(tenant),
        queryFn: () => backend.listSsoProviders(tenant),
        enabled: tenant !== '',
      }),
    useSsoProvider: (query: AdminSsoProviderQuery) =>
      useQuery({
        queryKey: ssoProviderKey(query.id ?? ''),
        queryFn: () => backend.getSsoProvider(query),
        enabled: query.id !== undefined && query.id !== '',
      }),
    useDeleteSsoProvider: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminSsoProviderRef) => backend.deleteSsoProvider(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({
            queryKey: ssoProvidersKey(input.tenant),
          })
        },
      })
    },
    useRemoveSsoDomain: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminSsoDomainRef) => backend.removeSsoDomain(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: ssoProviderKey(input.id) })
          void client.invalidateQueries({
            queryKey: ssoProvidersKey(input.tenant),
          })
        },
      })
    },
    useVerifySsoDomain: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminSsoDomainRef) => backend.verifySsoDomain(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: ssoProviderKey(input.id) })
          void client.invalidateQueries({
            queryKey: ssoProvidersKey(input.tenant),
          })
        },
      })
    },
    useRefreshSsoMetadata: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminSsoProviderRef) => backend.refreshSsoMetadata(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: ssoProviderKey(input.id) })
          void client.invalidateQueries({
            queryKey: ssoProvidersKey(input.tenant),
          })
        },
      })
    },
    useReverifySsoDomains: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminSsoProviderRef) => backend.reverifySsoDomains(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: ssoProviderKey(input.id) })
          void client.invalidateQueries({
            queryKey: ssoProvidersKey(input.tenant),
          })
        },
      })
    },
    useCreateSsoProvider: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminCreateSsoProviderInput) =>
          backend.createSsoProvider(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({
            queryKey: ssoProvidersKey(input.tenant),
          })
        },
      })
    },
    useUpdateSsoProvider: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminWriteSsoProviderInput) =>
          backend.updateSsoProvider(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: ssoProviderKey(input.id) })
          void client.invalidateQueries({
            queryKey: ssoProvidersKey(input.tenant),
          })
        },
      })
    },
    useClaimSsoDomain: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminClaimSsoDomainInput) =>
          backend.claimSsoDomain(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: ssoProviderKey(input.id) })
          void client.invalidateQueries({
            queryKey: ssoProvidersKey(input.tenant),
          })
        },
      })
    },

    useTenantSettingsList: (tenant) =>
      useQuery({
        queryKey: tenantSettingsKey(tenant),
        queryFn: () => backend.listTenantSettings(tenant),
        enabled: tenant !== '',
      }),
    useTenantSettingsWrite: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminTenantSetSettingInput) =>
          backend.writeTenantSetting(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({
            queryKey: tenantSettingsKey(input.tenant),
          })
        },
      })
    },
    useTenantSettingsRemove: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminTenantRemoveSettingInput) =>
          backend.removeTenantSetting(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({
            queryKey: tenantSettingsKey(input.tenant),
          })
        },
      })
    },
    useTenantRoles: (tenant) =>
      useQuery({
        queryKey: tenantRolesKey(tenant),
        queryFn: () => backend.listTenantRoles(tenant),
        enabled: tenant !== '',
      }),
    useTenantPermissions: (tenant) =>
      useQuery({
        queryKey: tenantPermissionsKey(tenant),
        queryFn: () => backend.listTenantPermissions(tenant),
        enabled: tenant !== '',
      }),
    useTenantGrants: (tenant, role) =>
      useQuery({
        queryKey: tenantGrantsKey(tenant, role ?? ''),
        queryFn: () => backend.listTenantGrants(tenant, role ?? ''),
        enabled: tenant !== '' && role !== undefined && role !== '',
      }),
    useTenantRoleBindings: (tenant, role, params) =>
      useQuery({
        queryKey: [...tenantRoleBindingsKey(tenant, role ?? ''), params ?? null],
        queryFn: () =>
          backend.listTenantRoleBindings(tenant, role ?? '', params),
        enabled: tenant !== '' && role !== undefined && role !== '',
      }),
    useTenantBindings: (tenant) =>
      useQuery({
        queryKey: tenantBindingsKey(tenant),
        queryFn: () => backend.listTenantBindings(tenant),
        enabled: tenant !== '',
      }),
    useTenantAccountBindings: (tenant, account) =>
      useQuery({
        queryKey: tenantAccountBindingsKey(tenant, account ?? ''),
        queryFn: () =>
          backend.listTenantAccountBindings(tenant, account ?? ''),
        enabled: tenant !== '' && account !== undefined && account !== '',
      }),
    useTenantWriteRole: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminTenantWriteRoleInput) =>
          backend.writeTenantRole(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({
            queryKey: tenantRolesKey(input.tenant),
          })
        },
      })
    },
    useTenantRemoveRole: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminTenantRoleRef) =>
          backend.removeTenantRole(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({
            queryKey: tenantRolesKey(input.tenant),
          })
          void client.invalidateQueries({
            queryKey: tenantBindingsKey(input.tenant),
          })
          void client.invalidateQueries({
            queryKey: tenantGrantsKey(input.tenant, input.role),
          })
          void client.invalidateQueries({
            queryKey: tenantRoleBindingsKey(input.tenant, input.role),
          })
        },
      })
    },
    useTenantGrantPermission: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminTenantGrantRef) =>
          backend.grantTenantPermission(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({
            queryKey: tenantGrantsKey(input.tenant, input.role),
          })
        },
      })
    },
    useTenantRevokePermission: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminTenantGrantRef) =>
          backend.revokeTenantPermission(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({
            queryKey: tenantGrantsKey(input.tenant, input.role),
          })
        },
      })
    },
    useTenantBindRole: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminTenantBindingRef) =>
          backend.bindTenantRole(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({
            queryKey: tenantBindingsKey(input.tenant),
          })
          void client.invalidateQueries({
            queryKey: tenantAccountBindingsKey(input.tenant, input.account),
          })
        },
      })
    },
    useTenantUnbindRole: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminTenantBindingRef) =>
          backend.unbindTenantRole(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({
            queryKey: tenantBindingsKey(input.tenant),
          })
          void client.invalidateQueries({
            queryKey: tenantAccountBindingsKey(input.tenant, input.account),
          })
        },
      })
    },

    useSmsTemplates: () =>
      useQuery({
        queryKey: SMS_TEMPLATES_KEY,
        queryFn: () => backend.listSmsTemplates(),
      }),
    useRegisterSmsTemplate: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminRegisterSmsTemplateInput) =>
          backend.registerSmsTemplate(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: SMS_TEMPLATES_KEY })
        },
      })
    },
    useOfferSmsTemplate: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminOfferSmsTemplateInput) =>
          backend.offerSmsTemplate(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: SMS_TEMPLATES_KEY })
        },
      })
    },
    useSetSmsTemplateEnabled: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: AdminSetSmsTemplateEnabledInput) =>
          backend.setSmsTemplateEnabled(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: SMS_TEMPLATES_KEY })
        },
      })
    },
    useRemoveSmsTemplate: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: { readonly name: string }) =>
          backend.removeSmsTemplate(input.name),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: SMS_TEMPLATES_KEY })
        },
      })
    },
  }
}
