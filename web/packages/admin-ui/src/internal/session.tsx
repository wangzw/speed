/**
 * The console session seam: the session the console's step-up-gated
 * writes elevate.
 *
 * The console's administration routes answer a write that asks for a
 * stronger authentication than the session proves with a 401 while the
 * session stays established (@speed/auth-core's session, delivered as a
 * cookie). To raise the session the page opens the step-up dialog, which
 * drives the session's authenticated step-up flow -- a session operation
 * @speed/auth-core owns. The host hands that session to the console once,
 * through AdminSessionProvider (AdminShell mounts it from its
 * `session` prop), and a page reads it back with useAdminSession.
 *
 * This is NOT a current-tenant seam: the session here is read for the
 * step-up operation alone, and no page derives a current tenant from it
 * (the console resolves no tenant -- see the package's AGENTS.md).
 *
 * useAdminSession fails closed to null: a page rendered with no
 * provider (a host that mounts no session, a suite that renders a page
 * alone) simply has no session to elevate, so its writes fire unguarded
 * and a refused one renders its error code -- it never throws.
 */

import { createContext, useContext } from 'react'
import type { ReactElement, ReactNode } from 'react'
import type { AuthSession } from '@speed/auth-core'

const AdminSessionContext = createContext<AuthSession | null>(null)

export interface AdminSessionProviderProps {
  /** The assembled session whose step-up flow the console drives; null
   * leaves the pages with no session to elevate. */
  readonly session: AuthSession | null
  readonly children: ReactNode
}

/** Mounts the session the console's step-up-gated writes elevate. */
export function AdminSessionProvider({
  session,
  children,
}: AdminSessionProviderProps): ReactElement {
  return (
    <AdminSessionContext.Provider value={session}>
      {children}
    </AdminSessionContext.Provider>
  )
}

/** The console session, or null when no provider is mounted. */
export function useAdminSession(): AuthSession | null {
  return useContext(AdminSessionContext)
}
