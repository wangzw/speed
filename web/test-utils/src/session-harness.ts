/**
 * session-harness.ts -- the shared scripting harness for tests that
 * drive an @speed/auth-core session. A fake request function bound
 * through the @speed/api-sdk runtime seam (bindRequestFn, the same seam
 * a host's real client uses) drives a real session over a fresh memory
 * store; tests assert observable state only: the store's token,
 * getSnapshot, subscriber notifications, the request script's bodies and
 * the raw ApiErrors a failed operation rejects.
 *
 * The harness covers the whole authn flow surface -- the login flow
 * (start, answer, current, abandon), the authenticated-purpose start
 * (link / step-up / enroll), the methods listing, logout, the token
 * endpoint a refresh uses and the /me profile read a login completion
 * makes -- so a package's suite scripts only the operations its surface
 * drives. The endpoint path constants are literal request keys; a test
 * that scripts a body shape writes it with the builder the constant's
 * reply needs (openFlow, authenticatedCompletion, tokenResponse, ...).
 *
 * The response builders return the snake_case bodies the wire carries,
 * exactly as the server sends them; the session's runtime guards
 * validate them, so an override that drops a required field is what a
 * protocol-violation test scripts.
 */

import {
  ApiError,
  createMemoryAccessTokenStore,
} from '@speed/api-client'
import type {
  AccessTokenStore,
  RequestFn,
  RequestOptions,
} from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { createAuthSession } from '@speed/auth-core'
import type {
  AuthSession,
  AuthSnapshot,
  AuthnFlowState,
  AuthnPrincipal,
  AuthnStep,
} from '@speed/auth-core'

// The generated operations pass their spec path verbatim to the bound
// request function, so a constant is the url the operation declares.
export const LOGIN_METHODS = 'GET /auth/methods'
export const LOGIN_START = 'POST /auth/flows'
export const LOGIN_START_AUTHENTICATED = 'POST /auth/flows/authenticated'
export const LOGIN_ANSWER = 'POST /auth/flows/answer'
export const LOGIN_CURRENT = 'GET /auth/flows/current'
export const LOGIN_ABANDON = 'DELETE /auth/flows/current'
export const LOGOUT = 'POST /auth/logout'
export const TOKEN_ISSUE = 'POST /auth/token'
export const PROFILE_GET = 'GET /auth/me'

/** The GET /auth/me profile the session reads for the principal after a
 * login completion. */
export function principal(
  overrides: Partial<AuthnPrincipal> = {},
): AuthnPrincipal {
  return {
    account_id: 'user-1',
    kind: 'person',
    display_name: 'Ada',
    mfa_required: false,
    password_set: true,
    created_at: '2026-01-01T00:00:00Z',
    attributes: {},
    locale: 'en-US',
    time_zone: 'UTC',
    identifiers: [],
    links: [],
    ...overrides,
  }
}

/** The GET /auth/methods body: the deployment's enabled method names. */
export function methodList(methods: readonly string[]): {
  methods: readonly string[]
} {
  return { methods: [...methods] }
}

type OpenState = 'first' | 'second' | 'enroll' | 'ready' | 'processing'

/** An open flow reply (the flow is awaiting the next answer). The
 * default is a first-factor challenge asking for identifier and
 * password; override `next` for another step or `state` for another
 * step of the machine. */
export function openFlow(
  overrides: {
    seq?: number
    state?: OpenState
    next?: AuthnStep
  } = {},
): AuthnFlowState {
  return {
    seq: overrides.seq ?? 1,
    state: overrides.state ?? 'first',
    next: overrides.next ?? {
      kind: 'challenge',
      fields: [
        { name: 'identifier', kind: 'text' },
        { name: 'password', kind: 'password', secret: true },
      ],
    },
  }
}

/** The flow reply a submit step reaches at the ready state: the client
 * answers it with `step: 'complete'`. */
export function completeFlow(seq = 2): AuthnFlowState {
  return { seq, state: 'ready', next: { kind: 'complete' } }
}

/** A token-issuing login completion. */
export function authenticatedCompletion(
  overrides: {
    access_token?: string
    refresh_token?: string
    expires_in?: number
  } = {},
): AuthnFlowState {
  return {
    state: 'authenticated',
    token_type: 'Bearer',
    access_token: overrides.access_token ?? 'access-1',
    expires_in: overrides.expires_in ?? 3600,
    refresh_token: overrides.refresh_token ?? 'refresh-1',
  }
}

/** A step-up completion: an elevated access token, no new refresh
 * token. */
export function elevatedCompletion(
  overrides: { access_token?: string; expires_in?: number } = {},
): AuthnFlowState {
  return {
    state: 'elevated',
    token_type: 'Bearer',
    access_token: overrides.access_token ?? 'access-elevated',
    expires_in: overrides.expires_in ?? 3600,
  }
}

/** A link flow completion: identity bound, login state untouched. */
export function linkedCompletion(): AuthnFlowState {
  return { state: 'linked' }
}

/** An enroll flow completion: factor registered, login state
 * untouched. */
export function enrolledCompletion(): AuthnFlowState {
  return { state: 'enrolled' }
}

/** The POST /auth/token body a refresh gets: the RFC 6749 §5.1 shape,
 * the refresh token rotated. */
export function tokenResponse(
  overrides: {
    access_token?: string
    refresh_token?: string
    expires_in?: number
  } = {},
): {
  access_token: string
  token_type: string
  expires_in: number
  refresh_token: string
} {
  return {
    access_token: overrides.access_token ?? 'access-2',
    token_type: 'Bearer',
    expires_in: overrides.expires_in ?? 3600,
    refresh_token: overrides.refresh_token ?? 'refresh-2',
  }
}

export function apiError(status: number, code: string): ApiError {
  return new ApiError({ status, code, attempts: 1 })
}

/** The scripted call a fake request function receives. */
interface ScriptedCall {
  path: string
  options?: RequestOptions
}

/** A fake endpoint: the handler's return value is the resolved
 * response body; throwing rejects the request with the thrown error
 * (tests throw ApiError to script HTTP failures). */
type Script = Record<string, (call: ScriptedCall) => unknown>

interface Call {
  method: string
  path: string
  options?: RequestOptions
}

export interface Harness {
  session: AuthSession
  store: AccessTokenStore
  calls: Call[]
}

/** Binds a scripted fake request function and creates the session over
 * a fresh memory store. Each call binds anew: the runtime seam is
 * last-bind-wins by contract. */
export function makeHarness(script: Script = {}): Harness {
  const store = createMemoryAccessTokenStore()
  const session = createAuthSession(store)
  const calls: Call[] = []
  const requestFn: RequestFn = (async <T>(
    path: string,
    options?: RequestOptions,
  ): Promise<T> => {
    const method = options?.method ?? 'GET'
    const key = `${method} ${path}`
    calls.push({ method, path, options })
    // Read the handler only when the key is an own property of the
    // script object: a prototype-chain key such as 'constructor' or
    // 'toString' must take the same missing-handler path as any other
    // unknown key, never dispatch onto an inherited function.
    const handler = Object.hasOwn(script, key) ? script[key] : undefined
    if (handler === undefined) {
      throw new Error(`no scripted handler for ${key}`)
    }
    return (await handler({ path, options })) as T
  }) as RequestFn
  bindRequestFn(requestFn)
  return { session, store, calls }
}

export function snapshotLog(session: AuthSession): AuthSnapshot[] {
  const seen: AuthSnapshot[] = []
  session.subscribe((snapshot) => {
    seen.push(snapshot)
  })
  return seen
}

export async function captureRejection(
  promise: Promise<unknown>,
): Promise<unknown> {
  try {
    await promise
  } catch (error) {
    return error
  }
  throw new Error('expected the promise to reject')
}
