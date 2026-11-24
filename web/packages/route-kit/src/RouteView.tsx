/**
 * RouteView: renders the declaration the current URL matches, or the provider's
 * fallback. A gated declaration whose features are absent reaches the same
 * fallback an unregistered path gets.
 */

import { useLocation } from 'react-router-dom'
import type { ReactElement, ReactNode } from 'react'
import { useRouteKitContext } from './context.js'
import { matchDeclaration } from './pathing.js'
import { isGated } from './presence.js'
import type { RouteMatch } from './pathing.js'
import type { RouteContext, RoutePresence } from './types.js'

interface Dispatch {
  readonly fallback: (context: RouteContext) => ReactNode
  readonly presence: RoutePresence | undefined
  readonly pending: ((context: RouteContext) => ReactNode) | undefined
  readonly failed: ((context: RouteContext) => ReactNode) | undefined
}

/** Renders the current URL's declaration or the fallback. */
export function RouteView(): ReactElement {
  const { table, fallback, presence, pending, failed } = useRouteKitContext()
  const { pathname } = useLocation()
  return (
    <>
      {renderMatch(matchDeclaration(table, pathname), pathname, {
        fallback,
        presence,
        pending,
        failed,
      })}
    </>
  )
}

function renderMatch(
  match: RouteMatch | undefined,
  pathname: string,
  dispatch: Dispatch,
): ReactNode {
  if (match === undefined) {
    return dispatch.fallback({ path: pathname, params: {} })
  }
  const { declaration, context } = match
  if (!isGated(declaration)) return declaration.view(context)
  const presence = dispatch.presence
  if (presence === undefined) return dispatch.fallback(context)
  if (presence.state === 'pending') {
    return dispatch.pending === undefined
      ? dispatch.fallback(context)
      : dispatch.pending(context)
  }
  if (presence.state === 'failed') {
    return dispatch.failed === undefined
      ? dispatch.fallback(context)
      : dispatch.failed(context)
  }
  const requires = declaration.requires ?? []
  return requires.every((name) => presence.has(name))
    ? declaration.view(context)
    : dispatch.fallback({ path: pathname, params: {} })
}
