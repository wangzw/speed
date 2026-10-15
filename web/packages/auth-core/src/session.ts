/**
 * The browser session state machine: createAuthSession wires the
 * generated authn flow surface of @speed/api-sdk (the login / link /
 * step-up / enroll flow operations, the methods listing, logout, and
 * the token endpoint a refresh uses) into one memory-only session.
 *
 * What lives where:
 * - the access token lives in the caller-supplied AccessTokenStore
 *   (the same store the host's @speed/api-client reads on every send,
 *   so a login here is immediately visible to every request);
 * - the refresh token lives in this closure only. The authn token
 *   response carries it in the body (the browser path delivers
 *   `delivery: 'token'`; there is no refresh cookie), and the refresh
 *   request takes the held token in its body, so the session keeps its
 *   own copy and never writes the store on a refresh's way out.
 *
 * Login is a server-side flow the client advances, not one request:
 * POST /auth/flows starts `purpose: 'login'`, POST /auth/flows/answer
 * answers the current step, GET /auth/flows/current resumes after a
 * page refresh and DELETE /auth/flows/current abandons it. The flow is
 * bound with a cookie by default (binding: 'cookie'), so the frontend
 * never reads a flow token; POST /auth/flows/authenticated starts the
 * same machine on the identity already signed in for `purpose: 'link'`,
 * `'step-up'` or `'enroll'`. Those operations never change the session
 * -- they only hand the step back to the view (see the interface docs).
 * Only `answerFlow` commits, and only when the step settles a token
 * issuance: a `purpose: 'login'` completion (state 'authenticated')
 * opens the session, a `step-up` completion (state 'elevated')
 * re-mints the access token with the principal unchanged, and a
 * `link`/`enroll` completion (state 'linked'/'enrolled') does not touch
 * the login state.
 *
 * The response shapes are not in the generated surface: every authn
 * fragment declares its 2xx without a schema, so orval types the
 * return as `void`. This package therefore hand-writes the response
 * models (`AuthnFlowState`, `AuthnStep`, `AuthnField`,
 * `AuthnFactorOption`, `AuthnPrincipal`, `AuthnMethodList`) and guards
 * every body-carrying 2xx at runtime: a body that is not an object, or
 * that is missing a required field, is refused as a
 * `status: 200`, `code: client.protocol` ApiError -- a contract
 * violating 2xx is never a successful login. The generated seam sets no
 * `requireJsonBody` (see the @speed/api-sdk runtime.ts doc), so an
 * empty 2xx resolves as undefined through it and the guard must catch
 * that too, before the first property access (a native TypeError is
 * not an ApiError and would escape the whole error contract).
 *
 * The failure contract: a request that fails rejects the raw ApiError
 * (distinguishable with isApiError) and changes nothing. A token-
 * issuing commit -- a login completion or a step-up completion -- that
 * lost a concurrent generation race rejects with
 * OperationSupersededError instead of silently resolving with the
 * winner's snapshot (see the class's doc comment). `refresh()` is the
 * silent path: it resolves true when the store holds a fresh token
 * afterwards, false when there is nothing to refresh or the held token
 * was refused (the session signs out locally), and rethrows raw only
 * for a transport or server-side failure (the held tokens stand).
 *
 * Permission sets: the snapshot additionally carries
 * setPermissionSet(domain, list) data -- host-attached lists. The
 * session never evaluates a list; it only applies the survival rules
 * when a principal change commits: a silent refresh or a step-up (the
 * same session continuing) keeps both lists, `link` and `enroll` keep
 * them (they do not change the login state), and a login -- even by
 * the same account -- or an anonymous transition clears both: a login
 * opens a fresh server session whose lists the host has not fetched
 * yet. A failed operation changes nothing, lists included.
 *
 * The failure semantics of `logout` deliberately diverge from the
 * above: a logout whose own request is refused still clears the local
 * session and rejects the raw ApiError, because a session it could no
 * longer revoke is no longer trustworthy locally either.
 */

import {
  ApiError,
  ERROR_CODE_PROTOCOL,
  isApiError,
} from '@speed/api-client'
import type { AccessTokenStore } from '@speed/api-client'
import {
  authnLoginAbandon,
  authnLoginAnswer,
  authnLoginCurrent,
  authnLoginMethods,
  authnLoginStart,
  authnLoginStartAuthenticated,
  authnProfileGet,
  authnSessionLogout,
  authnTokenIssue,
} from '@speed/api-sdk'
import type {
  AuthnLoginAnswerBody,
  AuthnLoginStartAuthenticatedBody,
  AuthnLoginStartBody,
} from '@speed/api-sdk'

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

/** The authenticated principal: the fields of GET /auth/me. A
 * hand-written model over the profile fragment (whose 2xx declares no
 * schema); it converges with the generated surface once that fragment
 * declares one. `account_id` is the account's identity; `identifiers`
 * and `links` are its verified identifiers and its external bindings,
 * each with its own identifier. */
export interface AuthnPrincipal {
  /** The account's identifier. */
  account_id: string
  /** The account kind (a person, a service, ...). */
  kind: string
  /** The display name. */
  display_name: string
  /** Whether the account requires a second factor. */
  mfa_required: boolean
  /** Whether the account has a password set. */
  password_set: boolean
  /** Creation instant, RFC 3339. */
  created_at: string
  /** The account's free-form attribute set. */
  attributes: Readonly<Record<string, string>>
  /** The account's preferred locale. */
  locale: string
  /** The account's preferred time zone. */
  time_zone: string
  /** The account's verified identifiers. */
  identifiers: readonly AuthnIdentifier[]
  /** The account's external bindings. */
  links: readonly AuthnLink[]
}

/** One verified identifier of an account (phone, email, ...). */
export interface AuthnIdentifier {
  id: string
  /** The identifier kind (email, phone, ...). */
  kind: string
  /** The identifier value. */
  value: string
  /** When it was verified, RFC 3339. */
  verified_at: string
  /** How it was verified. */
  verified_via: string
}

/** One external binding of an account (a social or enterprise
 * provider). */
export interface AuthnLink {
  id: string
  /** The provider name. */
  provider: string
  /** The email the provider asserted, when it did. */
  email?: string
  /** When the binding was created, RFC 3339. */
  created_at: string
  /** When the binding was last used to sign in, RFC 3339. */
  last_login_at?: string
}

/** One input a challenge step asks the client for. */
export interface AuthnField {
  /** The field name the answer must carry. */
  name: string
  /** The field kind (text, password, ...). */
  kind: string
  /** Whether the answer must supply it. */
  required?: boolean
  /** Whether the value is a secret (masked on input). */
  secret?: boolean
}

/** One second-factor option a factor step offers. */
export interface AuthnFactorOption {
  /** The factor's identifier. */
  id: string
  /** The factor kind (totp, webauthn, ...). */
  kind: string
  /** A display label, when the server supplies one. */
  label?: string
}

/** The next step of an open flow -- what the client does next. */
export type AuthnStep =
  | {
      kind: 'challenge'
      fields: readonly AuthnField[]
      /** How long the challenge stays valid, in seconds. */
      expires_in?: number
      /** How long before the client may resend, in seconds. */
      resend_after?: number
    }
  | { kind: 'redirect'; url: string; method: string }
  | { kind: 'factor'; factors: readonly AuthnFactorOption[] }
  | { kind: 'enroll'; kinds: readonly string[] }
  | { kind: 'complete' }
  | { kind: 'webauthn'; options: unknown }

/** The state of a flow as the wire carries it. An open flow (the
 * server is awaiting the next answer) carries a sequence number and
 * the next step; a settled flow carries its outcome. A completion that
 * issued tokens -- `authenticated` for a login, `elevated` for a
 * step-up -- carries the access token (and, for a login, the refresh
 * token); `linked` and `enrolled` settle a link or enroll flow without
 * touching the login state.
 *
 * The open-flow `state` is the record's live state (pkg/authn/login
 * flow.go): 'first', 'processing', 'second', 'enroll' or 'ready'. The
 * design's own enumeration listed only the three the client renders a
 * prompt for; 'ready' is reachable too (a submit step that reached the
 * ready state answers `next.kind: 'complete'` while still carrying
 * `state: 'ready'`), so it is part of the set rather than a protocol
 * violation. */
export type AuthnFlowState =
  | {
      seq: number
      state: 'first' | 'second' | 'enroll' | 'ready' | 'processing'
      next: AuthnStep
    }
  | {
      state: 'authenticated'
      token_type: 'Bearer'
      access_token: string
      expires_in: number
      refresh_token: string
    }
  | {
      state: 'elevated'
      token_type: 'Bearer'
      access_token: string
      expires_in: number
    }
  | { state: 'linked' | 'enrolled' }

/** The login methods this deployment offers (GET /auth/methods). The
 * list varies with the deployment's settings and the modules in the
 * assembly, so the frontend renders it rather than hard-coding a set. */
export interface AuthnMethodList {
  methods: readonly string[]
}

/** The observable session state. Immutable by convention: hosts read
 * it, they never mutate it. */
export interface AuthSnapshot {
  state: 'anonymous' | 'authenticated'
  /** The authenticated principal, or null while anonymous. */
  principal: AuthnPrincipal | null
  /** The host-attached permission lists per domain (see
   * setPermissionSet). Always present; a null list means no set is
   * known for that domain. Empty in both domains while anonymous. */
  permissionSets: AuthPermissionSets
}

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
  /** Starts a `purpose: 'login'` flow and returns its first step. Never
   * changes the session: the start of a flow is not a session. A 2xx
   * body that is not a valid flow state rejects with `client.protocol`
   * before anything else happens. */
  startFlow(request: AuthnLoginStartBody): Promise<AuthnFlowState>
  /** Starts a flow on the identity already signed in, for `purpose:
   * 'link'`, `'step-up'` or `'enroll'`. Same contract as startFlow:
   * forwards to the server and returns the step, changing nothing. */
  startAuthenticatedFlow(
    request: AuthnLoginStartAuthenticatedBody,
  ): Promise<AuthnFlowState>
  /** Answers the current step of the flow the request is bound to and
   * returns the resulting flow state. This is the one operation that
   * commits a token issuance: a `login` completion (state
   * 'authenticated') opens the session, a `step-up` completion (state
   * 'elevated') re-mints the access token with the principal unchanged,
   * and a `link`/`enroll` completion changes nothing. A login
   * completion re-reads GET /auth/me for the principal; if that read
   * fails the store is cleared and the commit rejects. A token-issuing
   * completion whose generation moved on -- a concurrent commit beat it
   * -- rejects with OperationSupersededError (see the class). */
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
  /** Ends the session: the server revokes it, the store empties and
   * the snapshot returns to anonymous. A logout whose own request is
   * refused still clears locally and rejects the raw ApiError -- a
   * session it could not revoke is no longer trustworthy here either.
   * A completed logout wins over a refresh that resolves after it. */
  logout(): Promise<void>
  /** Silently refreshes the access token with the held refresh token.
   * Resolves true when the store holds a fresh token afterwards;
   * resolves false when there is nothing to refresh, when the held
   * token was refused (the session signs out locally), or when a
   * concurrent commit replaced the session. A transport or server-side
   * failure rethrows the raw ApiError and leaves the held tokens in
   * place. Concurrent calls presenting the same held token share one
   * in-flight request. */
  refresh(): Promise<boolean>
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
 * Rejects a token-issuing commit -- a login flow completion or a
 * step-up completion -- whose own request answered successfully but
 * was superseded by a concurrent commit that landed first. The request
 * itself did not fail (`isApiError(error)` is false for this) and the
 * session is not broken -- some commit's tokens are applied and the
 * session is authenticated under them -- but they are not necessarily
 * THIS caller's, so this caller must not treat its own 2xx as a commit
 * signal: no one-shot side effect (a post-login redirect) may fire for
 * an identity the session is not actually running under. A superseded
 * step-up carries an extra cost the caller must hear: its completion
 * rotated the tokens and consumed, server-side, the challenge it
 * verified, so the caller must never re-offer that same second factor
 * -- only a fresh flow can retry. `snapshot` carries the session's
 * current (winning) state at the moment of rejection.
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
 * GET /auth/me) or carried over (a step-up keeps it): the token
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

function optionalNumber(
  value: unknown,
): number | undefined {
  return typeof value === 'number' ? value : undefined
}

function parseIdentifier(value: unknown): AuthnIdentifier {
  const rec = asObject(value, 'profile identifier is not an object')
  return {
    id: requireString(rec.id, 'profile identifier carries no id'),
    kind: typeof rec.kind === 'string' ? rec.kind : '',
    value: typeof rec.value === 'string' ? rec.value : '',
    verified_at: typeof rec.verified_at === 'string' ? rec.verified_at : '',
    verified_via: typeof rec.verified_via === 'string' ? rec.verified_via : '',
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

/** Validates the GET /auth/me body into the hand-written principal
 * model. `account_id` is the required field; the rest are carried as
 * the wire sends them. */
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
  }
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

/** Validates one step of an open flow into the hand-written step
 * union. `kind` is the discriminator; an unknown kind is a protocol
 * violation. */
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
 * ('done'/'failed') never reaches a 2xx -- GET /auth/flows/current
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

/** Validates a flow reply into the hand-written flow-state union. The
 * open-flow shape is a number `seq` plus a `next` step; a settled
 * completion carries its outcome (and, for a token issuance, the
 * tokens). A body whose `state` is none of these -- or an empty 2xx --
 * is a protocol violation, before any state change. */
function parseFlowState(body: unknown): AuthnFlowState {
  const rec = asObject(body, 'flow reply carries no body document')
  const state = requireString(rec.state, 'flow reply carries no state')
  switch (state) {
    case 'authenticated':
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

function parseMethodList(body: unknown): AuthnMethodList {
  const rec = asObject(body, 'methods 2xx carries no body document')
  if (!Array.isArray(rec.methods)) {
    throw protocolViolation('methods 2xx carries no methods array')
  }
  return {
    methods: rec.methods.map((entry) =>
      requireString(entry, 'methods entry is not a string'),
    ),
  }
}

/** The permission-set shape that carries nothing: every anonymous
 * snapshot and every login transition uses it. */
const NO_PERMISSION_SETS: AuthPermissionSets = {
  tenant: null,
  system: null,
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
 * Creates the session over the caller's access-token store. The store
 * is the single bridge into @speed/api-client: a host that built its
 * client with this same store and refreshAccessToken: () =>
 * session.refresh() gets silent refresh for free -- an expired-token
 * 401 on any request runs one refresh, and the retried request carries
 * the fresh token.
 */
export function createAuthSession(store: AccessTokenStore): AuthSession {
  let snapshot: AuthSnapshot = {
    state: 'anonymous',
    principal: null,
    permissionSets: NO_PERMISSION_SETS,
  }
  // The held refresh token: closure-only, never stored, never exposed.
  let refreshToken: string | null = null
  // The generation guard: a token-issuing commit bumps it; refresh
  // never does. Writes are applied only when the captured generation
  // still holds (see the file header).
  let generation = 0
  // The single-flight refresh: at most one in-flight request per held
  // refresh token.
  let refreshFlight: { held: string; promise: Promise<boolean> } | null = null
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

  /** Clears everything local: the store, the held refresh token and
   * the snapshot. Used by logout and by refresh when the server
   * refuses the held token. The caller decides whether the clear ends
   * its generation: logout clears and then bumps, so a refresh that
   * started under the old generation cannot resurrect the ended
   * session; the refresh-side clears deliberately do NOT bump -- the
   * server's refusal is the session's death, but a commit that started
   * before the refusal and settles after it is the session's
   * legitimate successor and must still be able to commit. */
  function clearLocal(): void {
    store.set(null)
    refreshToken = null
    snapshot = {
      state: 'anonymous',
      principal: null,
      permissionSets: NO_PERMISSION_SETS,
    }
  }

  /** Commits a validated token issuance without touching the principal
   * (a refresh, or a step-up whose principal the caller keeps). The
   * snapshot's permission sets are retained: the same session
   * continues. */
  function commitTokens(issued: IssuedTokens): void {
    store.set(issued.accessToken)
    if (issued.refreshToken !== null) {
      refreshToken = issued.refreshToken
    }
    if (snapshot.state !== 'authenticated') {
      snapshot = { ...snapshot, state: 'authenticated' }
    }
  }

  /** Commits a login completion: the store gets the new access token,
   * the closure the minted refresh token, and the snapshot the freshly
   * read principal with both permission domains cleared (a login opens
   * a fresh server session, so nothing rides over from the previous
   * one). */
  function commitLogin(
    issued: IssuedTokens,
    principal: AuthnPrincipal,
  ): void {
    store.set(issued.accessToken)
    if (issued.refreshToken !== null) {
      refreshToken = issued.refreshToken
    }
    snapshot = {
      state: 'authenticated',
      principal,
      permissionSets: NO_PERMISSION_SETS,
    }
  }

  /** The commit of a login completion: the response is validated first
   * (a contract-violating 2xx rejects even when a newer commit landed
   * -- the caller deserves to know its own request failed), then
   * applied only if no other commit won meanwhile. The principal is
   * read from GET /auth/me with the freshly written token; if that read
   * fails the store is cleared and the commit rejects. */
  async function settleLogin(
    opGeneration: number,
    state: Extract<AuthnFlowState, { state: 'authenticated' }>,
  ): Promise<void> {
    const issued: IssuedTokens = {
      accessToken: state.access_token,
      refreshToken: state.refresh_token,
    }
    if (generation !== opGeneration) {
      throw new OperationSupersededError(snapshot)
    }
    store.set(issued.accessToken)
    let principal: AuthnPrincipal
    try {
      principal = parsePrincipal(await authnProfileGet())
    } catch (error) {
      store.set(null)
      refreshToken = null
      throw error
    }
    if (generation !== opGeneration) {
      throw new OperationSupersededError(snapshot)
    }
    commitLogin(issued, principal)
    generation += 1
    notify()
  }

  /** The commit of a step-up completion: the access token is replaced,
   * the principal and the permission sets are kept (the same session
   * continues), and no GET /auth/me read follows. Validation precedes
   * the generation check, as for a login. */
  function settleElevated(
    opGeneration: number,
    state: Extract<AuthnFlowState, { state: 'elevated' }>,
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
      // session now. If it kept the held token -- a step-up mints no
      // new refresh token -- this refresh's success still consumed the
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

    async startFlow(request: AuthnLoginStartBody): Promise<AuthnFlowState> {
      // A pure flow step: parsed and handed back, changing nothing.
      return parseFlowState(await authnLoginStart(request))
    },

    async startAuthenticatedFlow(
      request: AuthnLoginStartAuthenticatedBody,
    ): Promise<AuthnFlowState> {
      return parseFlowState(await authnLoginStartAuthenticated(request))
    },

    async answerFlow(request: AuthnLoginAnswerBody): Promise<AuthnFlowState> {
      const opGeneration = generation
      const state = parseFlowState(await authnLoginAnswer(request))
      // Only a token-issuing completion changes the session.
      if (state.state === 'authenticated') {
        await settleLogin(opGeneration, state)
      } else if (state.state === 'elevated') {
        settleElevated(opGeneration, state)
      }
      // linked / enrolled / an open step: no state change.
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
      // commit that keeps it (a step-up) continues the very session the
      // revocation ends.
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
      // trustworthy here.
      const replaced =
        generation !== opGeneration &&
        (revokedSessionToken === null || refreshToken !== revokedSessionToken)
      if (!replaced) {
        clearLocal()
        // Bump so that a refresh that started under this generation is
        // dropped when it resolves: a refresh must not resurrect the
        // session this logout ended.
        generation += 1
        notify()
      }
      if (failed) {
        throw failure
      }
    },

    refresh(): Promise<boolean> {
      const held = refreshToken
      if (held === null) {
        // Nothing to refresh; not even a store read -- and certainly no
        // state change.
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
