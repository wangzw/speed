/**
 * A scripted console operations bundle for the console-ui suites.
 *
 * The console pages read and write through the ConsoleOperations bundle
 * the host mounts (see src/operations.tsx). In production the host binds
 * the generated @speed/api-sdk operations; here the suite binds real
 * react-query hooks over a scripted backend, so the pages exercise
 * genuine react-query states -- the first-load pending branch, a settled
 * empty answer, a failed read, and a write whose success invalidates the
 * list it belongs to -- without an HTTP transport.
 *
 * The query keys live here, in test code, because the production keys
 * live inside the generated hooks; the scripted backend is the seam's
 * only stand-in. A backend function a test scripts to reject drives the
 * corresponding failure branch.
 *
 * The backend is grouped by administration surface, mirroring the bundle:
 * the tenant roster and settings (platform-side, tenant-free), the audit
 * query, the tenant-scoped authorization surface, the account, session
 * and impersonation surfaces, the tenant-scoped invitation and membership
 * surfaces, the SSO surface and the SMS template surface. Every default
 * settles successfully (empty reads, resolved writes); a test overrides
 * the member it exercises.
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type {
  ConsoleAccount,
  ConsoleAccountBindingsQuery,
  ConsoleAccountFactor,
  ConsoleAccountPage,
  ConsoleAccountQuery,
  ConsoleAccountRef,
  ConsoleAccountSecret,
  ConsoleAccountSecretCreated,
  ConsoleAccountSession,
  ConsoleAddIdentifierInput,
  ConsoleAuditPage,
  ConsoleAuditRecord,
  ConsoleAuditRecordQuery,
  ConsoleBinding,
  ConsoleBindingRef,
  ConsoleClaimSsoDomainInput,
  ConsoleCreateAccountInput,
  ConsoleCreateSecretInput,
  ConsoleCreateSsoProviderInput,
  ConsoleCreateTenantInput,
  ConsoleGrant,
  ConsolePatchAccountInput,
  ConsoleResetPasswordInput,
  ConsoleSetAccountStatusInput,
  ConsoleSetMFARequiredInput,
  ConsoleSetMemberSSOExemptInput,
  ConsoleSsoVerifyToken,
  ConsoleWriteMemberInput,
  ConsoleWriteSsoProviderInput,
  ConsoleGrantRef,
  ConsoleImpersonation,
  ConsoleInvitationPage,
  ConsoleInvitationRef,
  ConsoleIssueInvitationInput,
  ConsoleMember,
  ConsoleMemberRef,
  ConsoleOfferSmsTemplateInput,
  ConsoleOperations,
  ConsolePermission,
  ConsoleRegisterSmsTemplateInput,
  ConsoleRemoveIdentifierInput,
  ConsoleRemoveSettingInput,
  ConsoleRevokeSecretInput,
  ConsoleRevokeSessionInput,
  ConsoleRole,
  ConsoleRoleRef,
  ConsoleSetSettingInput,
  ConsoleSetSmsTemplateEnabledInput,
  ConsoleSetTenantAliasInput,
  ConsoleSetTenantStatusInput,
  ConsoleSetting,
  ConsoleSmsTemplatePage,
  ConsoleSsoDomainRef,
  ConsoleSsoProvider,
  ConsoleSsoProviderRef,
  ConsoleSsoProviderQuery,
  ConsoleTenant,
  ConsoleTenantQuery,
  ConsoleWriteRoleInput,
} from '../src/operations.js'

/** The scripted backend the operations bundle calls, grouped by surface. */
export interface ConsoleBackend {
  // Tenant roster + settings + audit (the platform-side three).
  listTenants: () => Promise<readonly ConsoleTenant[]>
  getTenant: (query: ConsoleTenantQuery) => Promise<ConsoleTenant>
  createTenant: (input: ConsoleCreateTenantInput) => Promise<ConsoleTenant>
  setTenantAlias: (input: ConsoleSetTenantAliasInput) => Promise<void>
  setTenantStatus: (input: ConsoleSetTenantStatusInput) => Promise<void>
  listSettings: (
    namespace: string | undefined,
  ) => Promise<readonly ConsoleSetting[]>
  setSetting: (input: ConsoleSetSettingInput) => Promise<void>
  removeSetting: (input: ConsoleRemoveSettingInput) => Promise<void>
  listRecords: (
    params?: Parameters<ConsoleOperations['useAuditRecords']>[0],
  ) => Promise<ConsoleAuditPage>
  getRecord: (query: ConsoleAuditRecordQuery) => Promise<ConsoleAuditRecord>

  // Authorization surface (tenant-scoped).
  listPermissions: () => Promise<readonly ConsolePermission[]>
  listRoles: (tenant: string) => Promise<readonly ConsoleRole[]>
  writeRole: (input: ConsoleWriteRoleInput) => Promise<void>
  removeRole: (input: ConsoleRoleRef) => Promise<void>
  listGrants: (tenant: string, role: string) => Promise<readonly ConsoleGrant[]>
  grantPermission: (input: ConsoleGrantRef) => Promise<void>
  revokePermission: (input: ConsoleGrantRef) => Promise<void>
  listBindings: (tenant: string) => Promise<readonly ConsoleBinding[]>
  listAccountBindings: (
    query: ConsoleAccountBindingsQuery,
  ) => Promise<readonly ConsoleBinding[]>
  bindRole: (input: ConsoleBindingRef) => Promise<void>
  unbindRole: (input: ConsoleBindingRef) => Promise<void>

  // Account surface.
  listAccounts: () => Promise<ConsoleAccountPage>
  getAccount: (query: ConsoleAccountQuery) => Promise<ConsoleAccount>
  listAccountFactors: (query: ConsoleAccountQuery) => Promise<readonly ConsoleAccountFactor[]>
  listAccountSecrets: (query: ConsoleAccountQuery) => Promise<readonly ConsoleAccountSecret[]>
  listAccountSessions: (query: ConsoleAccountQuery) => Promise<readonly ConsoleAccountSession[]>
  deleteAccount: (input: ConsoleAccountRef) => Promise<void>
  removeIdentifier: (input: ConsoleRemoveIdentifierInput) => Promise<void>
  resetMFA: (input: ConsoleAccountRef) => Promise<void>
  revokeAccountSessions: (input: ConsoleAccountRef) => Promise<void>
  revokeSecret: (input: ConsoleRevokeSecretInput) => Promise<void>
  revokeSession: (input: ConsoleRevokeSessionInput) => Promise<void>
  createAccount: (input: ConsoleCreateAccountInput) => Promise<ConsoleAccount>
  patchAccount: (input: ConsolePatchAccountInput) => Promise<ConsoleAccount>
  setAccountStatus: (input: ConsoleSetAccountStatusInput) => Promise<ConsoleAccount>
  setMFARequired: (input: ConsoleSetMFARequiredInput) => Promise<void>
  resetPassword: (input: ConsoleResetPasswordInput) => Promise<void>
  addIdentifier: (input: ConsoleAddIdentifierInput) => Promise<void>
  createSecret: (
    input: ConsoleCreateSecretInput,
  ) => Promise<ConsoleAccountSecretCreated>

  // Impersonation surface.
  listImpersonations: (
    params: Parameters<ConsoleOperations['useImpersonations']>[0],
  ) => Promise<readonly ConsoleImpersonation[]>
  revokeImpersonation: (input: ConsoleAccountRef) => Promise<void>

  // Invitation + membership surfaces (tenant-scoped).
  listInvitations: (tenant: string) => Promise<ConsoleInvitationPage>
  issueInvitation: (input: ConsoleIssueInvitationInput) => Promise<void>
  revokeInvitation: (input: ConsoleInvitationRef) => Promise<void>
  listMembers: (tenant: string) => Promise<readonly ConsoleMember[]>
  removeMember: (input: ConsoleMemberRef) => Promise<void>
  writeMember: (input: ConsoleWriteMemberInput) => Promise<void>
  setMemberSsoExempt: (input: ConsoleSetMemberSSOExemptInput) => Promise<void>

  // SSO surface.
  listSsoProviders: (tenant: string) => Promise<readonly ConsoleSsoProvider[]>
  getSsoProvider: (query: ConsoleSsoProviderQuery) => Promise<ConsoleSsoProvider>
  deleteSsoProvider: (input: ConsoleSsoProviderRef) => Promise<void>
  removeSsoDomain: (input: ConsoleSsoDomainRef) => Promise<void>
  verifySsoDomain: (input: ConsoleSsoDomainRef) => Promise<void>
  refreshSsoMetadata: (input: ConsoleSsoProviderRef) => Promise<void>
  reverifySsoDomains: (input: ConsoleSsoProviderRef) => Promise<void>
  createSsoProvider: (input: ConsoleCreateSsoProviderInput) => Promise<ConsoleSsoProvider>
  updateSsoProvider: (input: ConsoleWriteSsoProviderInput) => Promise<ConsoleSsoProvider>
  claimSsoDomain: (input: ConsoleClaimSsoDomainInput) => Promise<ConsoleSsoVerifyToken>

  // SMS template surface.
  listSmsTemplates: () => Promise<ConsoleSmsTemplatePage>
  registerSmsTemplate: (input: ConsoleRegisterSmsTemplateInput) => Promise<void>
  offerSmsTemplate: (input: ConsoleOfferSmsTemplateInput) => Promise<void>
  setSmsTemplateEnabled: (input: ConsoleSetSmsTemplateEnabledInput) => Promise<void>
  removeSmsTemplate: (name: string) => Promise<void>
}

/**
 * A working in-memory backend for the common case, with the named
 * behaviour overridden per test. Everything defaults to a settled
 * successful answer (empty reads and resolved writes); a test that wants
 * a failure passes a rejecting function for the member it exercises.
 */
export function makeConsoleBackend(
  overrides: Partial<ConsoleBackend> = {},
): ConsoleBackend {
  const emptyPage = <T,>(): { entries: T[]; next: string } => ({
    entries: [],
    next: '',
  })
  return {
    listTenants: () => Promise.resolve([]),
    getTenant: () => Promise.resolve(tenantFixture()),
    createTenant: (input) =>
      Promise.resolve({ id: input.id, alias: input.alias, status: 'active' }),
    setTenantAlias: () => Promise.resolve(),
    setTenantStatus: () => Promise.resolve(),
    listSettings: () => Promise.resolve([]),
    setSetting: () => Promise.resolve(),
    removeSetting: () => Promise.resolve(),
    listRecords: () => Promise.resolve(emptyPage<ConsoleAuditRecord>()),
    getRecord: () => Promise.resolve(recordFixture()),

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
    issueInvitation: () => Promise.resolve(),
    revokeInvitation: () => Promise.resolve(),
    listMembers: () => Promise.resolve([]),
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
  overrides: Partial<ConsoleAccount> = {},
): ConsoleAccount {
  return {
    id: 'acc-1',
    kind: 'user',
    status: 'active',
    display_name: 'Operator',
    mfa_required: false,
    protected: false,
    password_set: true,
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

/** A minimal tenant row for the default detail read. */
export function tenantFixture(
  overrides: Partial<ConsoleTenant> = {},
): ConsoleTenant {
  return {
    id: 'acme',
    alias: 'Acme',
    status: 'active',
    ...overrides,
  }
}

/** A minimal audit record for the default detail read. */
export function recordFixture(
  overrides: Partial<ConsoleAuditRecord> = {},
): ConsoleAuditRecord {
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
  overrides: Partial<ConsoleSsoProvider> = {},
): ConsoleSsoProvider {
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
const IMPERSONATIONS_KEY = ['console-test', 'impersonations'] as const
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

/**
 * Builds a ConsoleOperations whose hooks run real react-query queries and
 * mutations over the given backend. Call it in the test body and pass the
 * result to renderWithProviders; keep the returned object stable across a
 * render (a module-level const or a value captured before render).
 */
export function createConsoleOperations(
  backend: ConsoleBackend,
): ConsoleOperations {
  return {
    useTenants: () =>
      useQuery({ queryKey: TENANTS_KEY, queryFn: () => backend.listTenants() }),
    useTenant: (query: ConsoleTenantQuery) =>
      useQuery({
        queryKey: tenantKey(query.id ?? ''),
        queryFn: () => backend.getTenant(query),
        enabled: query.id !== undefined && query.id !== '',
      }),
    useCreateTenant: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleCreateTenantInput) =>
          backend.createTenant(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: TENANTS_KEY })
        },
      })
    },
    useSetTenantAlias: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleSetTenantAliasInput) =>
          backend.setTenantAlias(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: TENANTS_KEY })
        },
      })
    },
    useSetTenantStatus: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleSetTenantStatusInput) =>
          backend.setTenantStatus(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: TENANTS_KEY })
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
        mutationFn: (input: ConsoleSetSettingInput) => backend.setSetting(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: ['console-test', 'settings'] })
        },
      })
    },
    useRemoveSetting: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleRemoveSettingInput) =>
          backend.removeSetting(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: ['console-test', 'settings'] })
        },
      })
    },
    useAuditRecords: (params) =>
      useQuery({
        queryKey: [...AUDIT_KEY, params ?? null],
        queryFn: () => backend.listRecords(params),
      }),
    useAuditRecord: (query: ConsoleAuditRecordQuery) =>
      useQuery({
        queryKey: auditRecordKey(query.id ?? ''),
        queryFn: () => backend.getRecord(query),
        enabled: query.id !== undefined && query.id !== '',
      }),

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
        mutationFn: (input: ConsoleWriteRoleInput) => backend.writeRole(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: rolesKey(input.tenant) })
        },
      })
    },
    useRemoveRole: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleRoleRef) => backend.removeRole(input),
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
        mutationFn: (input: ConsoleGrantRef) => backend.grantPermission(input),
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
        mutationFn: (input: ConsoleGrantRef) => backend.revokePermission(input),
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
    useAccountBindings: (query: ConsoleAccountBindingsQuery) =>
      useQuery({
        queryKey: accountBindingsKey(query.tenant, query.account ?? ''),
        queryFn: () => backend.listAccountBindings(query),
        enabled: query.account !== undefined && query.account !== '',
      }),
    useBindRole: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleBindingRef) => backend.bindRole(input),
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
        mutationFn: (input: ConsoleBindingRef) => backend.unbindRole(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: bindingsKey(input.tenant) })
          void client.invalidateQueries({
            queryKey: accountBindingsKey(input.tenant, input.account),
          })
        },
      })
    },

    useAccounts: () =>
      useQuery({ queryKey: ACCOUNTS_KEY, queryFn: () => backend.listAccounts() }),
    useAccount: (query: ConsoleAccountQuery) =>
      useQuery({
        queryKey: accountKey(query.id ?? ''),
        queryFn: () => backend.getAccount(query),
        enabled: query.id !== undefined && query.id !== '',
      }),
    useAccountFactors: (query: ConsoleAccountQuery) =>
      useQuery({
        queryKey: factorsKey(query.id ?? ''),
        queryFn: () => backend.listAccountFactors(query),
        enabled: query.id !== undefined && query.id !== '',
      }),
    useAccountSecrets: (query: ConsoleAccountQuery) =>
      useQuery({
        queryKey: secretsKey(query.id ?? ''),
        queryFn: () => backend.listAccountSecrets(query),
        enabled: query.id !== undefined && query.id !== '',
      }),
    useAccountSessions: (query: ConsoleAccountQuery) =>
      useQuery({
        queryKey: sessionsKey(query.id ?? ''),
        queryFn: () => backend.listAccountSessions(query),
        enabled: query.id !== undefined && query.id !== '',
      }),
    useDeleteAccount: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleAccountRef) => backend.deleteAccount(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: ACCOUNTS_KEY })
        },
      })
    },
    useRemoveIdentifier: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleRemoveIdentifierInput) =>
          backend.removeIdentifier(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: accountKey(input.id) })
        },
      })
    },
    useResetMFA: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleAccountRef) => backend.resetMFA(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: factorsKey(input.id) })
          void client.invalidateQueries({ queryKey: accountKey(input.id) })
        },
      })
    },
    useRevokeAccountSessions: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleAccountRef) =>
          backend.revokeAccountSessions(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: sessionsKey(input.id) })
        },
      })
    },
    useRevokeSecret: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleRevokeSecretInput) =>
          backend.revokeSecret(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: secretsKey(input.id) })
        },
      })
    },
    useRevokeSession: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleRevokeSessionInput) =>
          backend.revokeSession(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: sessionsKey(input.accountId) })
        },
      })
    },
    useCreateAccount: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleCreateAccountInput) =>
          backend.createAccount(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: ACCOUNTS_KEY })
        },
      })
    },
    usePatchAccount: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsolePatchAccountInput) =>
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
        mutationFn: (input: ConsoleSetAccountStatusInput) =>
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
        mutationFn: (input: ConsoleSetMFARequiredInput) =>
          backend.setMFARequired(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: accountKey(input.id) })
        },
      })
    },
    useResetPassword: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleResetPasswordInput) =>
          backend.resetPassword(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: sessionsKey(input.id) })
        },
      })
    },
    useAddIdentifier: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleAddIdentifierInput) =>
          backend.addIdentifier(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: accountKey(input.id) })
        },
      })
    },
    useCreateSecret: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleCreateSecretInput) =>
          backend.createSecret(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: secretsKey(input.id) })
        },
      })
    },

    useImpersonations: (params) =>
      useQuery({
        queryKey: [...IMPERSONATIONS_KEY, params],
        queryFn: () => backend.listImpersonations(params),
        enabled: params.tenant !== '',
      }),
    useRevokeImpersonation: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleAccountRef) =>
          backend.revokeImpersonation(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: IMPERSONATIONS_KEY })
        },
      })
    },

    useInvitations: (tenant: string) =>
      useQuery({
        queryKey: invitationsKey(tenant),
        queryFn: () => backend.listInvitations(tenant),
        enabled: tenant !== '',
      }),
    useIssueInvitation: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleIssueInvitationInput) =>
          backend.issueInvitation(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: invitationsKey(input.tenant) })
        },
      })
    },
    useRevokeInvitation: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleInvitationRef) =>
          backend.revokeInvitation(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: invitationsKey(input.tenant) })
        },
      })
    },

    useMembers: (tenant: string) =>
      useQuery({
        queryKey: membersKey(tenant),
        queryFn: () => backend.listMembers(tenant),
        enabled: tenant !== '',
      }),
    useRemoveMember: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleMemberRef) => backend.removeMember(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: membersKey(input.tenant) })
        },
      })
    },
    useWriteMember: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleWriteMemberInput) => backend.writeMember(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: membersKey(input.tenant) })
        },
      })
    },
    useSetMemberSsoExempt: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleSetMemberSSOExemptInput) =>
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
    useSsoProvider: (query: ConsoleSsoProviderQuery) =>
      useQuery({
        queryKey: ssoProviderKey(query.id ?? ''),
        queryFn: () => backend.getSsoProvider(query),
        enabled: query.id !== undefined && query.id !== '',
      }),
    useDeleteSsoProvider: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleSsoProviderRef) => backend.deleteSsoProvider(input),
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
        mutationFn: (input: ConsoleSsoDomainRef) => backend.removeSsoDomain(input),
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
        mutationFn: (input: ConsoleSsoDomainRef) => backend.verifySsoDomain(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: ssoProviderKey(input.id) })
        },
      })
    },
    useRefreshSsoMetadata: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleSsoProviderRef) => backend.refreshSsoMetadata(input),
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
        mutationFn: (input: ConsoleSsoProviderRef) => backend.reverifySsoDomains(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: ssoProviderKey(input.id) })
        },
      })
    },
    useCreateSsoProvider: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleCreateSsoProviderInput) =>
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
        mutationFn: (input: ConsoleWriteSsoProviderInput) =>
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
        mutationFn: (input: ConsoleClaimSsoDomainInput) =>
          backend.claimSsoDomain(input),
        onSuccess: (_answer, input) => {
          void client.invalidateQueries({ queryKey: ssoProviderKey(input.id) })
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
        mutationFn: (input: ConsoleRegisterSmsTemplateInput) =>
          backend.registerSmsTemplate(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: SMS_TEMPLATES_KEY })
        },
      })
    },
    useOfferSmsTemplate: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleOfferSmsTemplateInput) =>
          backend.offerSmsTemplate(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: SMS_TEMPLATES_KEY })
        },
      })
    },
    useSetSmsTemplateEnabled: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleSetSmsTemplateEnabledInput) =>
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
