/**
 * A controllable session for the demo host's suites.
 *
 * The app's gates read the session snapshot (auth-core's useAuthState), not
 * the session's operations, so a suite needs a snapshot it can set and flip --
 * the authenticated frame, an attached permission set, a session that turns
 * anonymous mid-use -- without driving a real login flow. createTestSession
 * returns an AuthSession whose `set` replaces the snapshot and notifies
 * subscribers (a real session flip); its operation methods reject, since the
 * rendering suites never submit a form.
 */

import type {
  AuthSession,
  AuthSessionListener,
  AuthSnapshot,
} from '@speed/auth-core'

/** The test session: an AuthSession plus the flip control the suites drive. */
export interface TestSession extends AuthSession {
  /** Replaces the snapshot and notifies subscribers, as a session transition does. */
  set(snapshot: AuthSnapshot): void
}

/** The anonymous snapshot: both permission domains unknown. */
export const ANONYMOUS_SNAPSHOT: AuthSnapshot = {
  state: 'anonymous',
  principal: null,
  permissionSets: { tenant: null, system: null },
}

/**
 * The authenticated snapshot with the given permission lists attached. Each
 * domain defaults to null -- an unknown set, the state a host that attaches
 * none leaves the session in.
 */
export function authenticatedSnapshot(options: {
  readonly tenant?: readonly string[] | null
  readonly system?: readonly string[] | null
} = {}): AuthSnapshot {
  return {
    state: 'authenticated',
    principal: {
      account_id: 'account-1',
      kind: '',
      display_name: 'Test User',
      mfa_required: false,
      password_set: true,
      created_at: '',
      attributes: {},
      locale: 'en-US',
      time_zone: 'UTC',
      identifiers: [],
      links: [],
    },
    permissionSets: {
      tenant: options.tenant ?? null,
      system: options.system ?? null,
    },
  }
}

/** Every operation is out of scope for a rendering suite. */
function unused(): never {
  throw new Error('the test session drives no operation')
}

/** A session whose snapshot `set` mutates and notifies; operations reject. */
export function createTestSession(
  initial: AuthSnapshot = ANONYMOUS_SNAPSHOT,
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
    listMethods: unused,
    startFlow: unused,
    startAuthenticatedFlow: unused,
    answerFlow: unused,
    readFlow: unused,
    abandonFlow: unused,
    logout: unused,
    refresh: unused,
    setPermissionSet: unused,
  }
}
