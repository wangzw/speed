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
 * endpoint a token refresh uses, the session read a restore makes, the
 * /me profile read a login completion makes and the /api/v1/features read a
 * login commit or anonymous-start restore makes -- so a package's suite
 * scripts only the operations its surface drives. The feature document
 * has a default answer (ready, no names); script FEATURES_GET to give it
 * names or make it fail. The endpoint path constants are literal request
 * keys; a test that scripts a body shape writes it with the builder the
 * constant's reply needs (openFlow, sessionCompletion,
 * authenticatedCompletion, tokenResponse, ...).
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
export const LOGIN_METHODS = 'GET /api/v1/auth/methods'
export const LOGIN_START = 'POST /api/v1/auth/flows'
export const LOGIN_START_AUTHENTICATED = 'POST /api/v1/auth/flows/authenticated'
export const LOGIN_ANSWER = 'POST /api/v1/auth/flows/answer'
export const LOGIN_CURRENT = 'GET /api/v1/auth/flows/current'
export const LOGIN_ABANDON = 'DELETE /api/v1/auth/flows/current'
export const HUMAN_CHALLENGE = 'POST /api/v1/auth/human/challenge'
export const CAPTCHA_CHALLENGE =
  'POST /api/v1/auth/human/captcha/challenge'
export const SSO_DISCOVER = 'POST /api/v1/auth/sso/discover'
export const LOGOUT = 'POST /api/v1/auth/logout'
export const TOKEN_ISSUE = 'POST /api/v1/auth/token'
export const PROFILE_GET = 'GET /api/v1/auth/me'
export const SESSION_GET = 'GET /api/v1/auth/session'
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
    color_mode: '',
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
  binding_providers?: string[]
  fields?: { name: string; kind: string; required?: boolean; secret?: boolean }[]
}

/** The GET /api/v1/auth/methods body: the deployment's enabled method
 * descriptors, whether a first login may open an account, the second
 * factor kinds offered, and the anonymous password reset capability. A
 * plain string is shorthand for a `form` descriptor of that name. */
export function methodList(
  methods: readonly (string | MethodDescriptorBody)[],
  overrides: {
    signup?: boolean
    signupPassword?: boolean
    mfaFactors?: readonly string[]
    /** The mechanism both surfaces run; the per-surface overrides below
     * narrow one of them. */
    humanRequired?: boolean
    humanProvider?: string
    humanSiteKey?: string
    /** The phone surface alone: a send that delivers a message to a phone
     * number. Unset members fall back to the shared overrides above. */
    humanPhoneProvider?: string
    humanPhoneRequired?: boolean
    humanPhoneSiteKey?: string
    /** The other surface alone: every send that does not address a phone
     * number. Unset members fall back to the shared overrides above. */
    humanOtherProvider?: string
    humanOtherRequired?: boolean
    humanOtherSiteKey?: string
    passwordReset?: boolean
    passwordResetMethods?: readonly string[]
  } = {},
): {
  methods: MethodDescriptorBody[]
  signup: { enabled: boolean; password: { enabled: boolean } }
  mfa: { factors: string[] }
  human: {
    phone: { required: boolean; provider: string; site_key?: string }
    other: { required: boolean; provider: string; site_key?: string }
  }
  password_reset: { enabled: boolean; methods: string[] }
} {
  const surface = (
    provider: string | undefined,
    required: boolean | undefined,
    siteKey: string | undefined,
  ): { required: boolean; provider: string; site_key?: string } => ({
    required: required ?? overrides.humanRequired ?? false,
    provider: provider ?? overrides.humanProvider ?? 'off',
    ...(siteKey === undefined && overrides.humanSiteKey === undefined
      ? {}
      : { site_key: siteKey ?? overrides.humanSiteKey }),
  })
  return {
    methods: methods.map((entry) =>
      typeof entry === 'string' ? { name: entry, kind: 'form' } : entry,
    ),
    signup: {
      enabled: overrides.signup ?? false,
      password: { enabled: overrides.signupPassword ?? false },
    },
    mfa: { factors: [...(overrides.mfaFactors ?? [])] },
    human: {
      phone: surface(
        overrides.humanPhoneProvider,
        overrides.humanPhoneRequired,
        overrides.humanPhoneSiteKey,
      ),
      other: surface(
        overrides.humanOtherProvider,
        overrides.humanOtherRequired,
        overrides.humanOtherSiteKey,
      ),
    },
    password_reset: {
      enabled: overrides.passwordReset ?? false,
      methods: [...(overrides.passwordResetMethods ?? [])],
    },
  }
}

/** The POST /api/v1/auth/human/challenge body: a proof-of-work challenge the
 * client solves. The default challenge is 16 base64url-encoded zero bytes
 * and a difficulty low enough to solve in a test. */
export function challengeResponse(
  overrides: {
    challenge?: string
    difficulty?: number
    expires_in?: number
  } = {},
): {
  provider: string
  algorithm: string
  challenge: string
  difficulty: number
  expires_in: number
} {
  return {
    provider: 'pow',
    algorithm: 'sha256',
    challenge: overrides.challenge ?? 'AAAAAAAAAAAAAAAAAAAAAA',
    difficulty: overrides.difficulty ?? 4,
    expires_in: overrides.expires_in ?? 300,
  }
}

/** The POST /api/v1/auth/human/captcha/challenge body: one self-hosted
 * captcha challenge of the requested form. Only the media of the form it
 * names is carried, as the server answers it. */
export function captchaChallenge(
  overrides: {
    form?: 'image' | 'slider' | 'audio'
    instruction?: string
    id?: string
    image_url?: string
    background_url?: string
    piece_url?: string
    audio_url?: string
    expires_in?: number
  } = {},
): {
  provider: string
  form: 'image' | 'slider' | 'audio'
  instruction: string
  id: string
  image_url?: string
  background_url?: string
  piece_url?: string
  audio_url?: string
  expires_in: number
} {
  const form = overrides.form ?? 'image'
  const instruction =
    overrides.instruction ??
    (form === 'slider'
      ? 'slide-the-piece'
      : form === 'audio'
        ? 'type-the-digits-you-hear'
        : 'type-the-digits')
  return {
    provider: 'captcha',
    form,
    instruction,
    id: overrides.id ?? 'Y2FwdGNoYS1jaGFsbGVuZ2U',
    ...(form === 'image'
      ? { image_url: overrides.image_url ?? '/api/v1/auth/human/captcha/image/1' }
      : {}),
    ...(form === 'slider'
      ? {
          background_url:
            overrides.background_url ??
            '/api/v1/auth/human/captcha/background/1',
          piece_url:
            overrides.piece_url ?? '/api/v1/auth/human/captcha/piece/1',
        }
      : {}),
    ...(form === 'audio'
      ? { audio_url: overrides.audio_url ?? '/api/v1/auth/human/captcha/audio/1' }
      : {}),
    expires_in: overrides.expires_in ?? 300,
  }
}

/** The POST /api/v1/auth/sso/discover body: the enterprise sign-in entries an
 * email's domain maps to, always the same shape (empty when nothing
 * matches). */
export function ssoProviders(
  providers: readonly { id: string; name: string; protocol: string }[] = [],
): { providers: { id: string; name: string; protocol: string }[] } {
  return { providers: providers.map((provider) => ({ ...provider })) }
}

type OpenState = 'first' | 'second' | 'enroll' | 'ready' | 'processing' | 'signup'

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

/** A cookie-delivered completion: the `AuthnLoginSession` body the
 * server answers with its Set-Cookie on a `delivery: 'session'` login or
 * step-up. */
export function sessionCompletion(
  overrides: { id?: string; expires_at?: string } = {},
): AuthnFlowState {
  return {
    state: 'authenticated',
    session: {
      id: overrides.id ?? 'session-1',
      expires_at: overrides.expires_at ?? '2026-01-01T01:00:00Z',
    },
  }
}

/** The GET /api/v1/auth/session body. Defaults to the anonymous answer (nobody
 * signed in); pass `{authenticated: true, ...}` for a signed-in session. */
export function sessionView(
  overrides: {
    authenticated?: boolean
    session_id?: string
    account_id?: string
    impersonating?: boolean
    methods?: string[]
    assurance?: 'none' | 'single' | 'multi'
    auth_time?: string
    provider?: string
  } = {},
): Record<string, unknown> {
  const authenticated = overrides.authenticated ?? false
  if (!authenticated) {
    return { authenticated: false }
  }
  return {
    authenticated: true,
    session_id: overrides.session_id ?? 'session-1',
    account_id: overrides.account_id ?? 'user-1',
    methods: overrides.methods ?? ['pwd'],
    assurance: overrides.assurance ?? 'single',
    auth_time: overrides.auth_time ?? '2026-01-01T00:00:00Z',
    ...(overrides.impersonating === undefined
      ? {}
      : { impersonating: overrides.impersonating }),
    ...(overrides.provider === undefined
      ? {}
      : { provider: overrides.provider }),
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

/** The POST /api/v1/auth/token body a refresh gets: the RFC 6749 §5.1 shape,
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

/** The GET /api/v1/features answer every harness provides unless the test
 * scripts its own: a ready state with no names. Every login commit and
 * every anonymous-start restore reads the document, so a real server
 * always answers it; a test that needs names (or a failed read) scripts
 * the key explicitly. The method list answers the off human gate the same
 * way: a surface that reads discovery before a send gets a real document
 * without every test having to script one. */
const DEFAULT_SCRIPT: Script = {
  [FEATURES_GET]: () => ({ features: [] }),
  [LOGIN_METHODS]: () => methodList([]),
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

/** Binds a scripted fake request function and creates a store-less
 * session -- the cookie-delivered path: no AccessTokenStore is passed,
 * so a token completion would not be persisted and the session relies on
 * the ambient cookie (which the fake request function knows nothing of).
 * The returned store is a fresh, unused memory store, present so a
 * caller can assert the cookie path never writes a token. */
export function makeCookieHarness(script: Script = {}): Harness {
  const store = createMemoryAccessTokenStore()
  const session = createAuthSession()
  const calls: Call[] = []
  const requestFn: RequestFn = (async <T>(
    path: string,
    options?: RequestOptions,
  ): Promise<T> => {
    const method = options?.method ?? 'GET'
    const key = `${method} ${path}`
    calls.push({ method, path, options })
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
