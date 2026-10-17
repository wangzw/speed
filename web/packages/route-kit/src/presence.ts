/**
 * The effective registration surface: the part of a table that can render
 * and navigate, narrowed by the host's feature-presence predicate.
 *
 * This is the one place the narrowing rule lives. `useNavItems`, `useMatch`
 * and `useHasRoute` all read the value this function returns, so the pages
 * listed in navigation, the page an exact match hands back and the answer a
 * call-to-action gets are by construction the same set -- a gated page that
 * is absent behaves as a page that does not exist.
 */

import type { RoutePresence } from './types.js'

/** A declaration carrying the optional feature requirement list. */
export type PresenceDeclarable = {
  readonly requires?: readonly string[]
}

/**
 * Whether a declaration is gated at all. An absent list and an empty list
 * both require nothing, so both keep the declaration outside the predicate's
 * influence.
 */
export function isGated(declaration: PresenceDeclarable): boolean {
  return declaration.requires !== undefined && declaration.requires.length > 0
}

/**
 * The declarations a host's presence predicate leaves in force.
 *
 * Ungated declarations are always kept. A gated declaration is kept only
 * when the predicate is ready and every name it requires is present; while
 * the predicate is absent, pending or failed, every gated declaration is
 * withheld.
 */
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
