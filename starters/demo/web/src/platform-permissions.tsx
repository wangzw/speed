/**
 * Attaches the signed-in caller's own permission sets, so the shell can gate
 * the admin on them (src/app.tsx's AdminEntry and AdminAreaGate).
 *
 * The platform publishes the caller's held permissions at
 * GET /api/v1/authz/me/permissions: `platform` is the set the caller holds in
 * the platform domain and `tenant` the set it holds in the tenant the request
 * resolved to. auth-core carries the sets the host attaches and evaluates
 * nothing itself -- setPermissionSet is the seam, and usePermission is a pure
 * set lookup over what was attached. The read is the caller's own, so it needs
 * neither the declared-permission catalog nor any platform-staff permission to
 * answer.
 *
 * The read waits for the feature document to be `ready` and for the current
 * tenant to be settled: while the feature state is `pending` or `failed`, or
 * the host has not decided the current tenant yet, both sets stay unknown
 * (null), so the admin area shows its frameless wait or failure rather than
 * misreading the caller as a member. That gating is what keeps the admin
 * area's "feature state first, then permission set" order meaningful -- a
 * failed feature read never looks like an empty permission set.
 *
 * The tenant half is the request's own requirement, not a preference: the
 * route sits on the API endpoint, which the tenant resolver guards, and a
 * request carrying no tenant name is refused 404 before any handler runs.
 * Reading before the host settles the current tenant would therefore spend a
 * request on a known refusal -- a console error at every sign-in -- and attach
 * the fail-closed empty set to a caller whose sets were never actually asked
 * for. The tenant is a host fact settled outside the session (src/app.tsx's
 * tenant frame, or the admin area's own settle), and the read follows it.
 *
 * Once the document is ready the read is (re)issued whenever the effective
 * identity changes -- an authenticated transition, a re-login as another
 * principal, or a started or ended impersonation -- and whenever the current
 * tenant moves. A login clears the sets (auth-core's survival rule), and
 * restore() retains whatever was attached and cannot know the identity
 * changed, so re-reading on this edge is what keeps a stale set from outliving
 * the identity it was read under. The tenant is a separate key: the tenant set
 * is the set held in the tenant the request resolved to, and the host settles
 * and switches the current tenant outside the session (src/chrome.tsx's cell,
 * recorded by the tenant area), so a first read issued before the tenant
 * settles answers an empty tenant set and a switch would otherwise leave the
 * old tenant's set attached. A re-read first resets both sets to unknown
 * immediately before issuing its read; the reset belongs to the read, not to
 * the identity-change action, so a read that hangs or is refused does not
 * leave the admin waiting on a set the action cleared.
 *
 * A read that is refused or fails attaches the empty set to both domains --
 * fail-closed, so a caller whose permissions are unknown is offered no platform
 * surface rather than the benefit of the doubt. A successful read attaches
 * both lists even when one is empty: an empty tenant list is a known answer
 * (the request resolved no tenant, or the caller holds nothing there), not an
 * unknown one.
 *
 * The read is deferred into a promise so a synchronous throw from the api-sdk
 * request seam (no request function bound) lands in the same catch as any other
 * failed read rather than escaping the effect.
 */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react'
import type { ReactElement, ReactNode } from 'react'
import { useAuthState, useFeatureState } from '@speed/auth-core'
import type { AuthSession } from '@speed/auth-core'
import { authzSelfPermissions } from './app-api/index.js'
import { useCurrentTenantId } from './chrome.js'

/**
 * The permission domains auth-core carries: 'system' is the tenant-independent
 * platform set and 'tenant' the set the principal holds in the tenant its
 * requests are resolved in.
 */
const PLATFORM_DOMAIN = 'system'
const TENANT_DOMAIN = 'tenant'

/** The read's outcome where a surface has to decide what to show: whether the
 * last attempt failed (the sets stay unknown) and how to try again. */
export interface PermissionReadState {
  /** The last read failed: the sets are unknown, not empty. */
  readonly failed: boolean
  /** Issues the read again; the surface retries by asking for it. */
  readonly retry: () => void
}

const NO_PERMISSION_READ: PermissionReadState = {
  failed: false,
  retry: () => undefined,
}

const PermissionReadContext = createContext<PermissionReadState>(
  NO_PERMISSION_READ,
)

/**
 * The last permission read's failure, for a surface that must not read an
 * unknown set as an empty one: the admin area renders its wait or a retry
 * from it. A tree without the provider (a unit test mounting one surface)
 * reads no failure and retries nothing.
 */
export function usePermissionRead(): PermissionReadState {
  return useContext(PermissionReadContext)
}

/**
 * Mounts the attach for a session and renders its children unchanged: the
 * production assembly wraps the view in this so the caller's permission sets
 * are attached once a viewer is signed in.
 */
export function PlatformPermissions({
  session,
  children,
}: {
  /** The assembled session the sets are attached to (the one attachSession bound). */
  readonly session: AuthSession
  readonly children: ReactNode
}): ReactElement {
  // A retry re-runs the read: the attempt is an effect key, and the failure
  // is published to the surfaces that must not read an unknown set as an
  // empty one.
  const [attempt, setAttempt] = useState(0)
  const [failed, setFailed] = useState(false)
  usePlatformPermissions(session, attempt, setFailed)
  const retry = useCallback(() => setAttempt((current) => current + 1), [])
  const value = useMemo<PermissionReadState>(
    () => ({ failed, retry }),
    [failed, retry],
  )
  return (
    <PermissionReadContext.Provider value={value}>
      {children}
    </PermissionReadContext.Provider>
  )
}

/**
 * Reads the caller's permission sets and attaches them to the session.
 *
 * The read is gated on the feature document being `ready` and the current
 * tenant being settled, not on any one feature: the permission read is scoped
 * to the caller, so it belongs to no optional capability. While the document
 * is `pending` or `failed`, or the current tenant is undecided, both sets stay
 * unknown (null) -- neither "absent" nor "present" -- and the admin area
 * shows its frameless wait/failure rather than misreading the operator as a
 * member. The guarded route answers 404 to a request without a tenant name, so
 * an undecided tenant is a request that would be refused before any handler;
 * it is not read. On every authenticated transition with a ready document the read
 * is (re)issued: a login clears the sets (auth-core's survival rule), so a
 * re-login as another principal replaces them rather than inheriting the
 * previous principal's. The current tenant is a second key: the tenant set is
 * the set held in the tenant the request resolved to, and the tenant settles
 * and changes outside the session, so a re-read follows it too. A re-read
 * first resets both sets to unknown, immediately before issuing its read.
 *
 * A failed read is not an empty set. A transport or server failure leaves both
 * sets unknown and reports the failure, and the admin area shows its wait or
 * its retry instead of reading "no permissions" into a fault. Only an answered
 * document attaches a set -- an answer whose domains are both empty is a known
 * empty set, distinct from a failure.
 */
function usePlatformPermissions(
  session: AuthSession,
  attempt: number,
  onFailure: (failed: boolean) => void,
): void {
  const snapshot = useAuthState()
  const authenticated = snapshot.state === 'authenticated'
  // The identity is an input: a login attaches a set for a different
  // principal, and a started or ended impersonation swaps the effective
  // account without the feature state moving. restore() retains whatever
  // set was attached and cannot know the identity changed, so re-reading on
  // this edge is what keeps a stale (or, during an impersonation, refused)
  // set from outliving the identity it was read under.
  const principalId =
    snapshot.state === 'authenticated'
      ? (snapshot.principal?.account_id ?? '')
      : ''
  const impersonating = snapshot.impersonating
  // The tenant the host currently resolves requests to. It is not part of the
  // session snapshot -- the tenant area settles and switches it -- so it is
  // read from the host's own cell and joins the identity as a re-read key.
  const tenantId = useCurrentTenantId()
  const features = useFeatureState()
  const ready = features.state === 'ready'
  useEffect(() => {
    if (!authenticated || !ready || tenantId === null) {
      // Nothing is read: the sets stay as they are, and a previous failure
      // no longer describes this state.
      onFailure(false)
      return
    }
    // Resetting to unknown belongs to this read, not to the identity-change
    // action that prompted it: the sets must go unknown immediately before a
    // new read is issued, so a restore() that hangs or is refused leaves the
    // admin waiting on an unknown set rather than a stale one.
    session.setPermissionSet(PLATFORM_DOMAIN, null)
    session.setPermissionSet(TENANT_DOMAIN, null)
    onFailure(false)
    let cancelled = false
    void Promise.resolve()
      .then(() => authzSelfPermissions())
      .then((answer) => {
        if (cancelled) {
          return
        }
        session.setPermissionSet(PLATFORM_DOMAIN, answer.platform)
        session.setPermissionSet(TENANT_DOMAIN, answer.tenant)
      })
      .catch(() => {
        if (cancelled) {
          return
        }
        // The sets were reset to unknown above and stay there: a failed read
        // says nothing about which permissions the caller holds.
        onFailure(true)
      })
    return () => {
      cancelled = true
    }
  }, [
    authenticated,
    ready,
    session,
    principalId,
    impersonating,
    tenantId,
    attempt,
    onFailure,
  ])
}
