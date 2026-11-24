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

export const LOGIN_METHODS = 'GET /api/v1/auth/methods'
export const LOGIN_START = 'POST /api/v1/auth/flows'
export const LOGIN_ANSWER = 'POST /api/v1/auth/flows/answer'
export const PROFILE_GET = 'GET /api/v1/auth/me'
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
  } = {},
): {
  methods: MethodDescriptorBody[]
  signup: { enabled: boolean; password: { enabled: boolean } }
  mfa: { factors: string[] }
  human: {
    phone: { required: boolean; provider: string; site_key?: string }
    other: { required: boolean; provider: string; site_key?: string }
  }
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
  }
}

type OpenState =
  | 'first'
  | 'second'
  | 'enroll'
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

interface ScriptedCall {
  path: string
  options?: RequestOptions
}

type Script = Record<string, (call: ScriptedCall) => unknown>

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
