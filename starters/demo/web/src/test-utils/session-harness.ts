/**
 * The scripted-session harness the host's suites drive an @speed/auth-core
 * session with. A fake request function bound through the @speed/api-sdk
 * runtime seam (bindRequestFn, the same seam a host's real client uses) drives
 * a real session over a fresh memory store; tests assert observable state only:
 * the store's token, snapshots, the request script's bodies and the raw
 * ApiErrors a failed operation rejects.
 *
 * The harness carries the operations the host's sign-in and feature-gating
 * suites drive -- the login start, answer and methods listing, the /me profile
 * read and the /api/v1/features read a login commit or anonymous-start restore
 * makes. The endpoint path constants are literal request keys; a test that
 * scripts a body shape writes it with the builder the constant's reply needs.
 *
 * The response builders return the snake_case bodies the wire carries, exactly
 * as the server sends them; the session's runtime guards validate them, so an
 * override that drops a required field is what a protocol-violation test
 * scripts.
 */

import { createMemoryAccessTokenStore } from '@speed/api-client'
import type {
  AccessTokenStore,
  RequestFn,
  RequestOptions,
} from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { createAuthSession } from '@speed/auth-core'
import type {
  AuthSession,
  AuthnFlowState,
  AuthnPrincipal,
  AuthnStep,
} from '@speed/auth-core'

// The generated operations pass their spec path verbatim to the bound request
// function, so a constant is the url the operation declares.
export const LOGIN_METHODS = 'GET /api/v1/auth/methods'
export const LOGIN_START = 'POST /api/v1/auth/flows'
export const LOGIN_ANSWER = 'POST /api/v1/auth/flows/answer'
export const PROFILE_GET = 'GET /api/v1/auth/me'
export const FEATURES_GET = 'GET /api/v1/features'

/** The GET /api/v1/auth/me profile the session reads for the principal after a
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
    verification: { state: 'none', email: '', since: '' },
    ...overrides,
  }
}

/** One method in a GET /api/v1/auth/methods body: the required `name` and `kind`
 * plus the optional descriptor fields the wire may carry. */
export interface MethodDescriptorBody {
  name: string
  kind: string
  protocol?: string
  label?: string
  opens_account?: boolean
  fields?: { name: string; kind: string; required?: boolean; secret?: boolean }[]
}

/** The GET /api/v1/auth/methods body: the deployment's enabled method
 * descriptors, whether a first login may open an account, and the second
 * factor kinds offered. A plain string is shorthand for a `form`
 * descriptor of that name. */
export function methodList(
  methods: readonly (string | MethodDescriptorBody)[],
  overrides: {
    signup?: boolean
    signupPassword?: boolean
    mfaFactors?: readonly string[]
  } = {},
): {
  methods: MethodDescriptorBody[]
  signup: { enabled: boolean; password: { enabled: boolean } }
  mfa: { factors: string[] }
} {
  return {
    methods: methods.map((entry) =>
      typeof entry === 'string' ? { name: entry, kind: 'form' } : entry,
    ),
    signup: {
      enabled: overrides.signup ?? false,
      password: { enabled: overrides.signupPassword ?? false },
    },
    mfa: { factors: [...(overrides.mfaFactors ?? [])] },
  }
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

/** The scripted call a fake request function receives. */
interface ScriptedCall {
  path: string
  options?: RequestOptions
}

/** A fake endpoint: the handler's return value is the resolved
 * response body; throwing rejects the request with the thrown error
 * (tests throw ApiError to script HTTP failures). */
type Script = Record<string, (call: ScriptedCall) => unknown>

/** The GET /api/v1/features answer every harness provides unless the test
 * scripts its own: a ready state with no names. Every login commit and
 * every anonymous-start restore reads the document, so a real server
 * always answers it; a test that needs names (or a failed read) scripts
 * the key explicitly. */
const DEFAULT_SCRIPT: Script = {
  [FEATURES_GET]: () => ({ features: [] }),
}

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
    // unknown key, never dispatch onto an inherited function. The
    // default document answers keys the test did not script.
    const handler = Object.hasOwn(script, key)
      ? script[key]
      : Object.hasOwn(DEFAULT_SCRIPT, key)
        ? DEFAULT_SCRIPT[key]
        : undefined
    if (handler === undefined) {
      throw new Error(`no scripted handler for ${key}`)
    }
    return (await handler({ path, options })) as T
  }) as RequestFn
  bindRequestFn(requestFn)
  return { session, store, calls }
}
