/**
 * A controllable session for the demo host's suites.
 *
 * The app's gates read the session snapshot (auth-core's useAuthState), not
 * the session's operations, so a suite needs a snapshot it can set and flip --
 * the authenticated frame, an attached permission set, a session that turns
 * anonymous mid-use -- without driving a real login flow. createTestSession
 * returns an AuthSession whose `set` replaces the snapshot and notifies
 * subscribers (a real session flip). Its flow operations reject -- the
 * rendering suites never submit a form -- while the reads the sign-in
 * surface makes on mount (the method discovery, the enterprise provider
 * discovery) resolve a small document so the surface renders.
 */

import type {
  AuthSession,
  AuthSessionListener,
  AuthSnapshot,
  AuthnMethodList,
} from '@speed/auth-core'
import { FEATURE_NAMES } from '@speed/api-sdk/features'

/** The method document the sign-in surface renders in these suites: the
 * password channel, the only one the skeleton offers. */
const FORM_METHODS: AuthnMethodList = {
  methods: [{ name: 'password', kind: 'form' }],
  signup: { enabled: false },
  mfa: { factors: [] },
}

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
  features: { state: 'none' },
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
    // The suite's session serves the whole platform vocabulary, so the
    // feature-gated pages and controls are present unless a test narrows
    // the set itself.
    features: { state: 'ready', names: new Set(FEATURE_NAMES) },
  }
}

/**
 * A rejected flow operation: every session operation is out of scope for a
 * rendering suite. It REJECTS rather than throwing synchronously, because the
 * views await or `.catch` these calls (`void session.abandonFlow().catch(...)`,
 * the step-up flow's cleanup) -- a synchronous throw would escape past the
 * handler the call expects.
 */
function rejecting(): Promise<never> {
  return Promise.reject(new Error('the test session drives no operation'))
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
    // The sign-in surface reads the deployment's methods on mount and the
    // enterprise entry its providers; the flows themselves stay out of
    // scope for a rendering suite, so every flow operation rejects.
    listMethods: () => Promise.resolve(FORM_METHODS),
    discoverSso: () => Promise.resolve([]),
    startFlow: rejecting,
    startAuthenticatedFlow: rejecting,
    answerFlow: rejecting,
    readFlow: rejecting,
    abandonFlow: rejecting,
    logout: rejecting,
    restore: rejecting,
    refresh: rejecting,
    retryFeatures: rejecting,
    isStepUpShortfall: () => false,
    // Host-attached permissions are out of scope here: the suites set the
    // snapshot's permission lists directly, so attaching is a no-op rather
    // than a rejection the host would have to catch.
    setPermissionSet: () => undefined,
  }
}
