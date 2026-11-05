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
  FeatureState,
} from '@speed/auth-core'
import { FEATURE_NAMES } from '@speed/api-sdk/features'

/** The method document the sign-in surface renders in these suites: the
 * password channel, the only one the skeleton offers. */
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

/** The test session: an AuthSession plus the flip control the suites drive. */
export interface TestSession extends AuthSession {
  /** Replaces the snapshot and notifies subscribers, as a session transition does. */
  set(snapshot: AuthSnapshot): void
}

/** The anonymous snapshot: both permission domains unknown. */
export const ANONYMOUS_SNAPSHOT: AuthSnapshot = {
  state: 'anonymous',
  principal: null,
  impersonating: false,
  permissionSets: { tenant: null, system: null },
  features: { state: 'none' },
}

/**
 * The authenticated snapshot with the given permission lists attached. Each
 * domain defaults to null -- an unknown set, the state a host that attaches
 * none leaves the session in. The feature state defaults to the platform
 * vocabulary a baseline deployment serves: every gated platform surface
 * present. A suite that needs a narrowed document, a pending read, a failed
 * one, or an application-owned feature such as the personal tenant passes its
 * own.
 */
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
      created_at: '',
      attributes: {},
      color_mode: '',
      locale: 'en-US',
      time_zone: 'UTC',
      identifiers: [],
      links: [],
      verification: { state: 'none', email: '', since: '' },
    },
    impersonating: false,
    permissionSets: {
      tenant: options.tenant ?? null,
      system: options.system ?? null,
    },
    // The suite's session serves the platform vocabulary a baseline
    // deployment serves, less the personal tenant's static feature: a suite
    // that exercises the personal module (its registration landing, for one)
    // passes its own feature state.
    features: options.features ?? {
      state: 'ready',
      names: new Set(FEATURE_NAMES.filter((name) => name !== 'org.personal')),
    },
  }
}

/** A ready feature state carrying exactly these names. */
export function readyFeatures(...names: string[]): FeatureState {
  return { state: 'ready', names: new Set(names) }
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
  methods: AuthnMethodList | (() => Promise<AuthnMethodList>) = FORM_METHODS,
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
    // The rendering suites do not rotate credentials, so the identity
    // transition is a window that never opens: the release is a no-op.
    beginIdentityTransition: () => () => undefined,
    refresh: rejecting,
    retryFeatures: rejecting,
    isStepUpShortfall: () => false,
    // Host-attached permissions are out of scope here: the suites set the
    // snapshot's permission lists directly, so attaching is a no-op rather
    // than a rejection the host would have to catch.
    setPermissionSet: () => undefined,
    // The local mirror a host uses after a profile write: replace the
    // authenticated principal's display name and notify, exactly as the
    // real session does. Anonymous snapshots have no principal to name.
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
