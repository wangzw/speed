/**
 * The notification surface's route-declaration types -- the structural
 * mirror of @speed/route-kit's registration surface.
 *
 * Like @speed/console-ui, this package reuses the declarative
 * registration seam: the notification surface exports one declaration --
 * a path pattern, a view callback and an optional navigation label --
 * and the application's composition layer collects it and hands it to the
 * route layer it chose. This package imports no router and does no path
 * matching: a declaration is data plus a view and nothing more.
 *
 * The shapes are kept structurally identical to route-kit's
 * RouteContext / RouteDeclaration / RouteTable. A declaration this
 * package exports is assignable to route-kit's RouteDeclaration without a
 * shared type edge between the two packages -- same-tier packages never
 * import one another.
 */

import type { ReactNode } from 'react'

/** The path parameters a matched declaration's `:name` segments collect. */
export type NotificationRouteParams = Readonly<Record<string, string>>

/** One match's context: the matched path and its parameters. */
export interface NotificationRouteContext {
  /** The matched path, query string excluded. */
  readonly path: string
  /** The matched path's parameters. */
  readonly params: NotificationRouteParams
}

/**
 * One page declaration. Structurally route-kit's RouteDeclaration: the
 * path pattern a URL matches against, the view the match renders, an
 * optional navigation label (absent keeps the page out of the navigation)
 * and an optional permission code (data; this package never decides on
 * it).
 */
export interface NotificationRoute {
  /** The path pattern, e.g. '/notifications'. */
  readonly path: string
  /** The view rendered when a URL matches this declaration. */
  readonly view: (context: NotificationRouteContext) => ReactNode
  /** The navigation label; absent keeps this declaration out of the
   * navigation. */
  readonly label?: ReactNode
  /** The permission code the page needs (data; this package never decides
   * on it). */
  readonly permission?: string
}

/** The notification surface's page set: a collection of declarations. */
export type NotificationRouteTable = readonly NotificationRoute[]
