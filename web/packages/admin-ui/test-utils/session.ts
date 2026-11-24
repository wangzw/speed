/**
 * A minimal session for the step-up suites: it classifies a 401 as auth-core
 * does and settles a step-up flow immediately as elevated.
 */

import type {
  AuthSession,
  AuthSessionListener,
  AuthSnapshot,
  AuthnFlowState,
  AuthnMethodList,
} from '@speed/auth-core'
import { isApiError } from '@speed/api-client'

/** The authenticated snapshot: no permission sets attached. */
export const AUTHENTICATED_SNAPSHOT: AuthSnapshot = {
  state: 'authenticated',
  principal: {
    account_id: 'account-1',
    kind: 'user',
    display_name: 'Operator',
    mfa_required: false,
    password_set: true,
    created_at: '2026-10-15T12:00:00Z',
    attributes: {},
    locale: 'zh-CN',
    time_zone: 'UTC',
    color_mode: '',
    identifiers: [],
    links: [],
    verification: { state: 'none', email: '', since: null },
  },
  impersonating: false,
  permissionSets: { tenant: null, system: null },
  features: { state: 'ready', names: new Set() },
}

/** The anonymous snapshot: no session, no permission sets. */
export const ANONYMOUS_SNAPSHOT: AuthSnapshot = {
  state: 'anonymous',
  principal: null,
  impersonating: false,
  permissionSets: { tenant: null, system: null },
  features: { state: 'none' },
}

const METHODS: AuthnMethodList = {
  methods: [{ name: 'password', kind: 'form' }],
  signup: { enabled: false, password: { enabled: false } },
  mfa: { factors: [] },
  human: {
    phone: { required: false, provider: 'off' },
    other: { required: false, provider: 'off' },
  },
  password_reset: { enabled: false, methods: [] },
}

/** The completion a started step-up settles as: the cookie rotated. */
const ELEVATED: AuthnFlowState = {
  state: 'authenticated',
  session: { id: 'sess-1', expires_at: '2026-10-17T00:00:00Z' },
}

function unused(): never {
  throw new Error('the step-up test session drives no such operation')
}

/** A session whose step-up flow settles elevated at once; `signOut` ends it mid-use. */
export interface StepUpTestSession extends AuthSession {
  /** Flip the snapshot to anonymous, as a session that ended mid-use. */
  signOut(): void
}

export function createStepUpSession(
  initial: AuthSnapshot = AUTHENTICATED_SNAPSHOT,
): StepUpTestSession {
  let snapshot = initial
  const listeners = new Set<AuthSessionListener>()
  const notify = (next: AuthSnapshot): void => {
    snapshot = next
    for (const listener of listeners) {
      listener(next)
    }
  }
  return {
    getSnapshot: () => snapshot,
    subscribe: (listener) => {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
    signOut: () => notify(ANONYMOUS_SNAPSHOT),
    isStepUpShortfall: (error) =>
      isApiError(error) && error.status === 401 && snapshot.state === 'authenticated',
    // The step-up flow settles elevated at once, so the first reply replays the write.
    startAuthenticatedFlow: () => Promise.resolve(ELEVATED),
    answerFlow: () => Promise.resolve(ELEVATED),
    abandonFlow: () => Promise.resolve(),
    readFlow: () => Promise.resolve(null),
    listMethods: () => Promise.resolve(METHODS),
    discoverSso: () => Promise.resolve([]),
    register: unused,
    startFlow: unused,
    logout: unused,
    restore: unused,
    // No suite opens an identity transition; the window exists for a host that rotates.
    beginIdentityTransition: () => () => undefined,
    refresh: unused,
    retryFeatures: () => Promise.resolve(),
    setPermissionSet: unused,
    setDisplayName: unused,
  }
}
