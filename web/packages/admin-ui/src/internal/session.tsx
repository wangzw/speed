/**
 * The console session seam: the session the step-up-gated writes elevate. It
 * is not a current-tenant seam -- no page derives a tenant from it -- and it
 * fails closed to null, so a page with no session fires its writes unguarded.
 */

import { createContext, useContext } from 'react'
import type { ReactElement, ReactNode } from 'react'
import type { AuthSession } from '@speed/auth-core'

const AdminSessionContext = createContext<AuthSession | null>(null)

/** The props of the console session provider. */
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
