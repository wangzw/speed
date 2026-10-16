/**
 * @speed/auth-core -- the browser session lifecycle as a memory-only
 * state machine over the generated authn flow surface of
 * @speed/api-sdk.
 *
 * createAuthSession wires the generated flows (login start / answer /
 * current / abandon, the authenticated-purpose start for link, step-up
 * and enroll, the methods listing, the email-domain SSO discovery,
 * logout and the token endpoint a refresh uses) into one observable
 * session: an AccessTokenStore holds
 * the access token (the bridge into @speed/api-client, which reads the
 * same store on every send), the refresh token lives only inside the
 * session closure, and subscribers observe the AuthSnapshot
 * transitions. Only a token-issuing completion -- a login completion
 * (state 'authenticated') or a step-up completion (state 'elevated')
 * -- changes the session. The failure contract is ApiError-based: a
 * request that fails rejects raw (tell apart with isApiError) and
 * changes nothing; refresh() is the silent path -- false for a refused
 * token (the session signs out), a raw rethrow for a transport/server
 * failure. A token-issuing commit that lost a concurrent race rejects
 * with OperationSupersededError (tell apart with
 * isOperationSuperseded) rather than silently resolving with the
 * winner's snapshot -- see session.ts.
 *
 * The authn fragments declare their 2xx response schemas, so the
 * response models (AuthnFlowState, AuthnStep, AuthnField,
 * AuthnFactorOption, AuthnPrincipal, AuthnMethodList, AuthnMethodDescriptor,
 * AuthnSsoProvider) are the generated types; this package guards each
 * body at runtime all the same, refusing a contract-violating 2xx as
 * client.protocol before any state change.
 *
 * The hooks (hooks.ts) read one host-attached session: attachSession
 * binds it once at bootstrap (last bind wins), useAuthState exposes the
 * snapshot and usePermission a fail-closed set lookup over the
 * host-attached per-domain permission lists (a UX affordance, never a
 * security boundary). The tenant is not the session's -- the server
 * resolves it per request -- so the host mounts its current tenant on
 * TenantProvider and useCurrentTenant reads it back. react is a peer
 * dependency of the main entry: any host of the hooks already renders
 * React.
 *
 * The tenant-scoped query-key convention (query-key.ts) also lives
 * here: tenantQueryKey prepends the tenant id as a key's outermost
 * segment, useTenantQueryKey derives the key and its enablement from
 * one hook call, and evictTenantQueries removes a departing tenant's
 * whole subtree once a switch commits. It belongs here because the
 * tenant is this package's own fact and api-client excludes the tenant
 * from its contract; @tanstack/react-query is a peer, so only a host
 * that already holds a QueryClient carries it (the factory calls its
 * removeQueries, never constructs one).
 *
 * No storage is written anywhere: the authn token responses carry the
 * refresh token in the body (the browser path delivers
 * `delivery: 'token'`; no refresh cookie exists), so the held token
 * survives only inside the session closure, and persistence of the
 * session across page loads is out of scope here (see README's Known
 * limitations).
 */

export {
  createAuthSession,
  isOperationSuperseded,
  OperationSupersededError,
} from './session.js'
export {
  attachSession,
  TenantProvider,
  useAuthState,
  useCurrentTenant,
  usePermission,
} from './hooks.js'
export {
  evictTenantQueries,
  tenantQueryKey,
  useTenantQueryKey,
} from './query-key.js'
export type { TenantQueryKey } from './query-key.js'
export type {
  AuthDomain,
  AuthPermissionSets,
  AuthSession,
  AuthSessionListener,
  AuthSnapshot,
  AuthnFactorOption,
  AuthnField,
  AuthnFlowState,
  AuthnIdentifier,
  AuthnLink,
  AuthnMethodDescriptor,
  AuthnMethodList,
  AuthnPrincipal,
  AuthnSsoProvider,
  AuthnStep,
} from './session.js'
