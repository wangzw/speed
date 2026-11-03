/**
 * The workspace administration surface's operations bundle: the
 * tenant-scoped reads and writes the workspace pages run through.
 *
 * Every member here addresses `/api/v1/tenants/{tenant}/...`; the tenant is
 * an argument the caller supplies from the current tenant the host handed
 * the workspace provider. The bundle holds no platform member and never
 * addresses `/api/v1/system`, so a workspace page cannot reach a platform
 * asset even by mistake. It is a subset of the console bundle's generated
 * bindings -- the subscription where the two surfaces overlap -- rather
 * than a second binding of the same operations.
 *
 * The tenant settings and tenant role/permission members are not part of
 * the bundle yet: those endpoints arrive with their own batches, and the
 * workspace declarations that need them stay out until then.
 */

import { adminOperations } from './operations.js'
import type { AdminOperations } from './operations.js'

/**
 * The tenant-scoped operations the workspace pages read and write through:
 * invitations and membership, and the tenant's SSO providers.
 */
export type WorkspaceAdminOperations = Pick<
  AdminOperations,
  | 'useInvitations'
  | 'useIssueInvitation'
  | 'useRevokeInvitation'
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
>

/**
 * The shipped workspace bundle: the generated tenant-scoped bindings the
 * console bundle already carries, picked into the narrower shape. A host
 * that needs a different wiring mounts its own bundle on
 * `WorkspaceAdminProvider`'s `operations` prop instead.
 */
export const workspaceAdminOperations: WorkspaceAdminOperations = {
  useInvitations: adminOperations.useInvitations,
  useIssueInvitation: adminOperations.useIssueInvitation,
  useRevokeInvitation: adminOperations.useRevokeInvitation,
  useMembers: adminOperations.useMembers,
  useRemoveMember: adminOperations.useRemoveMember,
  useWriteMember: adminOperations.useWriteMember,
  useSetMemberSsoExempt: adminOperations.useSetMemberSsoExempt,
  useSsoProviders: adminOperations.useSsoProviders,
  useSsoProvider: adminOperations.useSsoProvider,
  useDeleteSsoProvider: adminOperations.useDeleteSsoProvider,
  useRemoveSsoDomain: adminOperations.useRemoveSsoDomain,
  useVerifySsoDomain: adminOperations.useVerifySsoDomain,
  useRefreshSsoMetadata: adminOperations.useRefreshSsoMetadata,
  useReverifySsoDomains: adminOperations.useReverifySsoDomains,
  useCreateSsoProvider: adminOperations.useCreateSsoProvider,
  useUpdateSsoProvider: adminOperations.useUpdateSsoProvider,
  useClaimSsoDomain: adminOperations.useClaimSsoDomain,
}
