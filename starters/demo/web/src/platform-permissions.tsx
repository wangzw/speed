/**
 * Attaches the signed-in caller's platform permission set, so the shell can
 * gate the console on it (src/app.tsx's ConsoleEntry and ConsoleRouteGuard).
 *
 * auth-core carries the permission sets the host attaches and evaluates
 * nothing itself -- setPermissionSet is the seam, and usePermission is a pure
 * set lookup over what was attached. The platform exposes no "what may I do"
 * read to a caller, so the demo learns the caller's platform permissions from
 * the declared-permission catalog (GET /authz/admin/permissions): that route is
 * a platform-staff operation, answered only to a principal holding
 * authz.role.read in the platform domain, and its answer is every permission
 * the process declares -- the exact set the seed's platform administrator
 * holds. So an answered read attaches the catalog's codes as the caller's
 * platform set, and a refusal (an ordinary member, 403) attaches the empty set,
 * which leaves the member offered neither the console entry nor any console
 * page.
 *
 * This is the demo's own bridge over the gap: the platform carries no read of
 * the caller's held permissions, and this skeleton signs in a platform
 * administrator (which holds everything the catalog lists) or an ordinary
 * member (which holds nothing), so the catalog's contents and the caller's held
 * set coincide here. A product whose principals hold a subset of the declared
 * permissions must replace this with its own source of the caller's held set.
 *
 * A login clears the sets (auth-core's survival rule), so a re-login as
 * another principal replaces them rather than inheriting the previous
 * principal's.
 *
 * The set is (re)read whenever the effective identity changes -- an
 * authenticated transition, a re-login as another principal, or a started
 * or ended impersonation -- and only while the feature document is `ready`
 * and serves `authz.admin` -- the feature that owns the catalog endpoint. A
 * `pending` or `failed` document keeps the set unknown; a `ready` document
 * without `authz.admin` attaches the empty set. That gating is what keeps
 * the console area's "feature state first, then permission set" order
 * meaningful: a failed read never looks like an empty permission set.
 * During an impersonation the read is refused by the gateway, so the
 * attached empty set hides the console until the end's re-read restores the
 * operator's own set.
 */

import { useEffect } from 'react'
import type { ReactElement, ReactNode } from 'react'
import { useAuthState, useFeatureState } from '@speed/auth-core'
import type { AuthSession } from '@speed/auth-core'
import { authzAdminPermissionsList } from './app-api/index.js'

/**
 * The platform permission domain the console's codes resolve in (auth-core
 * names 'system' the tenant-independent platform set).
 */
const PLATFORM_DOMAIN = 'system'

/**
 * Mounts the attach for a session and renders its children unchanged: the
 * production assembly wraps the view in this so the platform permission set is
 * attached once a viewer is signed in.
 */
export function PlatformPermissions({
  session,
  children,
}: {
  /** The assembled session the set is attached to (the one attachSession bound). */
  readonly session: AuthSession
  readonly children: ReactNode
}): ReactElement {
  usePlatformPermissions(session)
  return <>{children}</>
}

/**
 * Reads the caller's platform permissions and attaches them to the session.
 *
 * The read is itself feature-gated: the catalog endpoint belongs to
 * `authz.admin`, so a deployment that does not serve it can never answer.
 * While the feature document is `pending` or `failed` the set stays unknown
 * (null) -- neither "absent" nor "present" -- and the console area shows its
 * frameless wait/failure rather than misreading the operator as a member.
 * `ready` without `authz.admin` attaches the empty set (there is no platform
 * surface to authorize), and `ready` with it (re)reads on every authenticated
 * transition: a login clears the sets (auth-core's survival rule), so a
 * re-login as another principal replaces them rather than inheriting the
 * previous principal's. A re-read first resets the set to unknown, immediately
 * before issuing its read; the reset is not part of the identity-change
 * action, which must not leave the console waiting on a set it cleared when
 * the session re-read hangs or fails. A read that is refused (or fails) attaches the empty
 * set -- fail-closed, so a viewer whose permissions are unknown is offered no
 * platform surface rather than the benefit of the doubt.
 *
 * The read is deferred into a promise so a synchronous throw from the api-sdk
 * request seam (no request function bound) lands in the same catch as any other
 * failed read rather than escaping the effect.
 */
function usePlatformPermissions(session: AuthSession): void {
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
  const features = useFeatureState()
  const ready = features.state === 'ready'
  const authzAdminServed = ready && features.names.has('authz.admin')
  useEffect(() => {
    if (!authenticated) {
      return
    }
    if (!ready) {
      // A pending or failed document leaves the set unknown.
      return
    }
    if (!authzAdminServed) {
      // The deployment serves no platform authorization surface: there is
      // nothing to read and nothing to hold.
      session.setPermissionSet(PLATFORM_DOMAIN, [])
      return
    }
    // Resetting to unknown belongs to this read, not to the identity-change
    // action that prompted it: the set must go unknown immediately before a
    // new read is issued, so a restore() that hangs or is refused leaves the
    // previous (known) set in place rather than the console waiting forever
    // on an unknown one.
    session.setPermissionSet(PLATFORM_DOMAIN, null)
    let cancelled = false
    void Promise.resolve()
      .then(() => authzAdminPermissionsList())
      .then((answer) => {
        if (cancelled) {
          return
        }
        session.setPermissionSet(
          PLATFORM_DOMAIN,
          answer.permissions.map((permission) => permission.permission),
        )
      })
      .catch(() => {
        if (cancelled) {
          return
        }
        session.setPermissionSet(PLATFORM_DOMAIN, [])
      })
    return () => {
      cancelled = true
    }
  }, [
    authenticated,
    ready,
    authzAdminServed,
    session,
    principalId,
    impersonating,
  ])
}
