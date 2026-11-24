/** Navigation derivation: an entry exists exactly when a declaration carries a `label`, and the current URL decides which one is selected. */

import { isUnder, literalPath, matchesPattern } from './pathing.js'
import type { NavItem, RouteDeclaration, RouteTable } from './types.js'

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

function withBasename(basename: string | undefined, path: string): string {
  if (basename === undefined) return path
  const prefix = basename.replace(/\/+$/, '')
  if (prefix === '') return path
  return path === '/' ? prefix : `${prefix}${path}`
}

function selectionSelector(
  pattern: string,
  pathname: string,
): string | undefined {
  if (matchesPattern(pattern, pathname)) return pathname
  const literal = literalPath(pattern)
  if (isUnder(pathname, literal)) return literal
  return undefined
}
