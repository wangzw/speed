/**
 * The browser session state machine: createAuthSession wires the
 * generated authn flow surface of @speed/api-sdk (the login / link /
 * step-up / enroll flow operations, the anonymous registration
 * operation, the methods listing, the session read that restores a page
 * load, logout, and the token endpoint a token-delivered refresh uses)
 * into one observable session.
 *
 * The session is delivered as a cookie. The login flow is started with
 * `delivery: 'session'`, so a completion answers `{state:
 * 'authenticated', session: {id, expires_at}}` and the server sets its
 * httpOnly session cookie on the same response; the browser then
 * attaches that cookie to every same-origin request on its own, so this
 * package holds no credential for the cookie path -- there is no access
 * token to keep in memory and no refresh token to hold in a closure.
 * Every request the generated operations make rides the ambient cookie
 * through @speed/api-client, whose configured credential mode
 * (`credentials`) sends it. Because the cookie outlives the page, a
 * reload no longer signs the user out: restore() reads GET /api/v1/auth/session,
 * and a session the cookie still names re-establishes the snapshot -- the
 * point that keeps a refresh logged in. restore() is also the client's
 * silent-401 leg: @speed/api-client's session hook runs it when a
 * credentialed request is refused, and a cookie that no longer holds
 * clears the local snapshot (the session is over) so the shells fall
 * back to sign-in.
 *
 * A token-delivered completion is still accepted, for a host that starts
 * its flow with `delivery: 'token'` and passes an AccessTokenStore: the
 * access token lands in that store (the same store @speed/api-client
 * reads on every send) and the minted refresh token lives only in this
 * closure, so such a host gets the token-refresh path
 * (refresh()). The platform's own composition (@speed/product-shell)
 * does not wire a store: it forces the cookie path, so this package
 * keeps no credential at all there. `delivery` is forced to 'session' on
 * every flow this package starts, so the browser path is always the
 * cookie one whatever a view asked for. A host that wants the token path
 * starts the login flow itself with the generated
 * `authnLoginStart({ delivery: 'token' })` operation and hands the
 * answers to answerFlow; that session is not rebuilt by restore() and is
 * lost on reload.
 *
 * The session cookie is SameSite=Lax (an operator may tighten it to
 * Strict; `none` is refused by the server), so a browser deployment's
 * frontend and API must be same-site: a cross-site frontend cannot carry
 * the cookie on its requests.
 *
 * Login is a server-side flow the client advances, not one request:
 * POST /api/v1/auth/flows starts `purpose: 'login'`, POST /api/v1/auth/flows/answer
 * answers the current step, GET /api/v1/auth/flows/current resumes after a
 * page refresh and DELETE /api/v1/auth/flows/current abandons it. The flow is
 * bound with a cookie by default (binding: 'cookie'), so the frontend
 * never reads a flow token; POST /api/v1/auth/flows/authenticated starts the
 * same machine on the identity already signed in for `purpose: 'link'`,
 * `'step-up'` or `'enroll'`. Those operations never change the session
 * -- they only hand the step back to the view (see the interface docs).
 * Only `answerFlow` commits, and only when a step settles the session: a
 * `purpose: 'login'` completion (state 'authenticated') opens the
 * session, a `step-up` completion opens it too with the same session
 * continuing -- the cookie rotated for a cookie session, a fresh access
 * token (state 'elevated') for a token session -- and a `link`/`enroll`
 * completion (state 'linked'/'enrolled') does not touch the login state.
 * A cookie login and a cookie step-up carry the same session completion
 * shape, so the package tells them apart by the purpose of the flow it
 * started, not by the body.
 *
 * The response shapes come from the generated surface: the authn
 * fragments declare their 2xx schemas, so the response models below
 * (`AuthnFlowState`, `AuthnStep`, `AuthnField`, `AuthnFactorOption`,
 * `AuthnPrincipal`, `AuthnMethodList`, `AuthnSessionView`) are the
 * generated types (or a client-side narrowing of them), not hand-written
 * copies. The package still guards every body-carrying 2xx at runtime,
 * because the generated types erase at the seam: a body that is not an
 * object, or that is missing a required field, is refused as a
 * `status: 200`, `code: client.protocol` ApiError -- a contract
 * violating 2xx is never a successful login. The generated seam sets no
 * `requireJsonBody` (see the @speed/api-sdk runtime.ts doc), so an empty
 * 2xx resolves as undefined through it and the guard must catch that
 * too, before the first property access (a native TypeError is not an
 * ApiError and would escape the whole error contract).
 *
 * The failure contract: a request that fails rejects the raw ApiError
 * (distinguishable with isApiError) and changes nothing. A
 * session-establishing commit -- a login completion or a step-up
 * completion -- that lost a concurrent generation race rejects with
 * OperationSupersededError instead of silently resolving with the
 * winner's snapshot (see the class's doc comment). `restore()` is the
 * silent path for a cookie session: it resolves true when the cookie
 * still names a session (the snapshot is authenticated afterwards),
 * false when nobody is signed in (the session signs out locally), and
 * rethrows raw only for a transport or server-side failure (the local
 * snapshot stands). `refresh()` is the silent path for a token
 * session: it resolves true when the store holds a fresh token
 * afterwards, false when there is nothing to refresh or the held token
 * was refused (the session signs out locally), and rethrows raw only for
 * a transport or server-side failure (the held tokens stand).
 *
 * Permission sets: the snapshot additionally carries
 * setPermissionSet(domain, list) data -- host-attached lists. The
 * session never evaluates a list; it only applies the survival rules
 * when a principal change commits: a silent restore or refresh or a
 * step-up (the same session continuing) keeps both lists, `link` and
 * `enroll` keep them (they do not change the login state), and a login
 * -- even by the same account -- or an anonymous transition clears
 * both: a login opens a fresh server session whose lists the host has
 * not fetched yet. A failed operation changes nothing, lists included.
 *
 * Impersonation: the snapshot carries `impersonating`, read from
 * GET /api/v1/auth/session by restore(). Only restore() writes it -- a login
 * completion and every anonymous snapshot carry false, and a step-up,
 * which continues the same session, keeps the value it read. This
 * package is never told that an impersonation started or ended: the host
 * issues that write itself and calls restore() once it settles, so the
 * field always follows the server's answer. The snapshot carries no
 * initiator identity; during an impersonation the principal is the
 * impersonated account.
 *
 * Identity transitions: beginIdentityTransition() opens a short window
 * in which the credential the session rides is being replaced
 * server-side. The case it exists for is an impersonation start, which
 * rotates the session cookie: a request issued under the old cookie can
 * answer 401 before the start write's Set-Cookie reaches the browser,
 * and the client's silent-401 leg then runs restore() with the cookie
 * the server has already detached, so the read answers anonymous even
 * though the session is not over -- a spurious sign-out in the middle
 * of the transition. While a transition is open -- and for a restore
 * that was already in flight when one opened -- an anonymous answer is
 * discarded: restore() resolves false without changing the snapshot,
 * the caller's request keeps its 401, and the next read after the
 * release settles the true state. A genuinely expired session is
 * therefore settled by that next read, not by the read issued under the
 * outgoing credential. The guard is identity-generic; this package still
 * never learns that an impersonation started or ended.
 *
 * Feature state: the snapshot also carries the feature document the
 * process serves (GET /api/v1/features), as a closed `FeatureState` set. A
 * login completion and an anonymous-start restore read it in parallel
 * with GET /api/v1/auth/me and commit the two together; a failed feature read
 * commits the session with `failed` rather than failing the login. The
 * authenticated restore, refresh, step-up, link and enroll paths keep
 * the state without a request, and logout (or a locally cleared
 * session) resets it to `none`. retryFeatures() is the explicit retry
 * for a failed read. The session never interprets a name; membership is
 * decided by the hooks and the shells.
 *
 * The failure semantics of `logout` deliberately diverge from the
 * above: a logout whose own request is refused still clears the local
 * session and rejects the raw ApiError, because a session it could no
 * longer revoke is no longer trustworthy locally either.
 */

import { ApiError, ERROR_CODE_PROTOCOL, isApiError } from '@speed/api-client'
import type { AccessTokenStore, RequestFn } from '@speed/api-client'
import {
  authnLoginAbandon,
  authnLoginAnswer,
  authnLoginCurrent,
  authnLoginMethods,
  authnLoginRegister,
  authnLoginStart,
  authnLoginStartAuthenticated,
  authnProfileGet,
  authnSessionGet,
  authnSessionLogout,
  authnSsoDiscover,
  authnTokenIssue,
  featureBoardList,
} from '@speed/api-sdk'
import type {
  AuthnLoginActionResult,
  AuthnLoginAnswerBody,
  AuthnLoginFactor,
  AuthnLoginField,
  AuthnLoginFlow,
  AuthnLoginMethods,
  AuthnLoginRegisterBody,
  AuthnLoginRegistration,
  AuthnLoginSession,
  AuthnLoginStartAuthenticatedBody,
  AuthnLoginStartBody,
  AuthnLoginStep,
  AuthnLoginToken,
  AuthnMethodDescriptor as AuthnMethodDescriptorModel,
  AuthnProfileIdentifier,
  AuthnProfileLink,
  AuthnProfileProfile,
  AuthnProfileVerification,
  AuthnSessionView as AuthnSessionViewModel,
  AuthnSsoProvider as AuthnSsoProviderModel,
  FeatureBoardDocument,
} from '@speed/api-sdk'

export type { AuthnLoginRegisterBody, AuthnLoginRegistration } from '@speed/api-sdk'

/** The permission domain a host-attached permission set belongs to.
 * 'tenant' names the permissions the principal holds inside the tenant
 * its requests are resolved in; 'system' names platform-staff
 * permissions that are tenant-independent. */
export type AuthDomain = 'tenant' | 'system'

/** The host-attached permission lists per domain: pure data the host
 * attaches through setPermissionSet. A null list means nothing is
 * known for that domain (checks against it fail closed). The session
 * never evaluates a list -- set membership is decided by the hooks and
 * the shells, never here. */
export interface AuthPermissionSets {
  tenant: readonly string[] | null
  system: readonly string[] | null
}

/** The authenticated principal: the fields of GET /api/v1/auth/me. The
 * generated response model of the profile fragment (AuthnProfileProfile).
 * `account_id` is the account's identity; `identifiers` and `links` are
 * its verified identifiers and its external bindings, each with its own
 * identifier. */
export type AuthnPrincipal = AuthnProfileProfile

/** One verified identifier of an account (phone, email, ...): the
 * generated identifier row of GET /api/v1/auth/me. */
export type AuthnIdentifier = AuthnProfileIdentifier

/** One external binding of an account (a social or enterprise
 * provider): the generated link row of GET /api/v1/auth/me. */
export type AuthnLink = AuthnProfileLink

/** One input a challenge step asks the client for: the generated field
 * row of a login step. */
export type AuthnField = AuthnLoginField

/** One second-factor option a factor step offers: the generated factor
 * row of a login step. */
export type AuthnFactorOption = AuthnLoginFactor

/** The next step of an open flow -- what the client does next. The wire
 * carries one flat step (the generated `AuthnLoginStep`, whose `kind`
 * names which fields apply); the client narrows it by `kind` into a
 * discriminated union, so a view reads only the fields its own step
 * carries. */
export type AuthnStep =
  | (AuthnLoginStep & { kind: 'challenge'; fields: AuthnLoginField[] })
  | (AuthnLoginStep & { kind: 'redirect'; url: string; method: string })
  | (AuthnLoginStep & { kind: 'factor'; factors: AuthnLoginFactor[] })
  | (AuthnLoginStep & { kind: 'enroll'; kinds: string[] })
  | (AuthnLoginStep & { kind: 'complete' })
  | (AuthnLoginStep & { kind: 'webauthn'; options: unknown })

/** A cookie-delivered completion: the generated `AuthnLoginSession`,
 * `{state: 'authenticated', session: {id, expires_at}}`, which the server
 * answers together with its Set-Cookie. A login and a step-up carry the
 * same shape (see the file header). */
type SessionCompletion = AuthnLoginSession & { state: 'authenticated' }

/** A token-delivered login completion: the generated `AuthnLoginToken`
 * narrowed to `authenticated` (a step-up carries `elevated`). The server
 * sets no cookie and this package holds the tokens a token host wired a
 * store for. */
type TokenCompletion = AuthnLoginToken & { state: 'authenticated' }

/** A token-delivered step-up completion: an elevated access token, no
 * refresh token. */
type ElevatedCompletion = Omit<AuthnLoginToken, 'refresh_token'> & {
  state: 'elevated'
}

/** The state of a flow as the wire carries it. Each member is the
 * generated reply of the login answer narrowed to one outcome: an open
 * flow (the server is awaiting the next answer) is the generated
 * `AuthnLoginFlow` carrying a sequence number and the next step; a
 * settled completion is a cookie-delivered session (`SessionCompletion`)
 * or a token-delivered pair (`TokenCompletion`/`ElevatedCompletion`);
 * `AuthnLoginActionResult` settles a link or enroll flow without
 * touching the login state.
 *
 * The open-flow `state` is the record's live state (pkg/authn/login
 * flow.go), the set `OPEN_FLOW_STATES` holds. The design's own
 * enumeration listed only the three the client renders a prompt for;
 * 'ready' is reachable too (a submit step that reached the ready state
 * answers `next.kind: 'complete'` while still carrying
 * `state: 'ready'`), so it is part of the set rather than a protocol
 * violation. */
export type AuthnFlowState =
  | (AuthnLoginFlow & { state: OpenFlowState; next: AuthnStep })
  | SessionCompletion
  | TokenCompletion
  | ElevatedCompletion
  | AuthnLoginActionResult

/** The login methods this deployment offers (GET /api/v1/auth/methods): the
 * generated response model. The list varies with the deployment's
 * settings and the modules in the assembly, so the frontend renders it
 * rather than hard-coding a set. */
export type AuthnMethodList = AuthnLoginMethods

/** One login method as the discovery endpoint reports it (a member of
 * GET /api/v1/auth/methods' `methods`): the generated descriptor. `kind` names
 * the rendering class (form, redirect, enterprise or webauthn), `label`
 * the method's brand name where it has one, `fields` the inputs a form
 * method's first step asks for, `opens_account` whether the method may
 * open an account for an identifier it does not yet know, and
 * `binding_providers` the provider values (or namespace prefixes) the
 * method's bindings appear under. */
export type AuthnMethodDescriptor = AuthnMethodDescriptorModel

/** One enterprise sign-in entry the email-domain discovery answers
 * (POST /api/v1/auth/sso/discover): the provider's opaque id, its display name
 * and the protocol a flow for it speaks (oidc or saml). */
export type AuthnSsoProvider = AuthnSsoProviderModel

/** Who is signed in on the session the request's cookie carries: the
 * generated response model of GET /api/v1/auth/session. `authenticated` is
 * false when nobody is signed in; the rest of the fields are then
 * absent. */
export type AuthnSessionView = AuthnSessionViewModel

/** The observable session state. Immutable by convention: hosts read
 * it, they never mutate it. */
export interface AuthSnapshot {
  state: 'anonymous' | 'authenticated'
  /** The authenticated principal, or null while anonymous. */
  principal: AuthnPrincipal | null
  /** Whether the current session carries an impersonation, read from
   * GET /api/v1/auth/session's `impersonating`. Only restore() writes it;
   * false while anonymous and on every login completion. The snapshot
   * carries no initiator identity -- during an impersonation the
   * principal is the impersonated account. A host re-reads the session
   * after it starts or ends an impersonation. */
  impersonating: boolean
  /** The host-attached permission lists per domain (see
   * setPermissionSet). Always present; a null list means no set is
   * known for that domain. Empty in both domains while anonymous. */
  permissionSets: AuthPermissionSets
  /** The feature document this process serves, as read for this
   * session. See FeatureState. */
  features: FeatureState
}

/** The closed set of feature-state values.
 *
 * `none` is the anonymous state: this package never requests
 * GET /api/v1/features while anonymous. `pending` exists only while a
 * retryFeatures() call is in flight. `failed` carries the failed read's
 * error: a failed read is neither "every feature absent" nor "every
 * feature present". `ready` carries the set of names the process
 * serves; a name the frontend does not know is accepted and ignored.
 */
export type FeatureState =
  | { readonly state: 'none' }
  | { readonly state: 'pending' }
  | { readonly state: 'failed'; readonly error: unknown }
  | { readonly state: 'ready'; readonly names: ReadonlySet<string> }

/** Receives the new snapshot on every state change. */
export type AuthSessionListener = (snapshot: AuthSnapshot) => void

/** The session handle createAuthSession returns. */
export interface AuthSession {
  /** The current snapshot; read it in render paths and in
   * useSyncExternalStore's getSnapshot. */
  getSnapshot(): AuthSnapshot
  /** Subscribe to state changes; returns the unsubscribe function. */
  subscribe(listener: AuthSessionListener): () => void
  /** The login methods this deployment offers. Reads the deployment's
   * method list; never derived from a hard-coded set. Rejects the raw
   * ApiError on failure, or a `client.protocol` error if the 2xx body
   * is not the documented shape. */
  listMethods(): Promise<AuthnMethodList>
  /** The enterprise sign-in entries an email's domain maps to, for the
   * emails-first discovery of a `enterprise` login method. Reads the
   * deployment's own directory; an email with no matching, verified
   * domain answers an empty list (never an error). Rejects the raw
   * ApiError on failure, or a `client.protocol` error if the 2xx body is
   * not the documented shape. */
  discoverSso(email: string): Promise<AuthnSsoProvider[]>
  /** Opens an account with a username, an email address and a password
   * (POST /api/v1/auth/register). Registration is not a login: it
   * establishes no session, writes no credential and no storage, and
   * leaves the snapshot, the permission sets and the feature state
   * untouched. The answer is the generated registration model (`state:
   * 'registered'`, the new `account_id`, and the email address awaiting
   * verification); a 2xx that is not that shape rejects with
   * `client.protocol` before anything happens. A refusal (a taken
   * username, closed signup, an exhausted budget, an unavailable
   * dependency, a password the policy rejects) rejects the raw ApiError.
   * The session that follows is opened by the ordinary login flow. */
  register(
    request: AuthnLoginRegisterBody,
  ): Promise<AuthnLoginRegistration>
  /** Starts a `purpose: 'login'` flow and returns its first step. The
   * flow is always started as a cookie-delivered session, whatever
   * `delivery` the request carries, so this method never yields a token
   * completion: a token host starts the flow with the generated
   * `authnLoginStart({ delivery: 'token' })` operation itself and passes
   * each answer to answerFlow. Never changes the session: the start
   * of a flow is not a session. A 2xx body that is not a valid flow
   * state rejects with `client.protocol` before anything else happens. */
  startFlow(request: AuthnLoginStartBody): Promise<AuthnFlowState>
  /** Starts a flow on the identity already signed in, for `purpose:
   * 'link'`, `'step-up'` or `'enroll'`. The `delivery` is left to the
   * caller (the account surfaces do not set one, so the server resolves
   * its default cookie session). Same contract as startFlow otherwise:
   * forwards to the server and returns the step, changing nothing. */
  startAuthenticatedFlow(
    request: AuthnLoginStartAuthenticatedBody,
  ): Promise<AuthnFlowState>
  /** Answers the current step of the flow the request is bound to and
   * returns the resulting flow state. This is the one operation that
   * commits the session: a `login` completion (state 'authenticated')
   * opens the session -- the principal is re-read from GET /api/v1/auth/me, and
   * for a token delivery the freshly minted tokens are written; a
   * `step-up` completion (state 'authenticated' for a cookie session
   * with the cookie rotated, or 'elevated' for a token session with a
   * fresh access token) keeps the principal and the permission sets with
   * the same session continuing, and a `link`/`enroll` completion
   * changes nothing. A session-establishing completion whose generation
   * moved on -- a concurrent commit beat it -- rejects with
   * OperationSupersededError (see the class). */
  answerFlow(request: AuthnLoginAnswerBody): Promise<AuthnFlowState>
  /** Reads the current step of the flow the request is bound to, so a
   * page refresh can resume it. Resolves null when there is no flow
   * (the binding is missing or the flow is done or gone -- a 401);
   * other failures reject raw. */
  readFlow(): Promise<AuthnFlowState | null>
  /** Abandons the flow the request is bound to. Idempotent: a 401 (no
   * binding, or the flow is already gone) resolves; other failures
   * reject raw. */
  abandonFlow(): Promise<void>
  /** Ends the session: the server revokes it and (for a cookie session)
   * clears the cookie, the store empties and the snapshot returns to
   * anonymous. A logout whose own request is refused still clears
   * locally and rejects the raw ApiError -- a session it could not
   * revoke is no longer trustworthy here either. A completed logout
   * wins over a refresh that resolves after it. */
  logout(): Promise<void>
  /** Restores the session the request's cookie names, by reading
   * GET /api/v1/auth/session. Resolves true when a session is signed in
   * afterwards -- the snapshot is authenticated, the principal is read
   * from GET /api/v1/auth/me, and `impersonating` is taken from the session
   * read's own field (false when the answer omits it) -- which is what
   * keeps a page reload logged in, and is also the leg
   * @speed/api-client runs on a refused
   * credentialed request. Resolves false when nobody is signed in (a
   * snapshot that was authenticated goes anonymous -- the session is
   * over -- and a 401 from the read is treated the same). A transport or
   * server-side failure rethrows the raw ApiError and leaves the local
   * snapshot in place.
   *
   * An anonymous answer is discarded -- resolves false, snapshot
   * untouched -- while an identity transition is open, or when one
   * opened after this read was issued (see beginIdentityTransition):
   * the answer describes a credential a transition is replacing, so it
   * must not end the session. The refused request that ran this restore
   * keeps its 401; the next read after the release settles the true
   * state.
   *
   * A token session is not rebuilt here. The read is answered from the
   * session cookie, and a token host sends none, so it answers nobody
   * signed in; `refresh()` is that session's silent leg, and a host that
   * must survive a reload re-establishes the token session itself.
   *
   * `issue` is the round-scoped request function @speed/api-client
   * hands its refresh hook: both of this method's reads then travel
   * through it, so a refusal among them surfaces here instead of
   * joining the refresh round that is awaiting this call -- the
   * profile read follows an await, where the client's
   * synchronous-invocation marker no longer covers it. Absent, the
   * generated operations' bound request function serves both reads. */
  restore(issue?: RequestFn): Promise<boolean>
  /** Opens an identity transition: a short window in which the
   * credential the session rides is being replaced server-side. The
   * case it exists for is an impersonation start, which rotates the
   * session cookie; a host opens it before issuing that write and
   * closes it (by calling the returned release) once the new identity
   * has settled -- or at once when the write does not land.
   *
   * While the window is open (and for a restore that was already in
   * flight when it opened), restore() discards an anonymous answer: it
   * resolves false without changing the snapshot, so the refused
   * request that ran it keeps its 401 and the host does not see a
   * spurious sign-out. That discarded answer describes the credential
   * the read carried -- one the transition is replacing -- not the
   * session that is coming. The next read after the release settles the
   * true state; a session that genuinely expired during the window is
   * settled by that next read rather than by the stale one. An
   * authenticated answer is never discarded: the host's own re-read
   * under the new credential commits normally while the window is open.
   *
   * The returned release is idempotent, and concurrent windows are
   * counted: the guard lifts only when every open release has been
   * called. The guard is identity-generic -- it carries no
   * impersonation knowledge, so a host covers any credential rotation
   * with it. */
  beginIdentityTransition(): () => void
  /** Re-reads the feature document for the current session. Only acts
   * while authenticated: the feature state turns `pending`, GET /api/v1/features
   * is read, and the result lands as `ready` or `failed` if the session
   * is still the one the read started under (same generation, still
   * authenticated, same principal account). While anonymous this
   * resolves without a request and changes nothing. Concurrent calls of
   * the same session share one in-flight read; a read begun under a
   * session that has since ended is never shared with the next one. */
  retryFeatures(): Promise<void>
  /** Silently refreshes the access token with the held refresh token
   * (a token session). Resolves true when the store holds a fresh token
   * afterwards; resolves false when there is nothing to refresh, when
   * the held token was refused (the session signs out locally), or when
   * a concurrent commit replaced the session. A transport or server-side
   * failure rethrows the raw ApiError and leaves the held tokens in
   * place. Concurrent calls presenting the same held token share one
   * in-flight request. A cookie session holds no refresh token, so this
   * resolves false without a request; its silent leg is restore(). */
  refresh(): Promise<boolean>
  /** Whether a failure a request made under this session met is one a
   * step-up would let the caller retry: an authentication refusal (an
   * ApiError whose status is 401) while the session is still
   * established. A refusal that also ended the session -- the client's
   * silent restore leg, which ran on the refused request, went
   * anonymous -- is not one: there is no session to elevate, and the
   * caller's session gate is what converges. A non-ApiError, or an
   * ApiError of another status, is never one. */
  isStepUpShortfall(error: unknown): boolean
  /** Host-attached permission list for one domain: replaces the list
   * the snapshot carries with a defensive copy (mutating the caller's
   * array later changes nothing here); passing null clears the domain.
   * Notifies subscribers like any other snapshot change. The session
   * only carries the lists and applies the survival rules (see the
   * file header); a host that attaches /me-derived lists should refetch
   * them after every login rather than reuse lists fetched under an
   * earlier commit. */
  setPermissionSet(domain: AuthDomain, perms: readonly string[] | null): void
}

/**
 * Rejects a session-establishing commit -- a login flow completion or a
 * step-up completion -- whose own request answered successfully but
 * was superseded by a concurrent commit that landed first. The request
 * itself did not fail (`isApiError(error)` is false for this) and the
 * session is not broken -- some commit's session is established and the
 * snapshot is authenticated -- but it is not necessarily THIS caller's,
 * so this caller must not treat its own 2xx as a commit signal: no
 * one-shot side effect (a post-login redirect) may fire for an identity
 * the session is not actually running under. A superseded token step-up
 * carries an extra cost the caller must hear: its completion rotated
 * the tokens and consumed, server-side, the challenge it verified, so
 * the caller must never re-offer that same second factor -- only a
 * fresh flow can retry. `snapshot` carries the session's current
 * (winning) state at the moment of rejection.
 */
export class OperationSupersededError extends Error {
  readonly snapshot: AuthSnapshot

  constructor(snapshot: AuthSnapshot) {
    super(
      'a concurrent operation committed to the session before this one settled',
    )
    this.name = 'OperationSupersededError'
    this.snapshot = snapshot
  }
}

/** Type guard for OperationSupersededError, mirroring isApiError's
 * shape so callers tell the two rejection kinds apart the same way.
 * Like isApiError it is intentionally lenient: an instanceof check
 * first, then a structural fallback -- a name, message and
 * snapshot-shaped payload -- so a rejection that crossed a package
 * boundary or a realm still reads as superseded rather than collapsing
 * into a caller's unknown-error path. */
export function isOperationSuperseded(
  value: unknown,
): value is OperationSupersededError {
  if (value instanceof OperationSupersededError) {
    return true
  }
  if (typeof value !== 'object' || value === null) {
    return false
  }
  const candidate = value as Partial<OperationSupersededError>
  return (
    candidate.name === 'OperationSupersededError' &&
    typeof candidate.message === 'string' &&
    typeof candidate.snapshot === 'object' &&
    candidate.snapshot !== null
  )
}

/** A validated token issuance: the access token, plus the minted
 * refresh token, or null when none was minted (a step-up reuses the
 * caller's). The principal is fetched separately (a login reads
 * GET /api/v1/auth/me) or carried over (a step-up keeps it): the token
 * responses do not carry it. */
interface IssuedTokens {
  accessToken: string
  refreshToken: string | null
}

/** Fail-closed response validation: a body-carrying 2xx whose body is
 * not the documented shape is a protocol violation, never a success. */
function protocolViolation(detail: string): ApiError {
  return new ApiError({
    status: 200,
    code: ERROR_CODE_PROTOCOL,
    attempts: 1,
    cause: new Error(detail),
  })
}

/** The body as a plain object, or a protocol violation. An empty 2xx
 * resolves as undefined through the generated seam (it declares no
 * document-existence expectation), so this guard runs before any
 * property access. */
function asObject(body: unknown, detail: string): Record<string, unknown> {
  if (typeof body !== 'object' || body === null || Array.isArray(body)) {
    throw protocolViolation(detail)
  }
  return body as Record<string, unknown>
}

function requireString(value: unknown, detail: string): string {
  if (typeof value !== 'string' || value === '') {
    throw protocolViolation(detail)
  }
  return value
}

function requireNumber(value: unknown, detail: string): number {
  if (typeof value !== 'number' || Number.isNaN(value)) {
    throw protocolViolation(detail)
  }
  return value
}

/** The 'Bearer' token type the authn surface declares. */
const BEARER = 'Bearer' as const

function requireBearer(value: unknown, detail: string): typeof BEARER {
  if (value !== BEARER) {
    throw protocolViolation(detail)
  }
  return BEARER
}

function optionalNumber(value: unknown): number | undefined {
  return typeof value === 'number' ? value : undefined
}

function parseIdentifier(value: unknown): AuthnIdentifier {
  const rec = asObject(value, 'profile identifier is not an object')
  return {
    id: requireString(rec.id, 'profile identifier carries no id'),
    kind: typeof rec.kind === 'string' ? rec.kind : '',
    value: typeof rec.value === 'string' ? rec.value : '',
    proven: rec.proven === true,
    verified_at: typeof rec.verified_at === 'string' ? rec.verified_at : '',
    verified_via: typeof rec.verified_via === 'string' ? rec.verified_via : '',
  }
}

/** The account's self-registration state: absent on an answer that predates
 * the field reads as `none` with no target and no instant. */
function parseVerification(value: unknown): AuthnProfileVerification {
  const rec = typeof value === 'object' && value !== null ? value : {}
  const typed = rec as Record<string, unknown>
  return {
    state: typeof typed.state === 'string' ? typed.state : 'none',
    email: typeof typed.email === 'string' ? typed.email : '',
    since: typeof typed.since === 'string' ? typed.since : '',
  }
}

function parseLink(value: unknown): AuthnLink {
  const rec = asObject(value, 'profile link is not an object')
  const link: AuthnLink = {
    id: requireString(rec.id, 'profile link carries no id'),
    provider: typeof rec.provider === 'string' ? rec.provider : '',
    created_at: typeof rec.created_at === 'string' ? rec.created_at : '',
  }
  if (typeof rec.email === 'string') {
    link.email = rec.email
  }
  if (typeof rec.last_login_at === 'string') {
    link.last_login_at = rec.last_login_at
  }
  return link
}

function parseStringMap(value: unknown): Readonly<Record<string, string>> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return {}
  }
  const out: Record<string, string> = {}
  for (const [key, entry] of Object.entries(value)) {
    if (typeof entry === 'string') {
      out[key] = entry
    }
  }
  return out
}

/** Validates the GET /api/v1/auth/me body into the generated principal model.
 * `account_id` is the required field; the rest are carried as the wire
 * sends them. */
function parsePrincipal(body: unknown): AuthnPrincipal {
  const rec = asObject(body, 'profile 2xx carries no body document')
  const identifiers = rec.identifiers
  const links = rec.links
  return {
    account_id: requireString(
      rec.account_id,
      'profile carries no account_id string',
    ),
    kind: typeof rec.kind === 'string' ? rec.kind : '',
    display_name: typeof rec.display_name === 'string' ? rec.display_name : '',
    mfa_required: rec.mfa_required === true,
    password_set: rec.password_set === true,
    created_at: typeof rec.created_at === 'string' ? rec.created_at : '',
    attributes: parseStringMap(rec.attributes),
    locale: typeof rec.locale === 'string' ? rec.locale : '',
    time_zone: typeof rec.time_zone === 'string' ? rec.time_zone : '',
    identifiers: Array.isArray(identifiers)
      ? identifiers.map((entry) => parseIdentifier(entry))
      : [],
    links: Array.isArray(links) ? links.map((entry) => parseLink(entry)) : [],
    verification: parseVerification(rec.verification),
  }
}

/** Validates the GET /api/v1/auth/session body into the generated session view.
 * `authenticated` is the one required field; the rest are carried as the
 * wire sends them (a false `authenticated` carries none of them). */
function parseSessionView(body: unknown): AuthnSessionView {
  const rec = asObject(body, 'session 2xx carries no body document')
  if (typeof rec.authenticated !== 'boolean') {
    throw protocolViolation('session 2xx carries no authenticated boolean')
  }
  const view: AuthnSessionView = { authenticated: rec.authenticated }
  if (typeof rec.session_id === 'string') {
    view.session_id = rec.session_id
  }
  if (typeof rec.account_id === 'string') {
    view.account_id = rec.account_id
  }
  if (typeof rec.actor_id === 'string') {
    view.actor_id = rec.actor_id
  }
  if (typeof rec.impersonating === 'boolean') {
    view.impersonating = rec.impersonating
  }
  if (Array.isArray(rec.methods)) {
    view.methods = rec.methods.filter(
      (m): m is NonNullable<AuthnSessionView['methods']>[number] =>
        typeof m === 'string',
    )
  }
  if (
    rec.assurance === 'none' ||
    rec.assurance === 'single' ||
    rec.assurance === 'multi'
  ) {
    view.assurance = rec.assurance
  }
  if (typeof rec.auth_time === 'string') {
    view.auth_time = rec.auth_time
  }
  if (typeof rec.provider === 'string') {
    view.provider = rec.provider
  }
  return view
}

function parseField(value: unknown): AuthnField {
  const rec = asObject(value, 'challenge field is not an object')
  const field: AuthnField = {
    name: requireString(rec.name, 'challenge field carries no name'),
    kind: typeof rec.kind === 'string' ? rec.kind : '',
  }
  if (rec.required === true) {
    field.required = true
  }
  if (rec.secret === true) {
    field.secret = true
  }
  return field
}

function parseFactor(value: unknown): AuthnFactorOption {
  const rec = asObject(value, 'factor option is not an object')
  const factor: AuthnFactorOption = {
    id: requireString(rec.id, 'factor option carries no id'),
    kind: typeof rec.kind === 'string' ? rec.kind : '',
  }
  if (typeof rec.label === 'string') {
    factor.label = rec.label
  }
  return factor
}

/** Validates one step of an open flow into the client's step union
 * (the generated flat step narrowed by `kind`). `kind` is the
 * discriminator; an unknown kind is a protocol violation. */
function parseStep(body: unknown): AuthnStep {
  const rec = asObject(body, 'flow step carries no object')
  const kind = requireString(rec.kind, 'flow step carries no kind')
  switch (kind) {
    case 'challenge': {
      const fields = Array.isArray(rec.fields)
        ? rec.fields.map((entry) => parseField(entry))
        : []
      const step: AuthnStep = { kind: 'challenge', fields }
      const expiresIn = optionalNumber(rec.expires_in)
      if (expiresIn !== undefined) {
        step.expires_in = expiresIn
      }
      const resendAfter = optionalNumber(rec.resend_after)
      if (resendAfter !== undefined) {
        step.resend_after = resendAfter
      }
      return step
    }
    case 'redirect':
      return {
        kind: 'redirect',
        url: requireString(rec.url, 'redirect step carries no url'),
        method: requireString(rec.method, 'redirect step carries no method'),
      }
    case 'factor':
      return {
        kind: 'factor',
        factors: Array.isArray(rec.factors)
          ? rec.factors.map((entry) => parseFactor(entry))
          : [],
      }
    case 'enroll':
      return {
        kind: 'enroll',
        kinds: Array.isArray(rec.kinds)
          ? rec.kinds.map((entry) =>
              requireString(entry, 'enroll kind is not a string'),
            )
          : [],
      }
    case 'complete':
      return { kind: 'complete' }
    case 'webauthn':
      return { kind: 'webauthn', options: rec.options }
    default:
      throw protocolViolation(`flow step carries an unknown kind ${kind}`)
  }
}

/** The live-flow states the wire carries on an open reply (the
 * record's states, pkg/authn/login flow.go). A terminal record state
 * ('done'/'failed') never reaches a 2xx -- GET /api/v1/auth/flows/current
 * refuses it with a 401 -- so it is not in the set. */
const OPEN_FLOW_STATES = [
  'first',
  'processing',
  'second',
  'enroll',
  'ready',
] as const
type OpenFlowState = (typeof OPEN_FLOW_STATES)[number]

function isOpenFlowState(state: string): state is OpenFlowState {
  return (OPEN_FLOW_STATES as readonly string[]).includes(state)
}

/** Validates a flow reply into the client's flow-state union (the
 * generated reply types narrowed to one outcome each). The open-flow
 * shape is a number `seq` plus a `next` step; a settled completion
 * carries its outcome. A `state: 'authenticated'` body is the
 * cookie-delivered `AuthnLoginSession` when it carries a `session`
 * document, and the token-delivered `AuthnLoginToken` when it carries
 * the token pair instead; `state: 'elevated'` is a token step-up. A body
 * whose `state` is none of these -- or an empty 2xx -- is a protocol
 * violation, before any state change. */
function parseFlowState(body: unknown): AuthnFlowState {
  const rec = asObject(body, 'flow reply carries no body document')
  const state = requireString(rec.state, 'flow reply carries no state')
  switch (state) {
    case 'authenticated': {
      // A cookie session answers with a session document; a token session
      // answers with the token pair. The presence of `session` decides.
      if (rec.session !== undefined) {
        const session = asObject(
          rec.session,
          'session completion carries no session document',
        )
        return {
          state: 'authenticated',
          session: {
            id: requireString(session.id, 'session completion carries no id'),
            expires_at:
              typeof session.expires_at === 'string' ? session.expires_at : '',
          },
        }
      }
      return {
        state: 'authenticated',
        token_type: requireBearer(
          rec.token_type,
          'login completion carries no Bearer token_type',
        ),
        access_token: requireString(
          rec.access_token,
          'login completion carries no access_token string',
        ),
        expires_in: requireNumber(
          rec.expires_in,
          'login completion carries no numeric expires_in',
        ),
        refresh_token: requireString(
          rec.refresh_token,
          'login completion carries no refresh_token string',
        ),
      }
    }
    case 'elevated':
      return {
        state: 'elevated',
        token_type: requireBearer(
          rec.token_type,
          'step-up completion carries no Bearer token_type',
        ),
        access_token: requireString(
          rec.access_token,
          'step-up completion carries no access_token string',
        ),
        expires_in: requireNumber(
          rec.expires_in,
          'step-up completion carries no numeric expires_in',
        ),
      }
    case 'linked':
      return { state: 'linked' }
    case 'enrolled':
      return { state: 'enrolled' }
    default: {
      if (!isOpenFlowState(state)) {
        throw protocolViolation(`flow reply carries an unknown state ${state}`)
      }
      return {
        seq: requireNumber(rec.seq, 'flow reply carries no numeric seq'),
        state,
        next: parseStep(rec.next),
      }
    }
  }
}

/** Whether a settled flow reply is the cookie-delivered session
 * completion (as opposed to a token-delivered login). */
function isSessionCompletion(
  state: AuthnFlowState,
): state is SessionCompletion {
  return 'session' in state
}

/** Validates a token-issuing body (the token endpoint) into the tokens
 * the session commits. `expectRefreshToken` is true for the refresh
 * grant (the response always rotates), false where a reply may mint
 * none. */
function parseIssuedTokens(
  body: unknown,
  expectRefreshToken: boolean,
): IssuedTokens {
  const rec = asObject(body, 'token-issuing response carries no body document')
  const accessToken = requireString(
    rec.access_token,
    'token-issuing response carries no access_token string',
  )
  if (expectRefreshToken) {
    return {
      accessToken,
      refreshToken: requireString(
        rec.refresh_token,
        'token-issuing response carries no refresh_token string',
      ),
    }
  }
  const refreshToken = rec.refresh_token
  if (refreshToken === undefined) {
    return { accessToken, refreshToken: null }
  }
  return {
    accessToken,
    refreshToken: requireString(
      refreshToken,
      'token-issuing response carries a malformed refresh_token',
    ),
  }
}

/** Validates one method descriptor of the GET /api/v1/auth/methods body. `name`
 * and `kind` are the required fields; the rest are carried as the wire
 * sends them (a method that opens with a form carries its `fields`, one
 * whose bindings land under known provider values carries
 * `binding_providers`). */
function parseMethodDescriptor(value: unknown): AuthnMethodDescriptorModel {
  const rec = asObject(value, 'methods entry is not an object')
  const descriptor: AuthnMethodDescriptorModel = {
    name: requireString(rec.name, 'method descriptor carries no name'),
    kind: requireString(rec.kind, 'method descriptor carries no kind'),
  }
  if (typeof rec.protocol === 'string') {
    descriptor.protocol = rec.protocol
  }
  if (typeof rec.label === 'string') {
    descriptor.label = rec.label
  }
  if (rec.opens_account === true) {
    descriptor.opens_account = true
  }
  if (Array.isArray(rec.fields)) {
    descriptor.fields = rec.fields.map((entry) => parseField(entry))
  }
  if (Array.isArray(rec.binding_providers)) {
    descriptor.binding_providers = rec.binding_providers.map((entry) =>
      requireString(
        entry,
        'method descriptor carries a non-string binding provider',
      ),
    )
  }
  return descriptor
}

function parseMethodList(body: unknown): AuthnMethodList {
  const rec = asObject(body, 'methods 2xx carries no body document')
  if (!Array.isArray(rec.methods)) {
    throw protocolViolation('methods 2xx carries no methods array')
  }
  const signup = asObject(rec.signup, 'methods 2xx carries no signup document')
  if (typeof signup.enabled !== 'boolean') {
    throw protocolViolation('methods signup carries no enabled boolean')
  }
  const signupPassword = asObject(
    signup.password,
    'methods signup carries no password document',
  )
  if (typeof signupPassword.enabled !== 'boolean') {
    throw protocolViolation('methods signup password carries no enabled boolean')
  }
  const mfa = asObject(rec.mfa, 'methods 2xx carries no mfa document')
  if (!Array.isArray(mfa.factors)) {
    throw protocolViolation('methods mfa carries no factors array')
  }
  const human = asObject(rec.human, 'methods 2xx carries no human document')
  if (typeof human.required !== 'boolean') {
    throw protocolViolation('methods human carries no required boolean')
  }
  const humanProvider = requireString(
    human.provider,
    'methods human carries no provider string',
  )
  const humanSiteKey =
    human.site_key === undefined
      ? undefined
      : requireString(human.site_key, 'methods human site_key is not a string')
  return {
    methods: rec.methods.map((entry) => parseMethodDescriptor(entry)),
    signup: {
      enabled: signup.enabled,
      password: { enabled: signupPassword.enabled },
    },
    mfa: {
      factors: mfa.factors.map((entry) =>
        requireString(entry, 'mfa factor is not a string'),
      ),
    },
    human: {
      required: human.required,
      provider: humanProvider,
      ...(humanSiteKey === undefined ? {} : { site_key: humanSiteKey }),
    },
  }
}

/** Validates the POST /api/v1/auth/sso/discover body into the enterprise
 * sign-in entries it carries. The answer is always the same shape -- an
 * empty list when an email's domain has no provider -- so a 2xx without
 * a providers array is a contract violation, not "no providers". */
function parseSsoProviders(body: unknown): AuthnSsoProviderModel[] {
  const rec = asObject(body, 'sso discover 2xx carries no body document')
  if (!Array.isArray(rec.providers)) {
    throw protocolViolation('sso discover 2xx carries no providers array')
  }
  return rec.providers.map((entry) => {
    const provider = asObject(entry, 'sso provider entry is not an object')
    const protocol = requireString(
      provider.protocol,
      'sso provider carries no protocol',
    )
    if (protocol !== 'oidc' && protocol !== 'saml') {
      throw protocolViolation(
        `sso provider carries an unknown protocol ${protocol}`,
      )
    }
    return {
      id: requireString(provider.id, 'sso provider carries no id'),
      name: requireString(provider.name, 'sso provider carries no name'),
      protocol,
    }
  })
}

/** Validates the POST /api/v1/auth/register body into the generated
 * registration model. A general-purpose self-registration always answers
 * the same outcome -- the literal `state: 'registered'`, the account it
 * opened, and the email address awaiting verification -- so anything
 * else is a contract violation, never a successful registration: the
 * account was not verifiably opened, and a caller must not proceed to a
 * login as if it had been. */
function parseRegistration(body: unknown): AuthnLoginRegistration {
  const rec = asObject(body, 'registration 2xx carries no body document')
  if (rec.state !== 'registered') {
    throw protocolViolation('registration 2xx carries no registered state')
  }
  const verification = asObject(
    rec.verification,
    'registration carries no verification document',
  )
  if (verification.state !== 'pending') {
    throw protocolViolation('registration verification is not pending')
  }
  return {
    state: 'registered',
    account_id: requireString(
      rec.account_id,
      'registration carries no account_id string',
    ),
    verification: {
      state: 'pending',
      email: requireString(
        verification.email,
        'registration verification carries no email string',
      ),
    },
  }
}

/** The permission-set shape that carries nothing: every anonymous
 * snapshot and every login transition uses it. */
const NO_PERMISSION_SETS: AuthPermissionSets = {
  tenant: null,
  system: null,
}

/** The feature state of every anonymous snapshot: no document is ever
 * requested for an anonymous caller. */
const NO_FEATURES: FeatureState = { state: 'none' }

/** Validates the GET /api/v1/features body into the served feature names. The
 * document carries the names only; a name the frontend does not know is
 * accepted (the set is the process's, not the build's). A body that is
 * not an object, or whose `features` is not a string array, is a
 * protocol violation -- never "no features". */
function parseFeatureDocument(body: unknown): ReadonlySet<string> {
  const rec = asObject(body, 'GET /api/v1/features answered no document')
  if (!Array.isArray(rec.features)) {
    throw protocolViolation('GET /api/v1/features carries no features array')
  }
  const names = new Set<string>()
  for (const entry of rec.features) {
    if (typeof entry !== 'string') {
      throw protocolViolation('GET /api/v1/features carries a non-string name')
    }
    names.add(entry)
  }
  return names
}

/** The purpose a flow was started for, as far as this package tracks it
 * to tell a cookie login completion from a cookie step-up completion
 * (both answer the same session body). `login` and `step-up` are the two
 * that settle a session; `link` and `enroll` settle an action result
 * instead. */
type FlowPurpose = 'login' | 'link' | 'step-up' | 'enroll'

function normalizePurpose(purpose: string | undefined): FlowPurpose {
  return purpose === 'link' || purpose === 'step-up' || purpose === 'enroll'
    ? purpose
    : 'login'
}

/** Whether a refusal of the held refresh token is terminal. The token
 * endpoint answers an unusable grant -- invalid, replayed or a revoked
 * session -- as an RFC 6749 §5.2 body with status 400 (`error:
 * invalid_grant`) and a client credential that did not verify as 401;
 * both mean the held token is dead, so the session signs out. A 503
 * (a dependency that could not answer) and a transport failure are not
 * a refusal: the held token may still be valid and the caller
 * rethrows. */
function isRefreshRefusal(error: unknown): boolean {
  if (!isApiError(error)) {
    return false
  }
  return (
    error.status === 400 ||
    error.status === 401 ||
    error.code === ERROR_CODE_PROTOCOL
  )
}

/**
 * Creates the session. The store is optional and is the bridge into
 * @speed/api-client for a token-delivered session: a host that starts
 * its login flow itself with the generated
 * `authnLoginStart({ delivery: 'token' })` operation and passes a store
 * gets the token path, with `refreshAccessToken: () => session.refresh()`
 * giving silent refresh for free. startFlow cannot be used to start that
 * flow: it forces the cookie delivery. A token session is not rebuilt by
 * restore() -- its requests carry no cookie -- and is lost on reload. A
 * host that leaves the store out (the platform's own composition) runs
 * the cookie path: the login flow is started as a cookie session, the
 * credential is the server's httpOnly cookie, and restore()
 * re-establishes the session after a reload.
 *
 * The browser cookie path requires the frontend and the API to be
 * same-site: the session cookie is SameSite=Lax (or Strict; the server
 * refuses None).
 */
export function createAuthSession(store?: AccessTokenStore): AuthSession {
  let snapshot: AuthSnapshot = {
    state: 'anonymous',
    principal: null,
    impersonating: false,
    permissionSets: NO_PERMISSION_SETS,
    features: NO_FEATURES,
  }
  // The held refresh token: closure-only, never stored, never exposed.
  // Only a token-delivered session holds one.
  let refreshToken: string | null = null
  // The generation guard: a session commit (a login or step-up
  // completion) and a logout bump it; restore and refresh never do. A
  // commit applies only when the generation captured at its entry still
  // holds (see the file header).
  let generation = 0
  // The identity-transition guard: how many transitions are open, and a
  // sequence number bumped on every open. A restore discards an anonymous
  // answer while a transition is open, and when one opened after the
  // restore was issued -- that answer describes a credential the
  // transition is replacing (see beginIdentityTransition).
  let openTransitions = 0
  let transitionSequence = 0
  // The purpose of the flow this session most recently advanced. The
  // server answers a cookie login and a cookie step-up with the same
  // session completion, so this is the only way to keep a step-up's
  // principal and permission sets while a login clears them.
  let pendingPurpose: FlowPurpose = 'login'
  // The single-flight refresh: at most one in-flight request per held
  // refresh token.
  let refreshFlight: { held: string; promise: Promise<boolean> } | null = null
  // The single-flight feature retry: concurrent retryFeatures() calls of
  // the same session share one GET /api/v1/features read. The flight is keyed on
  // the generation and account the read started under, so a read that
  // outlived its session (a logout and re-login while it was in flight) is
  // never handed to the next session: its own commit check is bound to
  // discard it, and sharing it would silently skip the new session's retry.
  let featuresFlight: {
    readonly generation: number
    readonly accountId: string
    readonly promise: Promise<void>
  } | null = null
  const listeners = new Set<AuthSessionListener>()

  function notify(): void {
    for (const listener of listeners) {
      try {
        listener(snapshot)
      } catch {
        // A throwing listener is contained: a subscriber (a host
        // callback or a test observer) must not freeze the others nor
        // corrupt the operation that committed. The listener that threw
        // is the host's bug to find; the commit it heard about stands.
      }
    }
  }

  /** Clears everything local: the store (a token session), the held
   * refresh token and the snapshot. Used by logout and by restore/refresh
   * when the server says nobody is signed in. The caller decides whether
   * the clear ends its generation: logout clears and then bumps, so a
   * restore or refresh that started under the old generation cannot
   * resurrect the ended session; the restore/refresh-side clears
   * deliberately do NOT bump -- the server's answer is the session's
   * death, but a commit that started before it and settles after it is
   * the session's legitimate successor and must still be able to
   * commit. */
  function clearLocal(): void {
    store?.set(null)
    refreshToken = null
    snapshot = {
      state: 'anonymous',
      principal: null,
      impersonating: false,
      permissionSets: NO_PERMISSION_SETS,
      features: NO_FEATURES,
    }
  }

  /** Commits a validated token issuance without touching the principal
   * (a refresh, or a step-up whose principal the caller keeps). The
   * snapshot's permission sets are retained: the same session
   * continues. */
  function commitTokens(issued: IssuedTokens): void {
    store?.set(issued.accessToken)
    if (issued.refreshToken !== null) {
      refreshToken = issued.refreshToken
    }
    if (snapshot.state !== 'authenticated') {
      snapshot = { ...snapshot, state: 'authenticated' }
    }
  }

  /** Commits a login completion: the store gets the new access token (a
   * token session), the closure the minted refresh token, and the
   * snapshot the freshly read principal and feature document with both
   * permission domains cleared (a login opens a fresh server session, so
   * nothing rides over from the previous one). */
  function commitLogin(
    issued: IssuedTokens,
    principal: AuthnPrincipal,
    features: FeatureState,
  ): void {
    store?.set(issued.accessToken)
    if (issued.refreshToken !== null) {
      refreshToken = issued.refreshToken
    }
    snapshot = {
      state: 'authenticated',
      principal,
      impersonating: false,
      permissionSets: NO_PERMISSION_SETS,
      features,
    }
  }

  /** The feature state a settled GET /api/v1/features read yields: `ready`
   * with the parsed names, `failed` carrying the read's error. A failed
   * feature read never fails the commit that carried it: it is neither
   * "every feature absent" nor "every feature present". */
  function settledFeatures(
    result: PromiseSettledResult<unknown>,
  ): FeatureState {
    if (result.status === 'rejected') {
      return { state: 'failed', error: result.reason }
    }
    try {
      return { state: 'ready', names: parseFeatureDocument(result.value) }
    } catch (error) {
      return { state: 'failed', error }
    }
  }

  /** The commit of a token login completion: the response is validated
   * first (a contract-violating 2xx rejects even when a newer commit
   * landed -- the caller deserves to know its own request failed), then
   * applied only if no other commit won meanwhile. The principal is read
   * from GET /api/v1/auth/me and the feature document from GET /api/v1/features, the
   * two reads in parallel with the freshly written token. A failed
   * profile read clears the store and rejects the commit; a failed
   * feature read commits the session with the feature state `failed`. */
  async function settleLogin(
    opGeneration: number,
    state: TokenCompletion,
  ): Promise<void> {
    const issued: IssuedTokens = {
      accessToken: state.access_token,
      refreshToken: state.refresh_token,
    }
    if (generation !== opGeneration) {
      throw new OperationSupersededError(snapshot)
    }
    store?.set(issued.accessToken)
    const [profile, features] = await Promise.allSettled([
      authnProfileGet(),
      featureBoardList(),
    ])
    let principal: AuthnPrincipal
    try {
      if (profile.status === 'rejected') {
        throw profile.reason
      }
      principal = parsePrincipal(profile.value)
    } catch (error) {
      store?.set(null)
      refreshToken = null
      throw error
    }
    if (generation !== opGeneration) {
      throw new OperationSupersededError(snapshot)
    }
    commitLogin(issued, principal, settledFeatures(features))
    generation += 1
    notify()
  }

  /** The commit of a cookie login completion: the server set the cookie,
   * so the session reads GET /api/v1/auth/me for the freshly signed-in
   * principal (riding the ambient cookie) and GET /api/v1/features for the
   * feature document, the two in parallel, and applies them with both
   * permission domains cleared. A failed feature read commits the
   * session with the feature state `failed`. No store is touched. */
  async function settleLoginSession(opGeneration: number): Promise<void> {
    if (generation !== opGeneration) {
      throw new OperationSupersededError(snapshot)
    }
    const [profile, features] = await Promise.allSettled([
      authnProfileGet(),
      featureBoardList(),
    ])
    if (profile.status === 'rejected') {
      throw profile.reason
    }
    const principal = parsePrincipal(profile.value)
    if (generation !== opGeneration) {
      throw new OperationSupersededError(snapshot)
    }
    snapshot = {
      state: 'authenticated',
      principal,
      impersonating: false,
      permissionSets: NO_PERMISSION_SETS,
      features: settledFeatures(features),
    }
    generation += 1
    notify()
  }

  /** The commit of a token step-up completion: the access token is
   * replaced, the principal and the permission sets are kept (the same
   * session continues), and no GET /api/v1/auth/me read follows. Validation
   * precedes the generation check, as for a login. */
  function settleElevated(
    opGeneration: number,
    state: ElevatedCompletion,
  ): void {
    const issued: IssuedTokens = {
      accessToken: state.access_token,
      refreshToken: null,
    }
    if (generation !== opGeneration) {
      throw new OperationSupersededError(snapshot)
    }
    commitTokens(issued)
    generation += 1
    notify()
  }

  /** The commit of a cookie step-up completion: the server rotated the
   * cookie, the principal, the permission sets and the feature state are
   * kept (the same session continues), and no GET /api/v1/auth/me read
   * follows. */
  function settleElevatedSession(opGeneration: number): void {
    if (generation !== opGeneration) {
      throw new OperationSupersededError(snapshot)
    }
    snapshot = {
      state: 'authenticated',
      principal: snapshot.principal,
      impersonating: snapshot.impersonating,
      permissionSets: snapshot.permissionSets,
      features: snapshot.features,
    }
    generation += 1
    notify()
  }

  /** The one actual refresh request (see refreshFlight). The captured
   * principal is the principal this refresh spoke for when it started;
   * comparing it against the winner's is how the losing refresh decides
   * whether its answer is worth a silent-401 retry. */
  async function runRefresh(
    held: string,
    capturedGeneration: number,
    capturedPrincipal: AuthnPrincipal | null,
  ): Promise<boolean> {
    let pair: unknown
    try {
      pair = await authnTokenIssue({
        grant_type: 'refresh_token',
        refresh_token: held,
      })
    } catch (error) {
      if (generation !== capturedGeneration) {
        // A commit landed while the refresh was in flight: it owns the
        // session now; its state stands untouched.
        return false
      }
      if (isRefreshRefusal(error)) {
        // The server refused the held token: the session is over.
        // Deliberately no generation bump (see clearLocal's doc).
        clearLocal()
        notify()
        return false
      }
      // Transport failure or a server-side error: the held token may
      // still be valid. Nothing was cleared on the way out, so the
      // session stands exactly as it was; rethrow raw.
      throw error
    }
    if (generation !== capturedGeneration) {
      // A commit landed while the refresh was in flight and owns the
      // session now. If it kept the held token -- a token step-up mints
      // no new refresh token -- this refresh's success still consumed the
      // token server-side, so the session adopts the rotated one or its
      // next refresh reads as a replay and dies. A commit that replaced
      // the token (a login) or ended the session (a logout) makes the
      // pair stale: dropped outright.
      if (refreshToken === held) {
        try {
          const rotated = parseIssuedTokens(pair, true)
          refreshToken = rotated.refreshToken
          // Nothing observable changed: no store write, no snapshot
          // change and no notify -- the winner's state already stands.
          // Whether the resolution is worth a silent-401 retry depends
          // on who won: the only commit that keeps the held token is a
          // step-up, which keeps the principal this refresh started
          // under, so resolving true is safe -- the retried request
          // speaks for the same identity.
          const winner = snapshot.principal
          return (
            capturedPrincipal !== null &&
            winner !== null &&
            capturedPrincipal.account_id === winner.account_id
          )
        } catch {
          // A contract-violating 2xx consumed the held token with
          // nothing adoptable: the session keeps the winner's tokens but
          // can no longer refresh. Degrade silently -- clearing would
          // kill the session a commit just established.
        }
      }
      return false
    }
    let issued: IssuedTokens
    try {
      issued = parseIssuedTokens(pair, true)
    } catch {
      // A 2xx consumed the held token but violated the contract: the
      // session cannot continue on an unverifiable pair. Deliberately
      // no generation bump (see clearLocal's doc).
      clearLocal()
      notify()
      return false
    }
    commitTokens(issued)
    notify()
    return true
  }

  return {
    getSnapshot(): AuthSnapshot {
      return snapshot
    },

    subscribe(listener: AuthSessionListener): () => void {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },

    async listMethods(): Promise<AuthnMethodList> {
      return parseMethodList(await authnLoginMethods())
    },

    async discoverSso(email: string): Promise<AuthnSsoProviderModel[]> {
      return parseSsoProviders(await authnSsoDiscover({ email }))
    },

    async register(
      request: AuthnLoginRegisterBody,
    ): Promise<AuthnLoginRegistration> {
      // A one-request account opening: parsed and handed back, changing
      // nothing. No snapshot write, no store write, no notify -- the
      // session still belongs to whoever it belonged to before.
      return parseRegistration(await authnLoginRegister(request))
    },

    async startFlow(request: AuthnLoginStartBody): Promise<AuthnFlowState> {
      // A pure flow step: parsed and handed back, changing nothing. The
      // delivery is forced to the cookie session whatever the caller
      // asked for -- this package's browser path is the cookie one.
      pendingPurpose = normalizePurpose(request.purpose)
      return parseFlowState(
        await authnLoginStart({ ...request, delivery: 'session' }),
      )
    },

  /** Starts a flow on the identity already signed in, for `purpose:
   * 'link'`, `'step-up'` or `'enroll'`. The `delivery` is left to the
   * caller: the callers of this operation (the account surfaces) do not
   * set one, so the server resolves its own default (a cookie session),
   * and a cookie step-up rotates the cookie exactly as a login does. A
   * 2xx body that is not a valid flow state rejects with `client.protocol`
   * before anything else happens. */
    async startAuthenticatedFlow(
      request: AuthnLoginStartAuthenticatedBody,
    ): Promise<AuthnFlowState> {
      pendingPurpose = normalizePurpose(request.purpose)
      return parseFlowState(await authnLoginStartAuthenticated(request))
    },

    async answerFlow(request: AuthnLoginAnswerBody): Promise<AuthnFlowState> {
      const opGeneration = generation
      const state = parseFlowState(await authnLoginAnswer(request))
      // Only a session completion changes the session.
      if (state.state === 'authenticated') {
        if (isSessionCompletion(state)) {
          if (pendingPurpose === 'step-up') {
            settleElevatedSession(opGeneration)
          } else {
            await settleLoginSession(opGeneration)
          }
        } else {
          await settleLogin(opGeneration, state)
        }
        pendingPurpose = 'login'
      } else if (state.state === 'elevated') {
        settleElevated(opGeneration, state)
        pendingPurpose = 'login'
      } else if (state.state === 'linked' || state.state === 'enrolled') {
        pendingPurpose = 'login'
      }
      // An open step: no state change.
      return state
    },

    async readFlow(): Promise<AuthnFlowState | null> {
      try {
        return parseFlowState(await authnLoginCurrent())
      } catch (error) {
        if (isApiError(error) && error.status === 401) {
          // No binding, or the flow is done or gone: there is no flow
          // to resume, which is an ordinary answer, not a failure.
          return null
        }
        throw error
      }
    },

    async abandonFlow(): Promise<void> {
      try {
        await authnLoginAbandon()
      } catch (error) {
        if (isApiError(error) && error.status === 401) {
          // The flow is already gone: DELETE flows/current is
          // idempotent, so this is the same success.
          return
        }
        throw error
      }
    },

    async logout(): Promise<void> {
      const opGeneration = generation
      // The refresh token of the session this logout speaks for: a
      // commit that keeps it (a token step-up) continues the very
      // session the revocation ends.
      const revokedSessionToken = refreshToken
      let failure: unknown
      let failed = false
      try {
        await authnSessionLogout()
      } catch (error) {
        failed = true
        failure = error
      }
      // A pre-empting commit that replaced the held token is a
      // different session the revocation never touched: it stands. A
      // commit that kept the held token (a step-up) continues the
      // session this logout ended, so the local state converges to
      // signed-out either way -- as does a logout whose own request
      // failed, since a session it could not revoke is no longer
      // trustworthy here. A cookie session holds no token, so any commit
      // that landed while the logout was in flight is a different
      // session and stands.
      const replaced =
        generation !== opGeneration &&
        (revokedSessionToken === null || refreshToken !== revokedSessionToken)
      if (!replaced) {
        clearLocal()
        // Bump so that a restore or refresh that started under this
        // generation is dropped when it resolves: neither must
        // resurrect the session this logout ended.
        generation += 1
        notify()
      }
      if (failed) {
        throw failure
      }
    },

    async restore(issue?: RequestFn): Promise<boolean> {
      const opGeneration = generation
      // The transition guard as it stood when this read was issued: an
      // anonymous answer is trustworthy only if no transition was open at
      // entry and none opened while the read was in flight (see
      // beginIdentityTransition). A read issued while a window was already
      // open carries the credential that window replaces, so its answer is
      // discarded whenever it settles -- including after the release.
      const transitionAtEntry = openTransitions > 0
      const transitionSequenceAtEntry = transitionSequence
      // Whether this restore begins from an authenticated snapshot: only
      // an anonymous start reads the feature document (the authenticated
      // path continues the same session and keeps the state).
      const wasAuthenticated = snapshot.state === 'authenticated'
      // The round-scoped request function, when the client supplied one,
      // carries both reads; otherwise the generated operations' bound
      // request function does. The profile read follows an await, so it
      // is this parameter -- not the client's synchronous-call marker --
      // that keeps it out of the refresh round awaiting this call.
      const readSession = (): Promise<AuthnSessionViewModel> =>
        issue === undefined
          ? authnSessionGet()
          : issue<AuthnSessionViewModel>('/api/v1/auth/session')
      const readProfile = (): Promise<AuthnProfileProfile> =>
        issue === undefined
          ? authnProfileGet()
          : issue<AuthnProfileProfile>('/api/v1/auth/me')
      const readFeatures = (): Promise<FeatureBoardDocument> =>
        issue === undefined
          ? featureBoardList()
          : issue<FeatureBoardDocument>('/api/v1/features')
      let view: AuthnSessionView
      try {
        view = parseSessionView(await readSession())
      } catch (error) {
        if (isApiError(error) && error.status === 401) {
          // A credential was presented but does not hold: nobody is
          // signed in. Treat it as the anonymous answer, like an
          // explicit authenticated:false.
          view = { authenticated: false }
        } else {
          throw error
        }
      }
      if (generation !== opGeneration) {
        // A commit landed while the read was in flight and owns the
        // session now; its state stands untouched.
        return false
      }
      if (!view.authenticated) {
        // An identity transition replaces the credential this read
        // carried. An anonymous answer then speaks for the outgoing
        // credential, not for the session that is coming, so it must not
        // end anything: resolve without changing the snapshot (the
        // refused request that ran this restore keeps its 401) and let the
        // next read after the release settle the true state.
        if (
          transitionAtEntry ||
          openTransitions > 0 ||
          transitionSequence !== transitionSequenceAtEntry
        ) {
          return false
        }
        if (snapshot.state !== 'anonymous') {
          clearLocal()
          notify()
        }
        return false
      }
      if (wasAuthenticated) {
        const principal = parsePrincipal(await readProfile())
        if (generation !== opGeneration) {
          return false
        }
        // The same session continues: the permission sets and the
        // feature state are kept.
        snapshot = {
          state: 'authenticated',
          principal,
          impersonating: view.impersonating === true,
          permissionSets: snapshot.permissionSets,
          features: snapshot.features,
        }
        notify()
        return true
      }
      // An anonymous start reads the principal and the feature document
      // in parallel and commits them together. A failed profile read
      // rethrows (the snapshot stands); a failed feature read commits
      // the session with the feature state `failed`.
      const [profile, features] = await Promise.allSettled([
        readProfile(),
        readFeatures(),
      ])
      if (profile.status === 'rejected') {
        throw profile.reason
      }
      const principal = parsePrincipal(profile.value)
      if (generation !== opGeneration) {
        return false
      }
      snapshot = {
        state: 'authenticated',
        principal,
        impersonating: view.impersonating === true,
        permissionSets: snapshot.permissionSets,
        features: settledFeatures(features),
      }
      notify()
      return true
    },

    beginIdentityTransition(): () => void {
      openTransitions += 1
      transitionSequence += 1
      let released = false
      return () => {
        // Idempotent per release: a caller that releases twice (or passes
        // one release to two owners) must not close someone else's window.
        if (released) {
          return
        }
        released = true
        openTransitions -= 1
      }
    },

    refresh(): Promise<boolean> {
      const held = refreshToken
      if (held === null) {
        // Nothing to refresh (a cookie session never holds a refresh
        // token); not even a store read -- and certainly no state
        // change.
        return Promise.resolve(false)
      }
      const capturedGeneration = generation
      // The principal this refresh speaks for, captured with the
      // generation: a commit that lands while the refresh is in flight
      // is compared against it. Never null here: a held token and an
      // authenticated snapshot are cleared together.
      const capturedPrincipal = snapshot.principal
      const inFlight = refreshFlight
      if (inFlight !== null && inFlight.held === held) {
        // Same held token: share the in-flight request. The flight is
        // keyed on the token, not the generation, because a step-up
        // commits without replacing the held token, so a refresh
        // started after it would otherwise fire a second presentation
        // of the same token -- which the authn server reads as theft
        // and answers by rotating the whole family out from under the
        // session.
        return inFlight.promise
      }
      const promise = runRefresh(held, capturedGeneration, capturedPrincipal)
      refreshFlight = { held, promise }
      const clearFlight = (): void => {
        if (refreshFlight?.promise === promise) {
          refreshFlight = null
        }
      }
      promise.then(clearFlight, clearFlight)
      return promise
    },

    isStepUpShortfall(error: unknown): boolean {
      // The session is still established exactly when the refusal was a
      // strength shortfall rather than the end of the session: a refused
      // request runs the client's restore leg, which flips an ended
      // session anonymous before the error reaches the caller.
      return (
        isApiError(error) &&
        error.status === 401 &&
        snapshot.state === 'authenticated'
      )
    },

    retryFeatures(): Promise<void> {
      // Anonymous sessions never request the document, and there is
      // nothing to retry.
      if (snapshot.state !== 'authenticated' || snapshot.principal === null) {
        return Promise.resolve()
      }
      const capturedGeneration = generation
      const capturedAccountId = snapshot.principal.account_id
      // Share only a read that belongs to this same session. A flight
      // begun under another generation or account is already destined to
      // be discarded by its own commit check, so handing it back would
      // resolve this call without issuing the read it promised.
      if (
        featuresFlight !== null &&
        featuresFlight.generation === capturedGeneration &&
        featuresFlight.accountId === capturedAccountId
      ) {
        return featuresFlight.promise
      }
      const previous = snapshot.features
      const pending: FeatureState = { state: 'pending' }
      // The flight is registered before `pending` is published: a listener
      // notified of the transition that re-enters retryFeatures() must find
      // this read and share it, not start a second one.
      let settle!: () => void
      const promise = new Promise<void>((resolve) => {
        settle = resolve
      })
      const flight = {
        generation: capturedGeneration,
        accountId: capturedAccountId,
        promise,
      }
      featuresFlight = flight
      snapshot = { ...snapshot, features: pending }
      notify()
      const clearFlight = (): void => {
        if (featuresFlight === flight) {
          featuresFlight = null
        }
      }
      promise.then(clearFlight, clearFlight)
      void (async (): Promise<void> => {
        try {
          let next: FeatureState
          try {
            next = {
              state: 'ready',
              names: parseFeatureDocument(await featureBoardList()),
            }
          } catch (error) {
            next = { state: 'failed', error }
          }
          // The write applies only to the session the read started under:
          // same generation, still authenticated, same principal. The
          // generation check alone is not enough -- the server-ended-session
          // clears deliberately do not bump it.
          const applies =
            generation === capturedGeneration &&
            snapshot.state === 'authenticated' &&
            snapshot.principal !== null &&
            snapshot.principal.account_id === capturedAccountId
          if (!applies) {
            // A commit landed while the read was in flight. If it kept this
            // retry's pending marker (a step-up), restore the previous
            // state; a login or logout replaced the marker with its own.
            if (snapshot.features === pending) {
              snapshot = { ...snapshot, features: previous }
              notify()
            }
            return
          }
          snapshot = { ...snapshot, features: next }
          notify()
        } finally {
          settle()
        }
      })()
      return promise
    },

    setPermissionSet(
      domain: AuthDomain,
      perms: readonly string[] | null,
    ): void {
      // The stored list is a defensive copy, never an alias of the
      // caller's array: a host that reuses one array across calls (or
      // mutates it after attaching) cannot change the snapshot.
      const copy: readonly string[] | null = perms === null ? null : [...perms]
      const current = snapshot.permissionSets
      snapshot = {
        ...snapshot,
        permissionSets:
          domain === 'tenant'
            ? { tenant: copy, system: current.system }
            : { tenant: current.tenant, system: copy },
      }
      notify()
    },
  }
}
