/**
 * @speed/auth-core -- the browser session lifecycle as a state machine
 * over the generated authn flow surface of @speed/api-sdk, carried by
 * the server's session cookie.
 *
 * createAuthSession wires the generated flows (login start / answer /
 * current / abandon, the authenticated-purpose start for link, step-up
 * and enroll, the anonymous registration operation, the methods listing,
 * the email-domain SSO discovery, the session read that restores a page
 * load and the logout that ends it) into one observable session. The session is delivered as a cookie:
 * the login flow is started with `delivery: 'session'`, so a completion
 * answers `{state: 'authenticated', session: {id, expires_at}}` and the
 * server sets its httpOnly cookie on the same response, which the
 * browser attaches to every same-origin request by itself. A store-less
 * session (the platform's own composition) therefore holds no credential
 * at all; restore() reads GET /api/v1/auth/session to re-establish the snapshot
 * from that cookie (what keeps a refresh logged in, and what ends the
 * local session when the cookie no longer holds). A host that passes an
 * AccessTokenStore keeps the token path: a `delivery: 'token'`
 * completion writes the access token into it and holds the minted
 * refresh token in the closure, refreshed through refresh(). Subscribers
 * observe the AuthSnapshot transitions. Only a session completion -- a
 * login completion (state 'authenticated') or a step-up completion
 * (state 'authenticated' for the cookie session, 'elevated' for the
 * token one) -- changes the session. The failure contract is
 * ApiError-based: a request that fails rejects raw (tell apart with
 * isApiError) and changes nothing; restore() and refresh() are the silent
 * paths -- false for a session that is no longer signed in (the session
 * goes anonymous), a raw rethrow for a transport/server failure. A
 * session-establishing commit that lost a concurrent race rejects with
 * OperationSupersededError (tell apart with isOperationSuperseded) rather
 * than silently resolving with the winner's snapshot -- see session.ts.
 *
 * The authn fragments declare their 2xx response schemas, so the
 * response models (AuthnFlowState, AuthnStep, AuthnField,
 * AuthnFactorOption, AuthnPrincipal, AuthnMethodList,
 * AuthnMethodDescriptor, AuthnSsoProvider) are the generated types; this
 * package guards each body at runtime all the same, refusing a
 * contract-violating 2xx as client.protocol before any state change.
 *
 * The hooks (hooks.ts) read one host-attached session: attachSession
 * binds it once at bootstrap (last bind wins), useAuthState exposes the
 * snapshot and usePermission a fail-closed set lookup over the
 * host-attached per-domain permission lists (a UX affordance, never a
 * security boundary). The snapshot also carries the feature state the
 * session read from the process's GET /api/v1/features document; useFeature is
 * a fail-closed membership lookup over it, useFeatureState hands the
 * state itself, and featurePresence maps it onto the route layer's
 * presence shape. The tenant is not the session's -- the server
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
 * tenant is this package's own fact and api-client only broadcasts the
 * host's tenant as transport, inferring and storing none;
 * @tanstack/react-query is a peer, so only a host that already holds a
 * QueryClient carries it (the factory calls its removeQueries, never
 * constructs one).
 *
 * Two smaller seams live here too: useStepUpGate remembers a
 * step-up-refused action and replays exactly it once the challenge is
 * won (step-up-gate.ts), and isTenantAdmissionRefusal tells the
 * admission layer's bare 404 from a module's own typed 404
 * (tenant-refusal.ts). The passkey ceremony driver (webauthn.ts) is the
 * package's one DOM-touching module -- it calls navigator.credentials --
 * and ships as the `./webauthn` subpath rather than through this entry,
 * so the main entry stays DOM-free.
 *
 * No storage is written anywhere: the credential is the server's
 * httpOnly session cookie, which the browser manages and scripts cannot
 * read; this package holds nothing of its own between requests.
 */

export {
  createAuthSession,
  isOperationSuperseded,
  OperationSupersededError,
} from './session.js'
export {
  attachSession,
  featurePresence,
  TenantProvider,
  useAuthState,
  useCurrentTenant,
  useFeature,
  useFeatureState,
  usePermission,
} from './hooks.js'
export {
  evictTenantQueries,
  tenantQueryKey,
  useTenantQueryKey,
} from './query-key.js'
export type { TenantQueryKey } from './query-key.js'
export { useStepUp } from './step-up.js'
export type {
  AuthnFlowAnswer,
  StepUpPhase,
  StepUpState,
} from './step-up.js'
export { useStepUpGate, stepUpReasonOf } from './step-up-gate.js'
export type { StepUpGate, StepUpReason } from './step-up-gate.js'
export {
  humanGateNeedsProof,
  powProofFromChallenge,
  requestPowProof,
  useHumanVerification,
} from './human.js'
export type {
  HumanVerification,
  HumanVerificationStatus,
  UseHumanVerificationOptions,
} from './human.js'
export { POW_HASH, solvePowChallenge } from './pow.js'
export { isTenantAdmissionRefusal } from './tenant-refusal.js'
export type {
  AuthDomain,
  AuthPermissionSets,
  AuthSession,
  AuthSessionListener,
  AuthSnapshot,
  AuthnFactorOption,
  AuthnField,
  AuthnFlowState,
  AuthnHumanGate,
  AuthnIdentifier,
  AuthnLink,
  AuthnLoginRegisterBody,
  AuthnLoginRegistration,
  AuthnMethodDescriptor,
  AuthnMethodList,
  AuthnPrincipal,
  AuthnSessionView,
  AuthnSsoProvider,
  AuthnStep,
  FeatureState,
  PasswordResetChannel,
} from './session.js'
