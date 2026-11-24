/**
 * The effective registration surface: the part of a table that can render and
 * navigate, narrowed by the host's feature-presence predicate. Navigation,
 * exact match and entry checks all read this one result.
 */

import type { RoutePresence } from './types.js'

export type PresenceDeclarable = {
  readonly requires?: readonly string[]
}

export function isGated(declaration: PresenceDeclarable): boolean {
  return declaration.requires !== undefined && declaration.requires.length > 0
}

/** The declarations a host's presence predicate leaves in force; a gated one is kept only when the predicate is ready and every name is present. */
export function presentDeclarations<T extends PresenceDeclarable>(
  table: readonly T[],
  presence: RoutePresence | undefined,
): readonly T[] {
  const ready =
    presence !== undefined && presence.state === 'ready' ? presence : undefined
  return table.filter((declaration) => {
    if (!isGated(declaration)) return true
    if (ready === undefined) return false
    const requires = declaration.requires ?? []
    return requires.every((name) => ready.has(name))
  })
}
