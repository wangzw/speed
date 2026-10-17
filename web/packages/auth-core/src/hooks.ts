/**
 * hooks.ts -- the React bindings over the @speed/auth-core session,
 * plus the tenant context the host mounts its current tenant on.
 *
 * The session hooks read one session the host attaches once, at
 * bootstrap: attachSession(session) is the module-level seam, last bind
 * wins (the same discipline as the bindRequestFn seam in
 * @speed/api-sdk). Before any session is attached -- and after a logout
 * -- every hook fails closed: useAuthState reports the anonymous
 * snapshot and usePermission returns false. No hook ever throws because
 * no session is attached.
 *
 * The snapshot is the single subscribable store: attachSession
 * forwards the attached session's notifications to every subscribed
 * component, and the selectors below recompute on every notification
 * (any bump re-renders every hook consumer -- a permission-set change
 * is a snapshot change like any other).
 *
 * Permission checks are pure set lookup, never evaluation: the host
 * attaches /me-derived lists through session.setPermissionSet, the
 * session's survival rules decide which lists a refresh or a step-up
 * keeps, and usePermission answers "is this string in that domain's
 * list" only -- a domain whose set is absent is false, and a 'tenant'
 * check never consults the 'system' list or vice versa. These checks
 * are a UX affordance, not a security boundary: the server authorizes.
 *
 * Feature checks are the same shape over the session's feature state:
 * useFeature is a fail-closed membership lookup over the names the
 * process serves, useFeatureState hands the state itself to a host that
 * must tell "reading", "failed" and "absent" apart, and featurePresence
 * maps it onto route-kit's presence shape. Only hosts read these; the
 * capability UI packages receive the answer through their props or
 * provider components.
 *
 * The tenant is not the session's: the server resolves it per request
 * (from the request host, a subdomain or a path) as a routing fact, and
 * the principal carries no tenant. So the host, which knows its own
 * route, supplies the current tenant through TenantProvider, and
 * useCurrentTenant reads it back -- one host-supplied value, never
 * derived from the principal and never remembered locally. The value
 * feeds the tenant-scoped query-key convention (query-key.ts).
 */

import {
  createContext,
  createElement,
  useContext,
  useMemo,
  useSyncExternalStore,
} from 'react'
import type { ReactElement, ReactNode } from 'react'
import type { FeatureName } from '@speed/api-sdk/features'
import type {
  AuthDomain,
  AuthSession,
  AuthSnapshot,
  FeatureState,
} from './session.js'

/** The snapshot hooks report before any session is attached. A module
 * constant: referentially stable, so useSyncExternalStore never sees
 * spurious changes while nothing is attached. */
const NO_SESSION_SNAPSHOT: AuthSnapshot = {
  state: 'anonymous',
  principal: null,
  impersonating: false,
  permissionSets: { tenant: null, system: null },
  features: { state: 'none' },
}

/** The attached session; null before the first attachSession call. */
let attachedSession: AuthSession | null = null
/** Unsubscribes the notification bridge on the attached session. */
let unattachBridge: (() => void) | null = null
/** The subscribed components (one listener per useSyncExternalStore
 * subscription). */
const listeners = new Set<() => void>()

function notifyListeners(): void {
  for (const listener of listeners) {
    listener()
  }
}

/** The getSnapshot every hook shares: the attached session's current
 * snapshot, or the stable anonymous one. Referentially stable between
 * changes on both paths. */
function getSnapshot(): AuthSnapshot {
  return attachedSession === null
    ? NO_SESSION_SNAPSHOT
    : attachedSession.getSnapshot()
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/**
 * Attaches the session the hooks read. Last bind wins: attaching a new
 * session unsubscribes the bridge on the previous one (its later
 * transitions no longer reach the components) and wakes every
 * subscribed component so it re-reads -- a swap may change the snapshot
 * identity. Attaching the session already attached is a no-op. The
 * hooks fail closed before the first call.
 */
export function attachSession(session: AuthSession): void {
  if (session === attachedSession) {
    return
  }
  if (unattachBridge !== null) {
    unattachBridge()
    unattachBridge = null
  }
  attachedSession = session
  // The bridge forwards every session notification to the components;
  // each listener's re-read then picks up the session's new snapshot.
  unattachBridge = session.subscribe(() => {
    notifyListeners()
  })
  // The newly attached session may already hold a different snapshot
  // than the components last saw (or than the no-session one): wake
  // them so the first render after attach is not stale.
  notifyListeners()
}

/** The snapshot rendered on the server. Server rendering has no
 * session -- attachSession runs in browser bootstrap code, never during
 * a server render -- so the anonymous snapshot is the only answer that
 * cannot mismatch the hydrated client's first paint. The module
 * constant is referentially stable, which useSyncExternalStore
 * requires of the server snapshot across calls. */
function getServerSnapshot(): AuthSnapshot {
  return NO_SESSION_SNAPSHOT
}

/** The full auth snapshot. Re-renders on every snapshot change --
 * state, principal or permission sets. Before attachSession, returns
 * the stable anonymous snapshot. */
export function useAuthState(): AuthSnapshot {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot)
}

/** Whether the principal holds the permission in the domain's
 * host-attached set. Pure set lookup that fails closed: false while
 * anonymous, when the domain's set is absent (null), and when the
 * permission is not in it. The 'tenant' set never satisfies a 'system'
 * check and vice versa. A UX affordance only -- the server authorizes. */
export function usePermission(
  domain: AuthDomain,
  permission: string,
): boolean {
  const snapshot = useAuthState()
  if (snapshot.state !== 'authenticated') {
    return false
  }
  const set = snapshot.permissionSets[domain]
  return set !== null && set.includes(permission)
}

/** Whether the process serves the named feature. A pure set lookup that
 * fails closed: false unless the feature state is `ready` and the set
 * contains the name -- so `none`, `pending`, `failed` and the
 * before-attach state all answer false. The name is a FeatureName, so a
 * misspelled name fails to compile. */
export function useFeature(name: FeatureName): boolean {
  const features = useFeatureState()
  return features.state === 'ready' && features.names.has(name)
}

/** The feature state itself. A host that must distinguish "still
 * reading", "the read failed" and "the feature is absent" reads this.
 * Before attachSession it is `none`. */
export function useFeatureState(): FeatureState {
  return useAuthState().features
}

/** Maps the feature state onto route-kit's presence shape without
 * importing route-kit: `ready` becomes a ready predicate, `failed`
 * becomes failed, and `pending`/`none` both read as pending -- an
 * anonymous caller has no feature state, so the fail-closed direction is
 * "not decided yet", never "everything present". The predicate takes a
 * plain string because the route layer calls it with the names a
 * declaration's `requires` carries; the FeatureName type governs where a
 * name is written, not this lookup. */
export function featurePresence(
  state: FeatureState,
):
  | { readonly state: 'pending' }
  | { readonly state: 'failed' }
  | { readonly state: 'ready'; readonly has: (name: string) => boolean } {
  if (state.state === 'ready') {
    return { state: 'ready', has: (name) => state.names.has(name) }
  }
  if (state.state === 'failed') {
    return { state: 'failed' }
  }
  return { state: 'pending' }
}

/** The tenant context. `undefined` means no provider is mounted (the
 * fail-closed default); any other value -- including null -- is the
 * host-supplied current tenant. */
const TenantContext = createContext<string | null | undefined>(undefined)

/**
 * Mounts the host's current tenant for the hooks and the query-key
 * convention to read. The host derives the value from its own route
 * (the server resolves the tenant per request, the principal does not
 * carry one) and passes it here; this package neither derives it nor
 * remembers a previous value. A null tenant (no tenant in the route)
 * reads back as the fail-closed null from useCurrentTenant.
 */
export function TenantProvider(props: {
  tenantId: string | null
  children: ReactNode
}): ReactElement {
  const { tenantId, children } = props
  return createElement(TenantContext.Provider, { value: tenantId }, children)
}

/** The tenant the host says the principal is acting in, or null when no
 * provider is mounted, the host passed null, or the value is empty.
 *
 * The returned { tenantId } object is referentially stable for a given
 * tenant: it changes identity only when the tenant itself changes, not
 * on re-renders. A hook that embeds the result in a query key or an
 * effect dependency therefore sees one stable key for the whole stay in
 * a tenant, instead of a fresh object every render defeating the
 * memoization -- the identity a host's tenant-namespaced cache key is
 * built from. */
export function useCurrentTenant(): { tenantId: string } | null {
  const tenantId = useContext(TenantContext)
  return useMemo(
    () =>
      typeof tenantId === 'string' && tenantId !== ''
        ? { tenantId }
        : null,
    [tenantId],
  )
}
