/**
 * The console's callback-return seam.
 *
 * The step-up dialog can start a flow whose method is a redirect: the
 * provider's round trip then lands the browser back on a page of this
 * application, and the server puts a refusal's problem code in the URL
 * fragment there. The landing page must be the one that reads it, so the
 * dialog sends that page's path as the flow's `return_to`. This package is
 * host-agnostic and does not know the current route, so the host supplies it
 * through the shell (AdminShell's `returnPath`), which the shell publishes
 * to its routed pages through this context and the dialog reads.
 *
 * With no provider (a page rendered on its own, as a unit test renders one)
 * or no path, the context is absent and the start carries no `return_to`:
 * the deployment's own default return target applies.
 */

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
