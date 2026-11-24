import type {
  AuthSession,
  AuthSessionListener,
  AuthSnapshot,
  AuthnMethodList,
  FeatureState,
} from '@speed/auth-core'
import { FEATURE_NAMES } from '@speed/api-sdk/features'

const FORM_METHODS: AuthnMethodList = {
  methods: [{ name: 'password', kind: 'form' }],
  signup: { enabled: false, password: { enabled: false } },
  mfa: { factors: [] },
  human: {
    phone: { required: false, provider: 'off' },
    other: { required: false, provider: 'off' },
  },
  password_reset: { enabled: false, methods: [] },
}

export interface TestSession extends AuthSession {
  set(snapshot: AuthSnapshot): void
}

export const ANONYMOUS_SNAPSHOT: AuthSnapshot = {
  state: 'anonymous',
  principal: null,
  impersonating: false,
  permissionSets: { tenant: null, system: null },
  features: { state: 'none' },
}

export function authenticatedSnapshot(options: {
  readonly tenant?: readonly string[] | null
  readonly system?: readonly string[] | null
  readonly features?: FeatureState
} = {}): AuthSnapshot {
  return {
    state: 'authenticated',
    principal: {
      account_id: 'account-1',
      kind: '',
      display_name: 'Test User',
      mfa_required: false,
      password_set: true,
      created_at: null,
      attributes: {},
      color_mode: '',
      locale: 'en-US',
      time_zone: 'UTC',
      identifiers: [],
      links: [],
      verification: { state: 'none', email: '', since: null },
    },
    impersonating: false,
    permissionSets: {
      tenant: options.tenant ?? null,
      system: options.system ?? null,
    },
    features: options.features ?? {
      state: 'ready',
      names: new Set(FEATURE_NAMES.filter((name) => name !== 'org.personal')),
    },
  }
}

export function readyFeatures(...names: string[]): FeatureState {
  return { state: 'ready', names: new Set(names) }
}

function rejecting(): Promise<never> {
  return Promise.reject(new Error('the test session drives no operation'))
}

export type FeatureRetryScript = () => Promise<FeatureState>

export function createTestSession(
  initial: AuthSnapshot = ANONYMOUS_SNAPSHOT,
  methods: AuthnMethodList | (() => Promise<AuthnMethodList>) = FORM_METHODS,
  featureRetry?: FeatureRetryScript,
): TestSession {
  let snapshot = initial
  const listeners = new Set<AuthSessionListener>()
  return {
    getSnapshot: () => snapshot,
    subscribe: (listener) => {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
    set: (next) => {
      snapshot = next
      for (const listener of listeners) {
        listener(next)
      }
    },
    listMethods: () =>
      typeof methods === 'function' ? methods() : Promise.resolve(methods),
    discoverSso: () => Promise.resolve([]),
    register: rejecting,
    startFlow: rejecting,
    startAuthenticatedFlow: rejecting,
    answerFlow: rejecting,
    readFlow: rejecting,
    abandonFlow: rejecting,
    logout: rejecting,
    restore: rejecting,
    beginIdentityTransition: () => () => undefined,
    refresh: rejecting,
    retryFeatures: () => {
      if (snapshot.state !== 'authenticated' || snapshot.principal === null) {
        return Promise.resolve()
      }
      const previous = snapshot.features
      snapshot = { ...snapshot, features: { state: 'pending' } }
      for (const listener of listeners) {
        listener(snapshot)
      }
      const settle =
        featureRetry === undefined
          ? Promise.resolve(previous)
          : featureRetry()
      return settle.then((next) => {
        if (snapshot.state !== 'authenticated' || snapshot.principal === null) {
          return
        }
        snapshot = { ...snapshot, features: next }
        for (const listener of listeners) {
          listener(snapshot)
        }
      })
    },
    isStepUpShortfall: () => false,
    setPermissionSet: () => undefined,
    setDisplayName: (name) => {
      if (snapshot.state !== 'authenticated' || snapshot.principal === null) {
        return
      }
      snapshot = {
        ...snapshot,
        principal: { ...snapshot.principal, display_name: name },
      }
      for (const listener of listeners) {
        listener(snapshot)
      }
    },
  }
}
