/**
 * The organization administration surface's operations bundle: the
 * tenant-scoped reads and writes the organization pages run through.
 *
 * Every member here addresses `/api/v1/tenants/{tenant}/...`; the tenant is
 * an argument the caller supplies from the current tenant the host handed
 * the organization provider. The bundle holds no platform member and never
 * addresses `/api/v1/system`, so an organization page cannot reach a platform
 * asset even by mistake.
 *
 * The invitation, membership and SSO members overlap with the console
 * bundle's generated bindings: the same generated operations, re-bound
 * here over the tenant-scoped key convention. Their generated keys name
 * the tenant only inside the path string, so forwarding them verbatim
 * would put the reads outside the tenant subtree the host evicts on a
 * switch; the bundle wraps every read's key in `tenantQueryKey` and points
 * every write's invalidation at the same tenant-prefixed factory. The
 * settings and role members are the tenant management surface's own
 * generated operations (`settings.tenantadmin` and `authz.tenantadmin`),
 * which the console bundle does not carry: their paths are
 * tenant-addressed, so they belong to this surface alone.
 *
 * The reads' query keys follow the tenant-scoped convention
 * (docs/adr/adr-web-tenant-query-key.md): auth-core's `tenantQueryKey`
 * prepends the tenant id as the key's outermost segment over the generated
 * bare key, so one tenant's reads share a prefix, a switch to another
 * tenant builds another key, and the host's `evictTenantQueries` removes
 * the departing tenant's whole subtree. The writes invalidate through the
 * same factory, so a write refreshes only the tenant it names.
 *
 * `OrganizationAdminProvider` publishes the bundle through
 * `OrganizationAdminOperationsProvider`; a host that mounts its own bundle at
 * the provider substitutes the wiring, and with none mounted the shipped
 * `organizationAdminOperations` answers.
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
  authnAdminMembersPut,
  authnAdminMembersRemove,
  authnAdminMembersSsoExempt,
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
  authzTenantadminBindingsDelete,
  authzTenantadminBindingsPut,
  authzTenantadminGrantsDelete,
  authzTenantadminGrantsPut,
  authzTenantadminRolesDelete,
  authzTenantadminRolesPut,
  getAuthnAdminMembersListQueryKey,
  getAuthnAdminMembersListQueryOptions,
  getAuthnAdminSsoProvidersGetQueryKey,
  getAuthnAdminSsoProvidersGetQueryOptions,
  getAuthnAdminSsoProvidersListQueryKey,
  getAuthnAdminSsoProvidersListQueryOptions,
  getAuthnInviteListQueryKey,
  getAuthnInviteListQueryOptions,
  getAuthzTenantadminBindingsListQueryKey,
  getAuthzTenantadminBindingsListQueryOptions,
  getAuthzTenantadminBindingsOfQueryKey,
  getAuthzTenantadminBindingsOfQueryOptions,
  getAuthzTenantadminGrantsListQueryKey,
  getAuthzTenantadminGrantsListQueryOptions,
  getAuthzTenantadminPermissionsListQueryOptions,
  getAuthzTenantadminRolesBindingsListQueryKey,
  getAuthzTenantadminRolesBindingsListQueryOptions,
  getAuthzTenantadminRolesListQueryKey,
  getAuthzTenantadminRolesListQueryOptions,
  getSettingsTenantadminSettingsListQueryKey,
  getSettingsTenantadminSettingsListQueryOptions,
  getTenantAdminOwnerPendingTransferQueryKey,
  getTenantAdminOwnerPendingTransferQueryOptions,
  getTenantProfileGetQueryKey,
  getTenantProfileGetQueryOptions,
  settingsTenantadminSettingsRemove,
  settingsTenantadminSettingsSet,
  tenantAdminOwnerCancelTransfer,
  tenantAdminOwnerDelete,
  tenantAdminOwnerRequestTransfer,
  tenantProfileUpdate,
} from '@speed/api-sdk'
import type {
  AuthnInviteIssueBody,
  AuthzTenantAdminBindingView,
  AuthzTenantAdminGrantView,
  AuthzTenantAdminPermissionView,
  AuthzTenantAdminRoleBindingView,
  AuthzTenantAdminRoleView,
  AuthzTenantadminRolesBindingsList200,
  AuthzTenantadminRolesBindingsListParams,
  SettingsTenantAdminSetting,
  SettingsTenantAdminSettingSource,
  SettingsTenantAdminSettings,
  TenantProfileView,
  TenantTransferView,
} from '@speed/api-sdk'
import { tenantQueryKey } from '@speed/auth-core'
import { errorCodeOf } from '@speed/ui-kit'
import type {
  AdminClaimSsoDomainInput,
  AdminCreateSsoProviderInput,
  AdminInvitationRef,
  AdminIssueInvitationInput,
  AdminMemberRef,
  AdminOperations,
  AdminSetMemberSSOExemptInput,
  AdminSsoDomainRef,
  AdminSsoProviderRef,
  AdminWriteMemberInput,
  AdminWriteSsoProviderInput,
} from './operations.js'
import { INVITATION_TERMINAL_CODE, useResendInvitationMutation } from './operations.js'
import { profileWriteOf } from './TenantProfileFields.js'
import type { TenantProfileFields } from './TenantProfileFields.js'

/** The tenant settings surface's read answer: the declared items it
 * exposes and whether this tenant's settings may be written at all. */
export type AdminTenantSettings = SettingsTenantAdminSettings

/** One declared setting as the tenant layer reports it -- the generated
 * row of GET /api/v1/tenants/{tenant}/settings, carrying the platform
 * value, the tenant value, their OR and the layer the effective value
 * points at. */
export type AdminTenantSetting = SettingsTenantAdminSetting

/** The layer the tenant read reports a value came from: the tenant layer,
 * the global layer, or the declared default. The server decides it; the
 * page renders the reported value and never infers it. */
export type AdminTenantSettingSource = SettingsTenantAdminSettingSource

/** One tenant's organization profile (GET
 * /api/v1/tenants/{tenant}/profile): the read-only identity, the four
 * descriptive fields and the owner account. */
export type AdminTenantProfile = TenantProfileView

/** The profile write's target and draft. The route carries the tenant and
 * the body carries all four fields. */
export interface AdminTenantProfileWriteInput {
  readonly tenant: string
  readonly fields: TenantProfileFields
}

/** One owner-transfer request as the organization's own surface reports it
 * (GET .../owner/requests/pending). Only the target's display name and
 * username are carried, never its account identifier. */
export type AdminTenantTransfer = TenantTransferView

/** POST /api/v1/tenants/{tenant}/owner/requests body: the active member the
 * current owner asks to take ownership. Ownership does not change until the
 * target confirms. */
export interface AdminTenantOwnerRequestInput {
  readonly tenant: string
  readonly account: string
}

/** DELETE .../owner/requests/{request} target: the pending request the
 * current owner cancels. */
export interface AdminTenantOwnerRequestRef {
  readonly tenant: string
  readonly request: string
}

/** DELETE /api/v1/tenants/{tenant} target. */
export interface AdminTenantRef {
  readonly tenant: string
}

/** One tenant role (GET .../tenants/{tenant}/roles), structurally the
 * console's role row. */
export type AdminTenantRole = AuthzTenantAdminRoleView

/** One governed permission (GET .../tenants/{tenant}/permissions). */
export type AdminTenantPermission = AuthzTenantAdminPermissionView

/** One permission grant of a tenant role (GET .../roles/{role}/grants). */
export type AdminTenantGrant = AuthzTenantAdminGrantView

/** One role binding within a tenant (GET .../tenants/{tenant}/bindings). */
export type AdminTenantBinding = AuthzTenantAdminBindingView

/** One account holding a role, as the role-usage read reports it: the
 * account, the display name the host's identity reader resolved, whether
 * the membership is active (unknown when no reader answered) and when the
 * binding was written. */
export type AdminTenantRoleBinding = AuthzTenantAdminRoleBindingView

/** One page of a role's holders together with the cursor of the next page. */
export type AdminTenantRoleBindingPage = AuthzTenantadminRolesBindingsList200

/** The role-usage read's cursor parameters. */
export type AdminTenantRoleBindingsQuery =
  AuthzTenantadminRolesBindingsListParams

/** PUT .../tenants/{tenant}/settings/{namespace}/{key} body: the item to
 * write on the tenant layer. */
export interface AdminTenantSetSettingInput {
  readonly tenant: string
  readonly namespace: string
  readonly key: string
  readonly value: string
}

/** DELETE .../tenants/{tenant}/settings/{namespace}/{key} target. */
export interface AdminTenantRemoveSettingInput {
  readonly tenant: string
  readonly namespace: string
  readonly key: string
}

/**
 * PUT .../tenants/{tenant}/roles/{role} body: a role's declared shape. The
 * role name is the key a binding holds and is never renamed, so a write
 * names an existing role or creates one; `description` is the operator's
 * free text.
 */
export interface AdminTenantWriteRoleInput {
  readonly tenant: string
  readonly role: string
  readonly description: string
}

/** A tenant-and-role target (delete a role, list its grants). */
export interface AdminTenantRoleRef {
  readonly tenant: string
  readonly role: string
}

/** A grant target: the role and the permission it grants or revokes. */
export interface AdminTenantGrantRef {
  readonly tenant: string
  readonly role: string
  readonly permission: string
}

/** A binding target: the account and the role bound or unbound. */
export interface AdminTenantBindingRef {
  readonly tenant: string
  readonly account: string
  readonly role: string
}

/**
 * The tenant-scoped operations the organization pages read and write through:
 * invitations and membership, SSO providers, the tenant settings layer and
 * the tenant roles, permissions, grants and bindings.
 */
export interface OrganizationAdminOperations
  extends Pick<
    AdminOperations,
    | 'useInvitations'
    | 'useIssueInvitation'
    | 'useRevokeInvitation'
    | 'useResendInvitation'
    | 'useMembers'
    | 'useRemoveMember'
    | 'useWriteMember'
    | 'useSetMemberSsoExempt'
    | 'useSsoProviders'
    | 'useSsoProvider'
    | 'useDeleteSsoProvider'
    | 'useRemoveSsoDomain'
    | 'useVerifySsoDomain'
    | 'useRefreshSsoMetadata'
    | 'useReverifySsoDomains'
    | 'useCreateSsoProvider'
    | 'useUpdateSsoProvider'
    | 'useClaimSsoDomain'
  > {
  // --- Tenant profile (/api/v1/tenants/{tenant}/profile) ----------------

  /** GET .../tenants/{tenant}/profile -- the organization's profile. */
  readonly useTenantProfile: (
    tenant: string,
  ) => UseQueryResult<AdminTenantProfile, unknown>
  /** PATCH .../tenants/{tenant}/profile -- write the four descriptive
   * fields; the identity is not writable. */
  readonly useTenantProfileWrite: () => UseMutationResult<
    void,
    unknown,
    AdminTenantProfileWriteInput
  >

  // --- Owner transfer and organization deletion (/api/v1/tenants/{tenant}) ---

  /** GET .../tenants/{tenant}/owner/requests/pending -- the organization's
   * pending transfer request, or null when it has none. `enabled` holds the
   * owner-only read back for a caller who is not the owner. */
  readonly useTenantOwnerPending: (
    tenant: string,
    enabled?: boolean,
  ) => UseQueryResult<AdminTenantTransfer | null, unknown>
  /** POST .../tenants/{tenant}/owner/requests -- ask an active member to
   * take ownership. Ownership is unchanged until the target confirms. */
  readonly useTenantOwnerRequest: () => UseMutationResult<
    AdminTenantTransfer,
    unknown,
    AdminTenantOwnerRequestInput
  >
  /** DELETE .../tenants/{tenant}/owner/requests/{request} -- cancel the
   * pending request. Ownership is unchanged. */
  readonly useTenantOwnerCancel: () => UseMutationResult<
    void,
    unknown,
    AdminTenantOwnerRequestRef
  >
  /** DELETE .../tenants/{tenant} -- tombstone the organization and clear its
   * core relations. Terminal: there is no restore route. */
  readonly useTenantOwnerDelete: () => UseMutationResult<
    void,
    unknown,
    AdminTenantRef
  >

  // --- Tenant settings (/api/v1/tenants/{tenant}/settings) --------------

  /** GET .../tenants/{tenant}/settings -- every item the declarations
   * expose to a tenant, the platform and tenant values beside the
   * effective one, and whether the host would accept a write at all. */
  readonly useTenantSettingsList: (
    tenant: string,
  ) => UseQueryResult<AdminTenantSettings, unknown>
  /** PUT .../settings/{namespace}/{key} -- write one item on the tenant layer. */
  readonly useTenantSettingsWrite: () => UseMutationResult<
    void,
    unknown,
    AdminTenantSetSettingInput
  >
  /** DELETE .../settings/{namespace}/{key} -- drop the tenant layer's row. */
  readonly useTenantSettingsRemove: () => UseMutationResult<
    void,
    unknown,
    AdminTenantRemoveSettingInput
  >

  // --- Tenant authorization (/api/v1/tenants/{tenant}) ------------------

  /** GET .../tenants/{tenant}/roles -- a tenant's roles. */
  readonly useTenantRoles: (
    tenant: string,
  ) => UseQueryResult<readonly AdminTenantRole[], unknown>
  /** GET .../tenants/{tenant}/permissions -- the governed permission set a
   * tenant administrator may hold and grant. */
  readonly useTenantPermissions: (
    tenant: string,
  ) => UseQueryResult<readonly AdminTenantPermission[], unknown>
  /** GET .../tenants/{tenant}/roles/{role}/grants -- a role's grants
   * (disabled while no role is chosen). */
  readonly useTenantGrants: (
    tenant: string,
    role: string | undefined,
  ) => UseQueryResult<readonly AdminTenantGrant[], unknown>
  /** GET .../tenants/{tenant}/roles/{role}/bindings -- one page of the
   * accounts holding the role, with the cursor of the next page (disabled
   * while no role is chosen). */
  readonly useTenantRoleBindings: (
    tenant: string,
    role: string | undefined,
    params?: AdminTenantRoleBindingsQuery,
  ) => UseQueryResult<AdminTenantRoleBindingPage, unknown>
  /** GET .../tenants/{tenant}/bindings -- a tenant's bindings. */
  readonly useTenantBindings: (
    tenant: string,
  ) => UseQueryResult<readonly AdminTenantBinding[], unknown>
  /** GET .../tenants/{tenant}/bindings/{account} -- the bindings an
   * account holds within the tenant (disabled while no account is chosen). */
  readonly useTenantAccountBindings: (
    tenant: string,
    account: string | undefined,
  ) => UseQueryResult<readonly AdminTenantBinding[], unknown>
  /** PUT .../tenants/{tenant}/roles/{role} -- create or rewrite a role. */
  readonly useTenantWriteRole: () => UseMutationResult<
    void,
    unknown,
    AdminTenantWriteRoleInput
  >
  /** DELETE .../tenants/{tenant}/roles/{role} -- remove a role and its
   * grants and bindings with it. */
  readonly useTenantRemoveRole: () => UseMutationResult<
    void,
    unknown,
    AdminTenantRoleRef
  >
  /** PUT .../roles/{role}/grants/{permission} -- grant a governed permission. */
  readonly useTenantGrantPermission: () => UseMutationResult<
    void,
    unknown,
    AdminTenantGrantRef
  >
  /** DELETE .../roles/{role}/grants/{permission} -- revoke a permission. */
  readonly useTenantRevokePermission: () => UseMutationResult<
    void,
    unknown,
    AdminTenantGrantRef
  >
  /** PUT .../bindings/{account}/{role} -- bind a role to an account. */
  readonly useTenantBindRole: () => UseMutationResult<
    void,
    unknown,
    AdminTenantBindingRef
  >
  /** DELETE .../bindings/{account}/{role} -- unbind a role from an account. */
  readonly useTenantUnbindRole: () => UseMutationResult<
    void,
    unknown,
    AdminTenantBindingRef
  >
}

/**
 * The shipped organization bundle: the invitation, membership and SSO
 * operations re-bound over the tenant-scoped key convention, plus the
 * tenant management surface's own generated operations. A host that needs a
 * different wiring mounts its own bundle on `OrganizationAdminProvider`'s
 * `operations` prop instead.
 */
export const organizationAdminOperations: OrganizationAdminOperations = {
  // --- Invitation surface (tenant-scoped) -------------------------------

  useInvitations: (tenant, params) => {
    const options = getAuthnInviteListQueryOptions(tenant, params)
    return useQuery({
      ...options,
      queryKey: tenantQueryKey(tenant, ...options.queryKey),
      select: (page) => page,
      enabled: tenant !== '',
    })
  },

  useIssueInvitation: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminIssueInvitationInput) => {
        const body: AuthnInviteIssueBody = {
          email: input.email,
          ...(input.expiresAt === undefined
            ? {}
            : { expires_at: input.expiresAt }),
          ...(input.role === undefined ? {} : { role: input.role }),
        }
        return authnInviteIssue(input.tenant, body)
      },
      onSuccess: (_answer, input) => {
        invalidateTenantInvitations(queryClient, input.tenant)
      },
    })
  },

  useRevokeInvitation: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminInvitationRef) =>
        authnInviteRevoke(input.tenant, input.id),
      onSuccess: (_answer, input) => {
        invalidateTenantInvitations(queryClient, input.tenant)
      },
      // A row another operator already moved answers 409 invitation-terminal.
      // The list has not been re-read, so it still shows the old state;
      // invalidating lets the server's judgement reach the row.
      onError: (error, input) => {
        if (errorCodeOf(error) === INVITATION_TERMINAL_CODE) {
          invalidateTenantInvitations(queryClient, input.tenant)
        }
      },
    })
  },

  useResendInvitation: () =>
    useResendInvitationMutation(invalidateTenantInvitations),

  // --- Membership surface (tenant-scoped) -------------------------------

  useMembers: (tenant, params) => {
    const options = getAuthnAdminMembersListQueryOptions(tenant, params)
    return useQuery({
      ...options,
      queryKey: tenantQueryKey(tenant, ...options.queryKey),
      // The page envelope carries the cursor of the page that follows; the
      // rows alone cannot tell a full last page from one with more behind it.
      // The previous page stays on screen while a search or page change is in
      // flight, so the table does not collapse under the operator.
      placeholderData: keepPreviousData,
      enabled: tenant !== '',
    })
  },

  useRemoveMember: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminMemberRef) =>
        authnAdminMembersRemove(input.tenant, input.account),
      onSuccess: (_answer, input) => {
        invalidateTenantMembers(queryClient, input.tenant)
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
        invalidateTenantMembers(queryClient, input.tenant)
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
        invalidateTenantMembers(queryClient, input.tenant)
      },
    })
  },

  // --- SSO surface (/api/v1/tenants/:tenant/sso) ------------------------

  useSsoProviders: (tenant) => {
    const options = getAuthnAdminSsoProvidersListQueryOptions(tenant)
    return useQuery({
      ...options,
      queryKey: tenantQueryKey(tenant, ...options.queryKey),
      select: (page) => page.providers,
      enabled: tenant !== '',
    })
  },

  useSsoProvider: ({ tenant, id }) => {
    const options = getAuthnAdminSsoProvidersGetQueryOptions(tenant, id ?? '')
    return useQuery({
      ...options,
      queryKey: tenantQueryKey(tenant, ...options.queryKey),
      select: (envelope) => envelope.provider,
      enabled: id !== undefined && id !== '',
    })
  },

  useDeleteSsoProvider: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminSsoProviderRef) =>
        authnAdminSsoProvidersDelete(input.tenant, input.id),
      onSuccess: (_answer, input) => {
        invalidateTenantSsoProviderList(queryClient, input.tenant)
        queryClient.removeQueries({
          queryKey: tenantQueryKey(
            input.tenant,
            ...getAuthnAdminSsoProvidersGetQueryKey(input.tenant, input.id),
          ),
        })
      },
    })
  },

  useRemoveSsoDomain: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminSsoDomainRef) =>
        authnAdminSsoProvidersRemoveDomain(
          input.tenant,
          input.id,
          input.domain,
        ),
      onSuccess: (_answer, input) => {
        invalidateTenantSsoProvider(queryClient, input.tenant, input.id)
      },
    })
  },

  useVerifySsoDomain: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminSsoDomainRef) =>
        authnAdminSsoProvidersVerifyDomain(
          input.tenant,
          input.id,
          input.domain,
        ),
      onSuccess: (_answer, input) => {
        invalidateTenantSsoProvider(queryClient, input.tenant, input.id)
      },
    })
  },

  useRefreshSsoMetadata: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminSsoProviderRef) =>
        authnAdminSsoProvidersRefreshMetadata(
          input.tenant,
          input.id,
        ).then(() => undefined),
      onSuccess: (_answer, input) => {
        invalidateTenantSsoProvider(queryClient, input.tenant, input.id)
      },
    })
  },

  useReverifySsoDomains: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminSsoProviderRef) =>
        authnAdminSsoProvidersReverifyDomains(input.tenant, input.id),
      onSuccess: (_answer, input) => {
        invalidateTenantSsoProvider(queryClient, input.tenant, input.id)
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
        invalidateTenantSsoProviderList(queryClient, input.tenant)
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
        invalidateTenantSsoProvider(queryClient, input.tenant, input.id)
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
        invalidateTenantSsoProvider(queryClient, input.tenant, input.id)
      },
    })
  },

  // --- Tenant profile (/api/v1/tenants/{tenant}/profile) ----------------

  useTenantProfile: (tenant) => {
    const options = getTenantProfileGetQueryOptions(tenant)
    return useQuery({
      ...options,
      queryKey: tenantQueryKey(tenant, ...options.queryKey),
      select: (envelope) => envelope.profile,
      enabled: tenant !== '',
    })
  },

  useTenantProfileWrite: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminTenantProfileWriteInput) =>
        tenantProfileUpdate(input.tenant, profileWriteOf(input.fields)).then(
          () => undefined,
        ),
      onSuccess: (_answer, input) => {
        invalidateTenantProfile(queryClient, input.tenant)
      },
    })
  },

  // --- Owner transfer and organization deletion -------------------------

  useTenantOwnerPending: (tenant, enabled = true) => {
    const options = getTenantAdminOwnerPendingTransferQueryOptions(tenant)
    return useQuery({
      ...options,
      queryKey: tenantQueryKey(tenant, ...options.queryKey),
      select: (envelope) => envelope.request,
      enabled: tenant !== '' && enabled,
    })
  },

  useTenantOwnerRequest: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminTenantOwnerRequestInput) =>
        tenantAdminOwnerRequestTransfer(input.tenant, {
          account: input.account,
        }).then((answer) => answer.request),
      onSuccess: (_answer, input) => {
        // The request is recorded but ownership has not changed. The pending
        // read is what the card renders now; the member rows are re-read so
        // the dialog's target list sees the current roster.
        invalidateTenantOwnerPending(queryClient, input.tenant)
        invalidateTenantMembers(queryClient, input.tenant)
      },
    })
  },

  useTenantOwnerCancel: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminTenantOwnerRequestRef) =>
        tenantAdminOwnerCancelTransfer(input.tenant, input.request),
      onSuccess: (_answer, input) => {
        // A cancelled request leaves ownership untouched; the card's pending
        // state and the profile it sits under are re-read.
        invalidateTenantOwnerPending(queryClient, input.tenant)
        invalidateTenantProfile(queryClient, input.tenant)
      },
    })
  },

  useTenantOwnerDelete: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminTenantRef) =>
        tenantAdminOwnerDelete(input.tenant),
      onSuccess: (_answer, input) => {
        // The organization is a tombstone and the caller is no longer a
        // member: its reads can only be refused (404). The terminal state is
        // reflected into the cached profile; the host's departure action
        // leaves the organization. The deletion also settles any pending
        // transfer request, so its cached read is dropped rather than
        // re-read.
        reflectTenantProfile(queryClient, input.tenant, (profile) => ({
          ...profile,
          status: 'deleted',
        }))
        queryClient.removeQueries({
          queryKey: tenantQueryKey(
            input.tenant,
            ...getTenantAdminOwnerPendingTransferQueryKey(input.tenant),
          ),
        })
      },
    })
  },

  // --- Tenant settings (/api/v1/tenants/{tenant}/settings) --------------

  useTenantSettingsList: (tenant) => {
    const options = getSettingsTenantadminSettingsListQueryOptions(tenant)
    return useQuery({
      ...options,
      queryKey: tenantQueryKey(tenant, ...options.queryKey),
      enabled: tenant !== '',
    })
  },

  useTenantSettingsWrite: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminTenantSetSettingInput) =>
        settingsTenantadminSettingsSet(
          input.tenant,
          input.namespace,
          input.key,
          { value: input.value },
        ),
      onSuccess: (_answer, input) => {
        invalidateTenantSettings(queryClient, input.tenant)
      },
    })
  },

  useTenantSettingsRemove: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminTenantRemoveSettingInput) =>
        settingsTenantadminSettingsRemove(
          input.tenant,
          input.namespace,
          input.key,
        ),
      onSuccess: (_answer, input) => {
        invalidateTenantSettings(queryClient, input.tenant)
      },
    })
  },

  useTenantRoles: (tenant) => {
    const options = getAuthzTenantadminRolesListQueryOptions(tenant)
    return useQuery({
      ...options,
      queryKey: tenantQueryKey(tenant, ...options.queryKey),
      select: (page) => page.roles,
      enabled: tenant !== '',
    })
  },

  useTenantPermissions: (tenant) => {
    const options = getAuthzTenantadminPermissionsListQueryOptions(tenant)
    return useQuery({
      ...options,
      queryKey: tenantQueryKey(tenant, ...options.queryKey),
      select: (page) => page.permissions,
      enabled: tenant !== '',
    })
  },

  useTenantGrants: (tenant, role) => {
    const options = getAuthzTenantadminGrantsListQueryOptions(
      tenant,
      role ?? '',
    )
    return useQuery({
      ...options,
      queryKey: tenantQueryKey(tenant, ...options.queryKey),
      select: (page) => page.grants,
      enabled: tenant !== '' && role !== undefined && role !== '',
    })
  },

  useTenantRoleBindings: (tenant, role, params) => {
    const options = getAuthzTenantadminRolesBindingsListQueryOptions(
      tenant,
      role ?? '',
      params,
    )
    return useQuery({
      ...options,
      queryKey: tenantQueryKey(tenant, ...options.queryKey),
      enabled: tenant !== '' && role !== undefined && role !== '',
    })
  },

  useTenantBindings: (tenant) => {
    const options = getAuthzTenantadminBindingsListQueryOptions(tenant)
    return useQuery({
      ...options,
      queryKey: tenantQueryKey(tenant, ...options.queryKey),
      select: (page) => page.bindings,
      enabled: tenant !== '',
    })
  },

  useTenantAccountBindings: (tenant, account) => {
    const options = getAuthzTenantadminBindingsOfQueryOptions(
      tenant,
      account ?? '',
    )
    return useQuery({
      ...options,
      queryKey: tenantQueryKey(tenant, ...options.queryKey),
      select: (page) => page.bindings,
      enabled: tenant !== '' && account !== undefined && account !== '',
    })
  },

  useTenantWriteRole: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminTenantWriteRoleInput) =>
        authzTenantadminRolesPut(input.tenant, input.role, {
          description: input.description,
        }),
      onSuccess: (_answer, input) => {
        invalidateTenantRoles(queryClient, input.tenant)
      },
    })
  },

  useTenantRemoveRole: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminTenantRoleRef) =>
        authzTenantadminRolesDelete(input.tenant, input.role),
      onSuccess: (_answer, input) => {
        // Removing a role revokes its grants and bindings with it, so the
        // tenant's binding reads and the role's grants are stale too.
        invalidateTenantRoles(queryClient, input.tenant)
        void queryClient.invalidateQueries({
          queryKey: tenantQueryKey(
            input.tenant,
            ...getAuthzTenantadminBindingsListQueryKey(input.tenant),
          ),
        })
        void queryClient.invalidateQueries({
          queryKey: tenantQueryKey(
            input.tenant,
            ...getAuthzTenantadminGrantsListQueryKey(
              input.tenant,
              input.role,
            ),
          ),
        })
        // The role's holder list is part of the role's usage; removing the
        // role leaves it meaningless. The bare path is the prefix of every
        // page of that read (the page's cursor is the key's trailing part).
        void queryClient.invalidateQueries({
          queryKey: tenantQueryKey(
            input.tenant,
            getAuthzTenantadminRolesBindingsListQueryKey(
              input.tenant,
              input.role,
            )[0],
          ),
        })
        invalidateTenantAccountBindings(queryClient, input.tenant)
      },
    })
  },

  useTenantGrantPermission: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminTenantGrantRef) =>
        authzTenantadminGrantsPut(
          input.tenant,
          input.role,
          input.permission,
        ),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: tenantQueryKey(
            input.tenant,
            ...getAuthzTenantadminGrantsListQueryKey(
              input.tenant,
              input.role,
            ),
          ),
        })
      },
    })
  },

  useTenantRevokePermission: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminTenantGrantRef) =>
        authzTenantadminGrantsDelete(
          input.tenant,
          input.role,
          input.permission,
        ),
      onSuccess: (_answer, input) => {
        void queryClient.invalidateQueries({
          queryKey: tenantQueryKey(
            input.tenant,
            ...getAuthzTenantadminGrantsListQueryKey(
              input.tenant,
              input.role,
            ),
          ),
        })
      },
    })
  },

  useTenantBindRole: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminTenantBindingRef) =>
        authzTenantadminBindingsPut(input.tenant, input.account, input.role),
      onSuccess: (_answer, input) => {
        invalidateTenantBindings(queryClient, input.tenant, input.account)
        // The member read carries each account's role names, so a binding
        // change leaves the rows stale even though the membership itself did
        // not change.
        invalidateTenantMembers(queryClient, input.tenant)
      },
    })
  },

  useTenantUnbindRole: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: AdminTenantBindingRef) =>
        authzTenantadminBindingsDelete(
          input.tenant,
          input.account,
          input.role,
        ),
      onSuccess: (_answer, input) => {
        invalidateTenantBindings(queryClient, input.tenant, input.account)
        // The role column is derived from these bindings; refresh it with the
        // binding reads so the panel and the row never disagree.
        invalidateTenantMembers(queryClient, input.tenant)
      },
    })
  },
}

/**
 * Invalidates a tenant's invitation list. The generated bare key is wrapped
 * in the tenant prefix the read uses, so the invalidation cannot drift from
 * it and a write touches only the tenant it names.
 */
function invalidateTenantInvitations(
  queryClient: ReturnType<typeof useQueryClient>,
  tenant: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: tenantQueryKey(tenant, ...getAuthnInviteListQueryKey(tenant)),
  })
}

/** Invalidates a tenant's whole membership list. */
function invalidateTenantMembers(
  queryClient: ReturnType<typeof useQueryClient>,
  tenant: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: tenantQueryKey(
      tenant,
      ...getAuthnAdminMembersListQueryKey(tenant),
    ),
  })
}

/** Invalidates a tenant's whole SSO provider list. */
function invalidateTenantSsoProviderList(
  queryClient: ReturnType<typeof useQueryClient>,
  tenant: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: tenantQueryKey(
      tenant,
      ...getAuthnAdminSsoProvidersListQueryKey(tenant),
    ),
  })
}

/** Invalidates a provider's detail read and its tenant's provider list: an
 * SSO write changes the provider's own row and its list entry together. */
function invalidateTenantSsoProvider(
  queryClient: ReturnType<typeof useQueryClient>,
  tenant: string,
  id: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: tenantQueryKey(
      tenant,
      ...getAuthnAdminSsoProvidersGetQueryKey(tenant, id),
    ),
  })
  invalidateTenantSsoProviderList(queryClient, tenant)
}

/**
 * Invalidates the tenant profile read a profile save leaves stale. The
 * generated bare key is wrapped in the tenant prefix the read uses, so the
 * invalidation cannot drift from it.
 */
function invalidateTenantProfile(
  queryClient: ReturnType<typeof useQueryClient>,
  tenant: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: tenantQueryKey(
      tenant,
      ...getTenantProfileGetQueryKey(tenant),
    ),
  })
}

/** Invalidates the organization's pending owner-transfer read. */
function invalidateTenantOwnerPending(
  queryClient: ReturnType<typeof useQueryClient>,
  tenant: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: tenantQueryKey(
      tenant,
      ...getTenantAdminOwnerPendingTransferQueryKey(tenant),
    ),
  })
}

/**
 * Reflects a confirmed deletion into the cached profile of a tenant the
 * caller has just left. The read cannot be refreshed -- the server now
 * refuses it -- so the fact the write established is written over the cached
 * answer; a page that stays (a host with no departure action) renders it, and
 * the host's tenant switch discards the departed tenant's whole subtree.
 */
function reflectTenantProfile(
  queryClient: ReturnType<typeof useQueryClient>,
  tenant: string,
  update: (profile: AdminTenantProfile) => AdminTenantProfile,
): void {
  queryClient.setQueriesData<{ readonly profile: AdminTenantProfile }>(
    {
      queryKey: tenantQueryKey(
        tenant,
        ...getTenantProfileGetQueryKey(tenant),
      ),
    },
    (envelope) =>
      envelope === undefined
        ? envelope
        : { ...envelope, profile: update(envelope.profile) },
  )
}

function invalidateTenantSettings(
  queryClient: ReturnType<typeof useQueryClient>,
  tenant: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: tenantQueryKey(
      tenant,
      ...getSettingsTenantadminSettingsListQueryKey(tenant),
    ),
  })
}

/** Invalidates a tenant's whole role list. */
function invalidateTenantRoles(
  queryClient: ReturnType<typeof useQueryClient>,
  tenant: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: tenantQueryKey(
      tenant,
      ...getAuthzTenantadminRolesListQueryKey(tenant),
    ),
  })
}

/** Invalidates both binding readers a tenant binding write leaves stale:
 * the tenant's whole list and the written account's own held bindings. */
function invalidateTenantBindings(
  queryClient: ReturnType<typeof useQueryClient>,
  tenant: string,
  account: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: tenantQueryKey(
      tenant,
      ...getAuthzTenantadminBindingsListQueryKey(tenant),
    ),
  })
  void queryClient.invalidateQueries({
    queryKey: tenantQueryKey(
      tenant,
      ...getAuthzTenantadminBindingsOfQueryKey(tenant, account),
    ),
  })
}

/** Invalidates every account-binding read under one tenant: a role removal
 * names no account, so the reads are matched by the tenant's generated
 * path rather than by account. */
function invalidateTenantAccountBindings(
  queryClient: ReturnType<typeof useQueryClient>,
  tenant: string,
): void {
  const prefix = getAuthzTenantadminBindingsOfQueryKey(tenant, '')[0]
  void queryClient.invalidateQueries({
    predicate: (query) =>
      query.queryKey[0] === tenant &&
      typeof query.queryKey[1] === 'string' &&
      query.queryKey[1].startsWith(prefix),
  })
}

const OrganizationAdminOperationsContext =
  createContext<OrganizationAdminOperations | null>(null)

export interface OrganizationAdminOperationsProviderProps {
  /** The operations bundle every organization page under this provider reads
   * through. */
  readonly operations: OrganizationAdminOperations
  readonly children: ReactNode
}

/**
 * Publishes the organization operations bundle to the pages below it. The
 * organization provider mounts one around its children; a host composing the
 * organization pages without the full provider mounts its own.
 */
export function OrganizationAdminOperationsProvider({
  operations,
  children,
}: OrganizationAdminOperationsProviderProps) {
  return (
    <OrganizationAdminOperationsContext.Provider value={operations}>
      {children}
    </OrganizationAdminOperationsContext.Provider>
  )
}

/**
 * The organization operations bundle the pages read through: the enclosing
 * provider's when one is mounted, the shipped `organizationAdminOperations`
 * otherwise.
 */
export function useOrganizationAdminOperations(): OrganizationAdminOperations {
  return useContext(OrganizationAdminOperationsContext) ?? organizationAdminOperations
}
