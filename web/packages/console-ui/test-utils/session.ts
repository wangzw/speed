/**
 * A minimal session for console-ui's step-up suites.
 *
 * The console's step-up gate classifies a refused write with
 * session.isStepUpShortfall (an auth-core session) and, when it is a
 * shortfall, drives the session's authenticated step-up flow. This helper
 * supplies a session that (a) classifies a 401 exactly as auth-core does
 * (an ApiError 401 while the snapshot is still authenticated), and (b)
 * settles a step-up flow immediately as elevated, so a suite can assert
 * that a refused write opens the dialog and is replayed once elevated --
 * without a real backend or a multi-step flow.
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
    identifiers: [],
    links: [],
  },
  permissionSets: { tenant: null, system: null },
}

/** The anonymous snapshot: no session, no permission sets. */
export const ANONYMOUS_SNAPSHOT: AuthSnapshot = {
  state: 'anonymous',
  principal: null,
  permissionSets: { tenant: null, system: null },
}

const METHODS: AuthnMethodList = {
  methods: [{ name: 'password', kind: 'form' }],
  signup: { enabled: false },
  mfa: { factors: [] },
}

/** The completion a started step-up settles as: the cookie rotated, the
 * login-shaped 'authenticated' body (auth-core narrows it to an elevation
 * for a step-up purpose). */
const ELEVATED: AuthnFlowState = {
  state: 'authenticated',
  session: { id: 'sess-1', expires_at: '2026-10-17T00:00:00Z' },
}

function unused(): never {
  throw new Error('the step-up test session drives no such operation')
}

/**
 * A session with the given initial snapshot, whose step-up flow settles
 * elevated at once. `signOut` flips the snapshot anonymous and notifies,
 * for a suite that models a session ending mid-use.
 */
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
    // The step-up flow settles elevated at once: the dialog fires its
    // onSuccess on the first reply, replaying the refused write.
    startAuthenticatedFlow: () => Promise.resolve(ELEVATED),
    answerFlow: () => Promise.resolve(ELEVATED),
    abandonFlow: () => Promise.resolve(),
    readFlow: () => Promise.resolve(null),
    listMethods: () => Promise.resolve(METHODS),
    discoverSso: () => Promise.resolve([]),
    startFlow: unused,
    logout: unused,
    restore: unused,
    refresh: unused,
    setPermissionSet: unused,
  }
}
