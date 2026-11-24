/**
 * The hook surface: the read side of the registration surface, the imperative
 * navigation handles and the link props an anchor needs. Every reader narrows
 * the table through `presentDeclarations` first, so absence is one fact.
 */

import { useCallback, useMemo } from 'react'
import type { MouseEventHandler } from 'react'
import {
  useHref,
  useLocation,
  useNavigate as useRouterNavigate,
} from 'react-router-dom'
import { useRouteKitContext } from './context.js'
import { createLinkClickHandler } from './link.js'
import { deriveNavItems } from './nav.js'
import { matchDeclaration } from './pathing.js'
import type { RouteMatch } from './pathing.js'
import { presentDeclarations } from './presence.js'
import type { NavItem } from './types.js'

/**
 * The navigation entries for the current URL: the effective surface's
 * `label`-bearing declarations, the selected one marked, each with a basename-
 * including href and an in-app onClick.
 */
export function useNavItems(): readonly NavItem[] {
  const { table, presence, basename } = useRouteKitContext()
  const { pathname } = useLocation()
  const navigate = useRouterNavigate()
  return useMemo(
    () =>
      deriveNavItems(
        presentDeclarations(table, presence),
        pathname,
        basename,
      ).map((item) => ({
        ...item,
        onClick: createLinkClickHandler(item.id, navigate),
      })),
    [table, presence, pathname, basename, navigate],
  )
}

/** The declaration the current URL matches exactly on the effective registration surface, or `undefined`; prefix inheritance plays no part. */
export function useMatch(): RouteMatch | undefined {
  const { table, presence } = useRouteKitContext()
  const { pathname } = useLocation()
  return useMemo(
    () => matchDeclaration(presentDeclarations(table, presence), pathname),
    [table, presence, pathname],
  )
}

/** Whether `path` matches a declaration exactly on the effective registration surface. */
export function useHasRoute(path: string): boolean {
  const { table, presence } = useRouteKitContext()
  return useMemo(
    () =>
      matchDeclaration(presentDeclarations(table, presence), path) !== undefined,
    [table, presence, path],
  )
}

/** A function that pushes a target path onto the history; RouteView re-renders from the new location. */
export function useNavigate(): (to: string) => void {
  const navigate = useRouterNavigate()
  return useCallback(
    (to: string) => {
      navigate(to)
    },
    [navigate],
  )
}

/** A function that replaces the current history entry with a target path. */
export function useRedirect(): (to: string) => void {
  const navigate = useRouterNavigate()
  return useCallback(
    (to: string) => {
      navigate(to, { replace: true })
    },
    [navigate],
  )
}

/** The raw path and query string a URL carries: `path` excludes the router's
 * basename; `search` is the untouched query string, `?` included, or empty
 * when there is none. */
export interface RouteLocationPath {
  readonly path: string
  readonly search: string
}

/** The current URL's raw path (basename excluded) and query string, without consulting the registration surface. */
export function useLocationPath(): RouteLocationPath {
  const { pathname, search } = useLocation()
  return useMemo(() => ({ path: pathname, search }), [pathname, search])
}

/** The props an anchor needs to act as an in-app link to one target: `href` with
 * the router's basename included, and an `onClick` that navigates in-app for a
 * plain primary click and leaves every other click to the browser. */
export interface RouteLinkTarget {
  readonly href: string
  readonly onClick: MouseEventHandler<HTMLElement>
}

/**
 * The `href` and `onClick` for a link to `to`, for hosts that render their own
 * anchor. The click judgement is this package's independent implementation of
 * the layout-kit policy, not a copy (see `src/link.ts`).
 */
export function useRouteLinkProps(to: string): RouteLinkTarget {
  useRouteKitContext()
  const href = useHref(to)
  const navigate = useRouterNavigate()
  const onClick = useMemo(
    () => createLinkClickHandler(to, navigate),
    [to, navigate],
  )
  return useMemo(() => ({ href, onClick }), [href, onClick])
}
