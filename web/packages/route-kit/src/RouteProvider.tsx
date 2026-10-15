/**
 * RouteProvider -- installs the router and hands the registration surface to
 * its subtree.
 *
 * It mounts a BrowserRouter (react-router-dom), so a product's pages get
 * real path URLs and deep links; every route-kit consumer below it
 * (RouteView, useNavItems, RouteLink, useNavigate) reads the same table and
 * the same live location, which is why navigation state and rendered view
 * cannot disagree.
 *
 * The table is checked before the children render: a duplicate, ambiguous or
 * malformed declaration, or a missing fallback, throws here at assembly
 * rather than surfacing as a page that never renders.
 */

import { useMemo } from 'react'
import type { ReactElement, ReactNode } from 'react'
import { BrowserRouter } from 'react-router-dom'
import { RouteKitContext } from './context.js'
import { assertRouteTable } from './table.js'
import type { RouteContext, RouteTable } from './types.js'

export interface RouteProviderProps {
  /** The product's pages: the one place they are declared. */
  readonly table: RouteTable
  /** The view rendered when no declaration matches the current URL. Required. */
  readonly fallback: (context: RouteContext) => ReactNode
  readonly children: ReactNode
}

export function RouteProvider({
  table,
  fallback,
  children,
}: RouteProviderProps): ReactElement {
  assertRouteTable(table, fallback)
  const value = useMemo(() => ({ table, fallback }), [table, fallback])
  return (
    <BrowserRouter>
      <RouteKitContext.Provider value={value}>
        {children}
      </RouteKitContext.Provider>
    </BrowserRouter>
  )
}
