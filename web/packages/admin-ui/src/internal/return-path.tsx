/** The console's callback-return seam. */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'

const AdminReturnPathContext = createContext<string | undefined>(undefined)

export interface AdminReturnPathProviderProps {
  /** The host's current path, or undefined to leave the start without one. */
  readonly returnPath?: string
  readonly children: ReactNode
}

/** Publishes the host's current path to the pages the shell renders. */
export function AdminReturnPathProvider({
  returnPath,
  children,
}: AdminReturnPathProviderProps) {
  return (
    <AdminReturnPathContext.Provider value={returnPath}>
      {children}
    </AdminReturnPathContext.Provider>
  )
}

/** The host's current path, or undefined when the shell has none. */
export function useAdminReturnPath(): string | undefined {
  return useContext(AdminReturnPathContext)
}
