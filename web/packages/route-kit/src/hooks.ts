/**
 * The hook surface: the read side of the registration surface and the
 * imperative navigation handle.
 *
 * The three readers -- useNavItems, useMatch and useHasRoute -- all narrow
 * the table through `presentDeclarations` first, so a gated page that is
 * absent is absent from navigation, from the exact match and from a
 * call-to-action's answer at once. That shared narrowing is what makes
 * "this page does not exist" one fact rather than three that can drift.
 */

import { useCallback, useMemo } from 'react'
import { useLocation, useNavigate as useRouterNavigate } from 'react-router-dom'
import { useRouteKitContext } from './context.js'
import { deriveNavItems } from './nav.js'
import { matchDeclaration } from './pathing.js'
import type { RouteMatch } from './pathing.js'
import { presentDeclarations } from './presence.js'
import type { NavItem } from './types.js'

/**
 * The navigation entries for the current URL: the effective registration
 * surface's `label`-bearing declarations, with the selected one marked.
 * Derived from the same narrowed set RouteView's consumers match against, so
 * the entry highlighted and the view rendered come from one source.
 */
export function useNavItems(): readonly NavItem[] {
  const { table, presence } = useRouteKitContext()
  const { pathname } = useLocation()
  return useMemo(
    () => deriveNavItems(presentDeclarations(table, presence), pathname),
    [table, presence, pathname],
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
