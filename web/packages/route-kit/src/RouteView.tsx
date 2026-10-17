/**
 * RouteView -- renders the declaration the current URL matches, or the
 * provider's fallback when none does.
 *
 * It reads the live location, not any component-local state, so a link click
 * or an imperative navigate re-renders it by the same path the URL took. The
 * context it hands a view is the matched path plus its captured params; the
 * fallback gets the unmatched path with no params.
 *
 * Matching runs over the whole table, not the narrowed surface: a gated path
 * still matches while the presence predicate is pending or failed, which is
 * what lets it render the pending or failed view. Absence is the exception --
 * a gated declaration whose features are absent renders the fallback with the
 * same context an unregistered path gets, the unmatched path and no params,
 * and its view is never called. The fallback therefore cannot tell the two
 * apart, however it reads its context.
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

/**
 * The one place the match turns into a view. Unmatched and gated-but-absent
 * both reach `fallback`; pending and failed reach the host's own views; an
 * ungated or present declaration reaches its view.
 */
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
