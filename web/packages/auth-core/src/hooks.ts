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

const NO_SESSION_SNAPSHOT: AuthSnapshot = {
  state: 'anonymous',
  principal: null,
  impersonating: false,
  permissionSets: { tenant: null, system: null },
  features: { state: 'none' },
}

let attachedSession: AuthSession | null = null
let unattachBridge: (() => void) | null = null
const listeners = new Set<() => void>()

function notifyListeners(): void {
  for (const listener of listeners) {
    listener()
  }
}

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
 * Attaches the session the hooks read; last bind wins, and the hooks fail
 * closed before the first call.
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
  unattachBridge = session.subscribe(() => {
    notifyListeners()
  })
  notifyListeners()
}

function getServerSnapshot(): AuthSnapshot {
  return NO_SESSION_SNAPSHOT
}

/** The full auth snapshot; re-renders on every snapshot change and is anonymous before attachSession. */
export function useAuthState(): AuthSnapshot {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot)
}

/**
 * Whether the principal holds the permission in the domain's host-attached
 * set. Fail-closed, and a UX affordance only -- the server authorizes.
 */
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

/**
 * Whether the process serves the named feature. Fail-closed: false unless the
 * feature state is `ready` and the set contains the name.
 */
export function useFeature(name: FeatureName): boolean {
  const features = useFeatureState()
  return features.state === 'ready' && features.names.has(name)
}

/** The feature state itself; `none` before attachSession. */
export function useFeatureState(): FeatureState {
  return useAuthState().features
}

/**
 * Maps the feature state onto route-kit's presence shape without importing
 * route-kit: `ready` becomes a ready predicate, `failed` becomes failed, and
 * `pending`/`none` both read as pending.
 */
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

const TenantContext = createContext<string | null | undefined>(undefined)

/** Mounts the host's current tenant; a null tenant reads back as the fail-closed null. */
export function TenantProvider(props: {
  tenantId: string | null
  children: ReactNode
}): ReactElement {
  const { tenantId, children } = props
  return createElement(TenantContext.Provider, { value: tenantId }, children)
}

/**
 * The tenant the host says the principal is acting in, or null. The returned
 * object is referentially stable for a given tenant.
 */
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
