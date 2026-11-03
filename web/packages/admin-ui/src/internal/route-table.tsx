/**
 * The console's published route table.
 *
 * A page that links to a sibling console page cannot build that sibling's
 * path from its own matched path without guessing: the console is mounted
 * under whatever prefix the host chose, and the sibling's declaration is
 * the authority on where it lives. The shell (or the frameless provider)
 * therefore publishes the route table the host handed it -- already
 * narrowed by the host's feature presence predicate -- and a page resolves
 * a sibling declaration by its stable `id` before building a link.
 *
 * The table is exactly what the host supplied: this package does not
 * filter it, add to it or derive from it. With no provider (a page
 * rendered on its own) the table is empty, so a sibling link that depends
 * on a declaration is simply not rendered.
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
