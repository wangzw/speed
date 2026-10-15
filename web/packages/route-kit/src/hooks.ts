/**
 * The hook surface: the read side of the registration surface and the
 * imperative navigation handle.
 */

import { useCallback, useMemo } from 'react'
import { useLocation, useNavigate as useRouterNavigate } from 'react-router-dom'
import { useRouteKitContext } from './context.js'
import { deriveNavItems } from './nav.js'
import type { NavItem } from './types.js'

/**
 * The navigation entries for the current URL: the table's `label`-bearing
 * declarations, with the selected one marked. Derived from the same table
 * RouteView matches against, so the entry highlighted and the view rendered
 * come from one source.
 */
export function useNavItems(): readonly NavItem[] {
  const { table } = useRouteKitContext()
  const { pathname } = useLocation()
  return useMemo(() => deriveNavItems(table, pathname), [table, pathname])
}

/**
 * A function that pushes a target path onto the history, for callers acting
 * from event handlers. It changes the URL only; RouteView re-renders from
 * the new location.
 */
export function useNavigate(): (to: string) => void {
  const navigate = useRouterNavigate()
  return useCallback(
    (to: string) => {
      navigate(to)
    },
    [navigate],
  )
}
