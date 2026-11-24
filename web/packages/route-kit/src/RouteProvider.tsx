/**
 * RouteProvider installs a BrowserRouter and hands the registration surface to
 * its subtree. It checks the table before the children render and, after a
 * navigation, hands focus to the new page.
 */

import { useMemo } from 'react'
import type { ReactElement, ReactNode } from 'react'
import { BrowserRouter } from 'react-router-dom'
import { RouteKitContext } from './context.js'
import { NavigationFocus } from './navigation-focus.js'
import { assertRouteTable } from './table.js'
import type { RouteContext, RoutePresence, RouteTable } from './types.js'

/**
 * Where focus goes after an in-app navigation: `'page-heading'` moves focus to
 * the new page's first level-one heading inside `main`, else `main` itself;
 * `'none'` leaves focus to the host.
 */
export type RouteNavigationFocus = 'page-heading' | 'none'

/** Props for RouteProvider. */
export interface RouteProviderProps {
  /** The product's pages: the one place they are declared. */
  readonly table: RouteTable
  /** The view rendered when no declaration matches the current URL. Required. */
  readonly fallback: (context: RouteContext) => ReactNode
  /** The host's feature-presence answer; required when the table contains a gated declaration. */
  readonly presence?: RoutePresence
  /** The view a gated path renders while the presence predicate is pending. */
  readonly pending?: (context: RouteContext) => ReactNode
  /** The view a gated path renders when the presence predicate failed. */
  readonly failed?: (context: RouteContext) => ReactNode
  /** The deployment path prefix passed to the installed router; absent keeps the origin root. */
  readonly basename?: string
  /** Focus hand-off after an in-app navigation; defaults to `'page-heading'`, and `'none'` leaves focus to the host. */
  readonly navigationFocus?: RouteNavigationFocus
  readonly children: ReactNode
}

/** Installs the router, validates the table, and provides the registration surface. */
export function RouteProvider({
  table,
  fallback,
  presence,
  pending,
  failed,
  basename,
  navigationFocus = 'page-heading',
  children,
}: RouteProviderProps): ReactElement {
  assertRouteTable(table, fallback, { presence, pending, failed })
  const value = useMemo(
    () => ({ table, fallback, presence, pending, failed, basename }),
    [table, fallback, presence, pending, failed, basename],
  )
  return (
    <BrowserRouter basename={basename}>
      <RouteKitContext.Provider value={value}>
        {children}
        {navigationFocus === 'none' ? null : <NavigationFocus />}
      </RouteKitContext.Provider>
    </BrowserRouter>
  )
}
