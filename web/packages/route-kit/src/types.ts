/** The registration surface's shared vocabulary: what a product declares and what the package hands back. */

import type { MouseEventHandler, ReactNode } from 'react'

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
  /** The path pattern, e.g. `/cases/:caseId`; `:name` captures one segment. */
  readonly path: string
  /** The view this path renders when it matches. */
  readonly view: (context: RouteContext) => ReactNode
  /** The navigation label; a declaration without one does not contribute a nav item. */
  readonly label?: ReactNode
  /** The permission code this page needs; data, never read here. */
  readonly permission?: string
  /** The feature names this page needs; data, handed to the host's presence predicate. */
  readonly requires?: readonly string[]
}

/** The host's answer to "is this feature present?", handed to `RouteProvider`. */
export type RoutePresence =
  | { readonly state: 'pending' }
  | { readonly state: 'failed' }
  | {
      readonly state: 'ready'
      readonly has: (name: string) => boolean
    }

/** A product's pages: the one place it names which pages it is made of. */
export type RouteTable = readonly RouteDeclaration[]

/** One navigation entry, derived from a `label`-bearing declaration; structural, so a shell's frame accepts it without a shared type edge. */
export interface NavItem {
  /** The declaring path pattern, stable across renders; in-app navigation targets it. */
  readonly id: string
  /** The declaration's label. */
  readonly label: ReactNode
  /** The link target, with the router's basename included. */
  readonly href: string
  /** Whether the current URL selects this entry. */
  readonly selected: boolean
  /** A plain primary click navigates in-app; every other click keeps the browser's behaviour. */
  readonly onClick?: MouseEventHandler<HTMLElement>
}
