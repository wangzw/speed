/**
 * RouteView -- renders the declaration the current URL matches, or the
 * provider's fallback when none does.
 *
 * It reads the live location, not any component-local state, so a link click
 * or an imperative navigate re-renders it by the same path the URL took. The
 * context it hands a view is the matched path plus its captured params; the
 * fallback gets the unmatched path with no params.
 */

import { useLocation } from 'react-router-dom'
import type { ReactElement } from 'react'
import { useRouteKitContext } from './context.js'
import { matchDeclaration } from './pathing.js'

export function RouteView(): ReactElement {
  const { table, fallback } = useRouteKitContext()
  const { pathname } = useLocation()
  const match = matchDeclaration(table, pathname)
  return (
    <>
      {match === undefined
        ? fallback({ path: pathname, params: {} })
        : match.declaration.view(match.context)}
    </>
  )
}
