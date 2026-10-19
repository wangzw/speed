/**
 * The hook surface: the read side of the registration surface, the
 * imperative navigation handles and the link props an anchor needs.
 *
 * The three readers -- useNavItems, useMatch and useHasRoute -- all narrow
 * the table through `presentDeclarations` first, so a gated page that is
 * absent is absent from navigation, from the exact match and from a
 * call-to-action's answer at once. That shared narrowing is what makes
 * "this page does not exist" one fact rather than three that can drift.
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
 * The navigation entries for the current URL: the effective registration
 * surface's `label`-bearing declarations, with the selected one marked.
 * Derived from the same narrowed set RouteView's consumers match against, so
 * the entry highlighted and the view rendered come from one source. Each
 * entry's `href` includes the router's basename, so a modified click, a
 * middle click or a copied link points at the URL the application is served
 * at, and each carries the `onClick` a frame should attach beside it: a
 * plain primary click navigates in-app, a modified or new-tab click is left
 * to the browser. That handler navigates by the entry's `id` -- the
 * declaration's own path -- because the router prepends the basename itself
 * and would double it otherwise.
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

/**
 * The declaration the current URL matches exactly on the effective
 * registration surface, with its match context -- or `undefined` when
 * nothing does. Prefix inheritance plays no part here: a path below a nav
 * entry that has no declaration of its own does not inherit that entry.
 */
export function useMatch(): RouteMatch | undefined {
  const { table, presence } = useRouteKitContext()
  const { pathname } = useLocation()
  return useMemo(
    () => matchDeclaration(presentDeclarations(table, presence), pathname),
    [table, presence, pathname],
  )
}

/**
 * Whether `path` matches a declaration exactly on the effective registration
 * surface. A parameterized pattern matches by path, and a gated declaration
 * whose features are absent answers false -- an entry control pointing at it
 * disappears with the page.
 */
export function useHasRoute(path: string): boolean {
  const { table, presence } = useRouteKitContext()
  return useMemo(
    () =>
      matchDeclaration(presentDeclarations(table, presence), path) !== undefined,
    [table, presence, path],
  )
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

/**
 * A function that replaces the current history entry with a target path, for
 * a redirect that should not leave a back-button entry behind (a callback
 * page, a sign-in completion). It changes the URL only; RouteView re-renders
 * from the new location.
 */
export function useRedirect(): (to: string) => void {
  const navigate = useRouterNavigate()
  return useCallback(
    (to: string) => {
      navigate(to, { replace: true })
    },
    [navigate],
  )
}

/** The raw path and query string a URL carries. */
export interface RouteLocationPath {
  /** The path, independent of whether any declaration matches it. */
  readonly path: string
  /** The query string, `?` included, or an empty string when there is none. */
  readonly search: string
}

/**
 * The current URL's raw path and query string, without consulting the
 * registration surface: an unmatched path reads the same as a matched one.
 * `path` excludes the router's basename, like every path this package hands
 * out; `search` is the untouched query string. Use it instead of importing
 * react-router-dom's `useLocation` in host code.
 */
export function useLocationPath(): RouteLocationPath {
  const { pathname, search } = useLocation()
  return useMemo(() => ({ path: pathname, search }), [pathname, search])
}

/** The props an anchor needs to act as an in-app link to one target. */
export interface RouteLinkTarget {
  /** The target's href, with the router's basename included. */
  readonly href: string
  /**
   * An in-app navigation for a plain primary click, and nothing otherwise:
   * a modified click, a middle click, a `target` other than `_self`, a
   * `download` link or an already handled event keeps the browser's own
   * behaviour (a new tab, a save, the default navigation).
   */
  readonly onClick: MouseEventHandler<HTMLElement>
}

/**
 * The `href` and `onClick` for a link to `to`, for hosts that render their
 * own anchor or hand the pair to a component that accepts an href and a
 * click handler (a MUI `component`, a navigation frame's entry).
 *
 * `href` comes from react-router-dom's `useHref`, so the router's basename
 * is included and a modified click or a copied link points at a real URL.
 * The click judgement implements the same policy as `@speed/layout-kit`'s
 * `isPlainPrimaryClick` but is an independent implementation, not a copy of
 * it -- the two differ on a missing `button` value, on targets other than
 * `_blank`, and on `currentTarget` versus the nearest anchor (see
 * `src/link.ts`).
 */
export function useRouteLinkProps(to: string): RouteLinkTarget {
  // Read the context for its fail-fast error: a link hook outside the
  // provider is the same composition mistake the other hooks report.
  useRouteKitContext()
  const href = useHref(to)
  const navigate = useRouterNavigate()
  const onClick = useMemo(
    () => createLinkClickHandler(to, navigate),
    [to, navigate],
  )
  return useMemo(() => ({ href, onClick }), [href, onClick])
}
