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

export function principal(
  overrides: Partial<AuthnPrincipal> = {},
): AuthnPrincipal {
  return {
    account_id: 'user-1',
    kind: 'user',
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
    verification: { state: 'none', email: '', since: null },
    ...overrides,
  }
}

export interface MethodDescriptorBody {
  name: string
  kind: string
  protocol?: string
  label?: string
  opens_account?: boolean
  binding_providers?: string[]
  fields?: { name: string; kind: string; required?: boolean; secret?: boolean }[]
}

export function methodList(
  methods: readonly (string | MethodDescriptorBody)[],
  overrides: {
    signup?: boolean
    signupPassword?: boolean
    mfaFactors?: readonly string[]
    humanRequired?: boolean
    humanProvider?: string
    humanSiteKey?: string
    humanPhoneProvider?: string
    humanPhoneRequired?: boolean
    humanPhoneSiteKey?: string
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

export function ssoProviders(
  providers: readonly { id: string; name: string; protocol: string }[] = [],
): { providers: { id: string; name: string; protocol: string }[] } {
  return { providers: providers.map((provider) => ({ ...provider })) }
}

type OpenState =
  | 'first'
  | 'second'
  | 'enroll'
  | 'reprove'
  | 'ready'
  | 'processing'
  | 'signup'

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

export function completeFlow(seq = 2): AuthnFlowState {
  return { seq, state: 'ready', next: { kind: 'complete' } }
}

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

export function linkedCompletion(): AuthnFlowState {
  return { state: 'linked' }
}

export function enrolledCompletion(): AuthnFlowState {
  return { state: 'enrolled' }
}

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

export function apiError(
  status: number,
  code: string,
  retryAfterMs?: number,
): ApiError {
  return new ApiError({
    status,
    code,
    attempts: 1,
    ...(retryAfterMs === undefined ? {} : { retryAfterMs }),
  })
}

interface ScriptedCall {
  path: string
  options?: RequestOptions
}

type Script = Record<string, (call: ScriptedCall) => unknown>

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
    // Own-property only: a prototype-chain key such as 'constructor' must
    // take the missing-handler path, never dispatch onto an inherited
    // function.
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
