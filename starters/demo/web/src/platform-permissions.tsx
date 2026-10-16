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
 * The set is (re)read on every authenticated transition: a login clears the
 * sets (auth-core's survival rule), so a re-login as another principal
 * replaces them rather than inheriting the previous principal's.
 */

import { useEffect } from 'react'
import type { ReactElement, ReactNode } from 'react'
import { useAuthState } from '@speed/auth-core'
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
 * Reads the caller's platform permissions and attaches them to the session
 * whenever it turns authenticated. A read that is refused (or fails) attaches
 * the empty set -- fail-closed, so a viewer whose permissions are unknown is
 * offered no platform surface rather than the benefit of the doubt.
 *
 * The read is deferred into a promise so a synchronous throw from the api-sdk
 * request seam (no request function bound) lands in the same catch as any other
 * failed read rather than escaping the effect.
 */
function usePlatformPermissions(session: AuthSession): void {
  const authenticated = useAuthState().state === 'authenticated'
  useEffect(() => {
    if (!authenticated) {
      return
    }
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
  }, [authenticated, session])
}
