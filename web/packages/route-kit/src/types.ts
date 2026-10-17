/**
 * The registration surface's shared vocabulary: what a product declares and
 * what the package hands back. These shapes are the host-visible contract
 * (see the package README's Stability section and AGENTS.md); changing one
 * after release is a breaking change.
 */

import type { ReactNode } from 'react'

/** The values a matched path pattern captured, keyed by `:name`. */
export type RouteParams = Readonly<Record<string, string>>

/** One match: the path that was hit and the params it captured. */
export interface RouteContext {
  /** The matched path, without its query string. */
  readonly path: string
  /** The captured path params (empty for a literal-pattern match). */
  readonly params: RouteParams
}

/** One page: a path pattern, its view, and optional navigation metadata. */
export interface RouteDeclaration {
  /** The path pattern, e.g. `/cases/:caseId`. Literal segments match by equality; `:name` captures one segment. */
  readonly path: string
  /** The view this path renders when it matches. */
  readonly view: (context: RouteContext) => ReactNode
  /** The navigation label; a declaration without one does not contribute a nav item. */
  readonly label?: ReactNode
  /**
   * The permission code this page needs. It is data: this package carries it
   * but never reads it -- authorization is the host composing a RouteGuard.
   */
  readonly permission?: string
  /**
   * The feature names this page needs, all of which must be present for the
   * declaration to render and to appear in navigation. It is data: this
   * package hands the names to the host's presence predicate and never
   * interprets them. A declaration without this field (or with an empty
   * list) is not gated and is unaffected by the predicate.
   */
  readonly requires?: readonly string[]
}

/**
 * The host's answer to "is this feature present?", handed to `RouteProvider`.
 * It is a structural shape: a host derives it from its own feature state
 * without a shared type edge. `pending` and `failed` are the two states in
 * which no gated declaration is present yet.
 */
export type RoutePresence =
  | { readonly state: 'pending' }
  | { readonly state: 'failed' }
  | {
      readonly state: 'ready'
      readonly has: (name: string) => boolean
    }

/** A product's pages: the one place it names which pages it is made of. */
export type RouteTable = readonly RouteDeclaration[]

/**
 * One navigation entry, derived from a `label`-bearing declaration. The
 * shape is structural (`id`, `label`, `href`, `selected`) so a shell's frame
 * accepts it without a shared type edge.
 */
export interface NavItem {
  /** The declaring path pattern, stable across renders. */
  readonly id: string
  /** The declaration's label. */
  readonly label: ReactNode
  /** The link target built from the declaration's path. */
  readonly href: string
  /** Whether the current URL selects this entry. */
  readonly selected: boolean
}
