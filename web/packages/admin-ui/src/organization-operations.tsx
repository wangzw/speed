/**
 * The organization surface's operations: tenant-scoped reads and writes keyed by
 * `tenantQueryKey`; no member addresses `/api/v1/system`.
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

/** The tenant settings read's answer: the exposed items and whether they may be written. */
export type AdminTenantSettings = SettingsTenantAdminSettings

/** One declared setting as the tenant layer reports it, with both layers' values. */
export type AdminTenantSetting = SettingsTenantAdminSetting

/** The layer a reported value came from, as the server reports it. */
export type AdminTenantSettingSource = SettingsTenantAdminSettingSource

/** One tenant's organization profile: identity, four descriptive fields and owner. */
export type AdminTenantProfile = TenantProfileView

/** The profile write's target and draft. */
export interface AdminTenantProfileWriteInput {
  readonly tenant: string
  readonly fields: TenantProfileFields
}

/** One owner-transfer request, carrying the target's names but never its id. */
export type AdminTenantTransfer = TenantTransferView

/** POST .../owner/requests body: the active member asked to take ownership. */
export interface AdminTenantOwnerRequestInput {
  readonly tenant: string
  readonly account: string
}

/** DELETE .../owner/requests/{request} target. */
export interface AdminTenantOwnerRequestRef {
  readonly tenant: string
  readonly request: string
}

/** DELETE /api/v1/tenants/{tenant} target. */
export interface AdminTenantRef {
  readonly tenant: string
}

/** One tenant role, structurally the console's role row. */
export type AdminTenantRole = AuthzTenantAdminRoleView

/** One governed permission (GET .../tenants/{tenant}/permissions). */
export type AdminTenantPermission = AuthzTenantAdminPermissionView

/** One permission grant of a tenant role (GET .../roles/{role}/grants). */
export type AdminTenantGrant = AuthzTenantAdminGrantView

/** One role binding within a tenant (GET .../tenants/{tenant}/bindings). */
export type AdminTenantBinding = AuthzTenantAdminBindingView

/** One account holding a role, with its resolved identity and membership state. */
export type AdminTenantRoleBinding = AuthzTenantAdminRoleBindingView

/** One page of a role's holders together with the next-page cursor. */
export type AdminTenantRoleBindingPage = AuthzTenantadminRolesBindingsList200

/** The role-usage read's cursor parameters. */
export type AdminTenantRoleBindingsQuery =
  AuthzTenantadminRolesBindingsListParams

/** PUT .../tenants/{tenant}/settings/{namespace}/{key} body. */
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
 * The write-role body. The role name is the key bindings hold and is never
 * renamed, so a write names an existing role or creates one.
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
 * invitations and membership, SSO, tenant settings and tenant authorization.
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
  /** GET .../tenants/{tenant}/profile -- the organization's profile. */
  readonly useTenantProfile: (
    tenant: string,
  ) => UseQueryResult<AdminTenantProfile, unknown>
  /** PATCH .../tenants/{tenant}/profile -- write the four descriptive fields. */
  readonly useTenantProfileWrite: () => UseMutationResult<
    void,
    unknown,
    AdminTenantProfileWriteInput
  >

  /** GET .../owner/requests/pending -- the pending request or null; `enabled` holds it for a non-owner. */
  readonly useTenantOwnerPending: (
    tenant: string,
    enabled?: boolean,
  ) => UseQueryResult<AdminTenantTransfer | null, unknown>
  /** POST .../owner/requests -- ask an active member to take ownership (unchanged until confirmed). */
  readonly useTenantOwnerRequest: () => UseMutationResult<
    AdminTenantTransfer,
    unknown,
    AdminTenantOwnerRequestInput
  >
  /** DELETE .../owner/requests/{request} -- cancel the pending request. */
  readonly useTenantOwnerCancel: () => UseMutationResult<
    void,
    unknown,
    AdminTenantOwnerRequestRef
  >
  /** DELETE .../tenants/{tenant} -- tombstone the organization; terminal, with no restore route. */
  readonly useTenantOwnerDelete: () => UseMutationResult<
    void,
    unknown,
    AdminTenantRef
  >

  /** GET .../tenants/{tenant}/settings -- every exposed item and whether a write is accepted. */
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

  /** GET .../tenants/{tenant}/roles -- a tenant's roles. */
  readonly useTenantRoles: (
    tenant: string,
  ) => UseQueryResult<readonly AdminTenantRole[], unknown>
  /** GET .../tenants/{tenant}/permissions -- the governed permission set. */
  readonly useTenantPermissions: (
    tenant: string,
  ) => UseQueryResult<readonly AdminTenantPermission[], unknown>
  /** GET .../roles/{role}/grants -- a role's grants (disabled while no role is chosen). */
  readonly useTenantGrants: (
    tenant: string,
    role: string | undefined,
  ) => UseQueryResult<readonly AdminTenantGrant[], unknown>
  /** GET .../roles/{role}/bindings -- one page of holders (disabled while no role is chosen). */
  readonly useTenantRoleBindings: (
    tenant: string,
    role: string | undefined,
    params?: AdminTenantRoleBindingsQuery,
  ) => UseQueryResult<AdminTenantRoleBindingPage, unknown>
  /** GET .../tenants/{tenant}/bindings -- a tenant's bindings. */
  readonly useTenantBindings: (
    tenant: string,
  ) => UseQueryResult<readonly AdminTenantBinding[], unknown>
  /** GET .../bindings/{account} -- an account's bindings (disabled while no account is chosen). */
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
  /** DELETE .../roles/{role} -- remove a role and its grants and bindings with it. */
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
 * The shipped organization bundle: the shared operations re-bound over the
 * tenant-scoped key convention, plus the tenant management surface's own.
 */
export const organizationAdminOperations: OrganizationAdminOperations = {
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
      // A row another operator already moved answers 409 invitation-terminal;
      // invalidate so the server's judgement reaches the row.
      onError: (error, input) => {
        if (errorCodeOf(error) === INVITATION_TERMINAL_CODE) {
          invalidateTenantInvitations(queryClient, input.tenant)
        }
      },
    })
  },

  useResendInvitation: () =>
    useResendInvitationMutation(invalidateTenantInvitations),

  useMembers: (tenant, params) => {
    const options = getAuthnAdminMembersListQueryOptions(tenant, params)
    return useQuery({
      ...options,
      queryKey: tenantQueryKey(tenant, ...options.queryKey),
      // The envelope carries the next cursor; the previous page stays on screen
      // while a search or page change runs, so the table does not collapse.
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
        // The request is recorded but ownership has not changed; the pending
        // read and the dialog's target list are re-read.
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
        // A cancelled request leaves ownership untouched; re-read pending and profile.
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
        // The caller has left: its reads can only be refused, so the tombstone
        // is written over the cached profile and the settled pending read is dropped.
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
        // Removing a role revokes its grants and bindings, so those reads and
        // the tenant's bindings are stale too.
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
        // The role's holder list is meaningless after removal; the bare path is
        // the prefix of every page of that read (the cursor is the key's tail).
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
        // The member read carries each account's role names, so a binding change
        // leaves the rows stale even though the membership did not change.
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
        // The role column derives from these bindings; refresh it with them.
        invalidateTenantMembers(queryClient, input.tenant)
      },
    })
  },
}

function invalidateTenantInvitations(
  queryClient: ReturnType<typeof useQueryClient>,
  tenant: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: tenantQueryKey(tenant, ...getAuthnInviteListQueryKey(tenant)),
  })
}

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
 * Writes a confirmed deletion over the cached profile of a tenant the caller has
 * left: the read would now be refused, so the established fact replaces it.
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

/** Invalidates both binding readers a write leaves stale. */
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

/** Invalidates every account-binding read under a tenant, matched by path. */
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

/** The props of the organization operations provider. */
export interface OrganizationAdminOperationsProviderProps {
  /** The operations bundle every organization page under this provider reads through. */
  readonly operations: OrganizationAdminOperations
  readonly children: ReactNode
}

/** Publishes the organization operations bundle to the pages below it. */
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

/** The bundle: the provider's when mounted, `organizationAdminOperations` otherwise. */
export function useOrganizationAdminOperations(): OrganizationAdminOperations {
  return useContext(OrganizationAdminOperationsContext) ?? organizationAdminOperations
}
