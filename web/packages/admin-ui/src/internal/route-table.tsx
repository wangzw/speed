/**
 * The console's published route table: the shell publishes the host's narrowed
 * table so a page resolves a sibling declaration by its stable `id`. With no
 * provider the table is empty and a sibling link is not rendered.
 */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'
import type { AdminRoute } from '../route.js'

const EMPTY_ROUTES: readonly AdminRoute[] = []

const AdminRouteTableContext =
  createContext<readonly AdminRoute[]>(EMPTY_ROUTES)

export interface AdminRouteTableProviderProps {
  /** The host-supplied route table; absent publishes an empty table. */
  readonly routes?: readonly AdminRoute[]
  readonly children: ReactNode
}

/** Publishes the host's route table to the pages it renders. */
export function AdminRouteTableProvider({
  routes = EMPTY_ROUTES,
  children,
}: AdminRouteTableProviderProps) {
  return (
    <AdminRouteTableContext.Provider value={routes}>
      {children}
    </AdminRouteTableContext.Provider>
  )
}

/** The host-supplied route table; empty when no provider is mounted. */
export function useAdminRouteTable(): readonly AdminRoute[] {
  return useContext(AdminRouteTableContext)
}

/** The declaration with the given stable id, or undefined when absent. */
export function adminRouteById(
  routes: readonly AdminRoute[],
  id: string,
): AdminRoute | undefined {
  return routes.find((route) => route.id === id)
}
