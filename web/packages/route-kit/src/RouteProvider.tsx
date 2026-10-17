/**
 * RouteProvider -- installs the router and hands the registration surface to
 * its subtree.
 *
 * It mounts a BrowserRouter (react-router-dom), so a product's pages get
 * real path URLs and deep links; every route-kit consumer below it
 * (RouteView, useNavItems, useMatch, useHasRoute, RouteLink, useNavigate)
 * reads the same table, the same feature-presence predicate and the same
 * live location, which is why navigation state and rendered view cannot
 * disagree.
 *
 * The table is checked before the children render: a duplicate, ambiguous or
 * malformed declaration, a missing fallback, or a gated table missing its
 * presence predicate or its pending/failed views, throws here at assembly
 * rather than surfacing as a page that never renders.
 */

import { useMemo } from 'react'
import type { ReactElement, ReactNode } from 'react'
import { BrowserRouter } from 'react-router-dom'
import { RouteKitContext } from './context.js'
import { assertRouteTable } from './table.js'
import type { RouteContext, RoutePresence, RouteTable } from './types.js'

export interface RouteProviderProps {
  /** The product's pages: the one place they are declared. */
  readonly table: RouteTable
  /** The view rendered when no declaration matches the current URL. Required. */
  readonly fallback: (context: RouteContext) => ReactNode
  /**
   * The host's feature-presence answer. Required when the table contains a
   * gated declaration; an ungated table needs none.
   */
  readonly presence?: RoutePresence
  /** The view a gated path renders while the presence predicate is pending. */
  readonly pending?: (context: RouteContext) => ReactNode
  /** The view a gated path renders when the presence predicate failed. */
  readonly failed?: (context: RouteContext) => ReactNode
  readonly children: ReactNode
}

export function RouteProvider({
  table,
  fallback,
  presence,
  pending,
  failed,
  children,
}: RouteProviderProps): ReactElement {
  assertRouteTable(table, fallback, { presence, pending, failed })
  const value = useMemo(
    () => ({ table, fallback, presence, pending, failed }),
    [table, fallback, presence, pending, failed],
  )
  return (
    <BrowserRouter>
      <RouteKitContext.Provider value={value}>
        {children}
      </RouteKitContext.Provider>
    </BrowserRouter>
  )
}

