/** The console's impersonation-starting seam. */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'

/** Starts an attempt; the returned function releases it, when one is returned. */
export type AdminImpersonationStarting = () => void | (() => void)

const AdminImpersonationStartingContext = createContext<
  AdminImpersonationStarting | undefined
>(undefined)

export interface AdminImpersonationStartingProviderProps {
  /** The host's attempt hook, or undefined to leave the form self-contained. */
  readonly onImpersonationStarting?: AdminImpersonationStarting
  readonly children: ReactNode
}

/** Publishes the host's impersonation-starting hook to the pages it renders. */
export function AdminImpersonationStartingProvider({
  onImpersonationStarting,
  children,
}: AdminImpersonationStartingProviderProps) {
  return (
    <AdminImpersonationStartingContext.Provider
      value={onImpersonationStarting}
    >
      {children}
    </AdminImpersonationStartingContext.Provider>
  )
}

/** The host's impersonation-starting hook, or undefined when none is supplied. */
export function useAdminImpersonationStarting():
  | AdminImpersonationStarting
  | undefined {
  return useContext(AdminImpersonationStartingContext)
}
