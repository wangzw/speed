/** The console's impersonation-change seam. */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'

/** Which identity edge the page just settled. */
export type AdminImpersonationChange = 'started' | 'ended'

const AdminImpersonationChangeContext = createContext<
  ((change: AdminImpersonationChange) => void | Promise<void>) | undefined
>(undefined)

export interface AdminImpersonationChangeProviderProps {
  /** The host's action, or undefined to leave the page self-contained. */
  readonly onImpersonationChange?: (
    change: AdminImpersonationChange,
  ) => void | Promise<void>
  readonly children: ReactNode
}

/** Publishes the host's identity-change action to the pages it renders. */
export function AdminImpersonationChangeProvider({
  onImpersonationChange,
  children,
}: AdminImpersonationChangeProviderProps) {
  return (
    <AdminImpersonationChangeContext.Provider value={onImpersonationChange}>
      {children}
    </AdminImpersonationChangeContext.Provider>
  )
}

/** The host's identity-change action, or undefined when the shell has none. */
export function useAdminImpersonationChange():
  | ((change: AdminImpersonationChange) => void | Promise<void>)
  | undefined {
  return useContext(AdminImpersonationChangeContext)
}
