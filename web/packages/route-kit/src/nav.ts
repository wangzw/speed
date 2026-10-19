/**
 * Navigation derivation. A nav item exists exactly when a declaration
 * carries a `label`, so the table stays the single source of truth: the
 * pages that render and the entries that navigate are read from one list
 * and cannot disagree. Which entry is selected is the current URL's
 * decision, computed here against the same patterns the view matches.
 */

import { isUnder, literalPath, matchesPattern } from './pathing.js'
import type { NavItem, RouteDeclaration, RouteTable } from './types.js'

/**
 * The declarations' navigation entries, in table order, with the current
 * URL's selection applied.
 *
 * An entry is a *candidate* when the current path matches its pattern in
 * full, or sits below its literal path -- the latter is what selects a
 * parent (`/cases`) while its own child route (`/cases/:caseId`) is open.
 * Among candidates the longest selector wins; a selector is the full path
 * for a full match and the literal path otherwise, so an exact hit always
 * outranks a prefix parent and a more specific parent outranks a shallower
 * one. Ties keep the earlier declaration. No candidate means nothing is
 * selected, and a declaration without a label is never a candidate.
 *
 * `basename` is the deployment prefix the router was installed under. An
 * entry's `href` includes it, so the value is a URL the browser can open --
 * a new tab, a copied link and an unmodified click all reach the same place
 * -- while `id` stays the declaration's own path, which is what in-app
 * navigation hands the router (the router applies the prefix itself).
 */
export function deriveNavItems(
  table: RouteTable,
  pathname: string,
  basename?: string,
): readonly NavItem[] {
  const labeled: RouteDeclaration[] = []
  let selectedPath: string | undefined
  let selectedLength = -1

  for (const declaration of table) {
    if (declaration.label === undefined) continue
    labeled.push(declaration)
    const selector = selectionSelector(declaration.path, pathname)
    if (selector !== undefined && selector.length > selectedLength) {
      selectedPath = declaration.path
      selectedLength = selector.length
    }
  }

  return labeled.map((declaration) => ({
    id: declaration.path,
    label: declaration.label,
    href: withBasename(basename, declaration.path),
    selected: declaration.path === selectedPath,
  }))
}

/**
 * The declaration path with the deployment prefix applied, following the
 * rule react-router-dom applies to `href`s: the root path resolves to the
 * prefix itself, and an absent, empty or root prefix leaves the path alone.
 */
function withBasename(basename: string | undefined, path: string): string {
  if (basename === undefined) return path
  const prefix = basename.replace(/\/+$/, '')
  if (prefix === '') return path
  return path === '/' ? prefix : `${prefix}${path}`
}

/**
 * The string a labeled declaration ranks by against the current path, or
 * `undefined` when it is not a candidate. A full pattern match ranks by the
 * whole path; otherwise a path below the declaration's literal path ranks
 * by that literal path.
 */
function selectionSelector(
  pattern: string,
  pathname: string,
): string | undefined {
  if (matchesPattern(pattern, pathname)) return pathname
  const literal = literalPath(pattern)
  if (isUnder(pathname, literal)) return literal
  return undefined
}
