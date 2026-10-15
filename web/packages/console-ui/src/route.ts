/**
 * The console's route-declaration types -- the structural mirror of
 * @speed/route-kit's registration surface.
 *
 * The platform console reuses the declarative registration seam the
 * tenant-facing product shell uses: each page exports one declaration --
 * a path pattern, a view callback, an optional navigation label and an
 * optional permission code -- and the application's composition layer
 * collects the declarations and hands them to the route layer it chose.
 * This package does not import @speed/route-kit: it only exports values
 * shaped like RouteDeclaration, so a page is a declaration and nothing
 * about routing, link construction or path matching lives here (that is
 * the route layer's, and the composition layer's, whole job).
 *
 * The shapes below are kept structurally identical to route-kit's
 * RouteContext / RouteDeclaration / RouteTable. A declaration this
 * package exports is assignable to route-kit's RouteDeclaration without
 * a shared type edge between the two packages -- same-tier packages never
 * import one another.
 */

import type { ReactNode } from 'react'

/** The path parameters a matched declaration's `:name` segments collect. */
export type ConsoleRouteParams = Readonly<Record<string, string>>

/** One match's context: the matched path and its parameters. */
export interface ConsoleRouteContext {
  /** The matched path, query string excluded. */
  readonly path: string
  /** The matched path's parameters. */
  readonly params: ConsoleRouteParams
}

/**
 * One page declaration. Structurally route-kit's RouteDeclaration: the
 * path pattern a URL matches against, the view the match renders, an
 * optional navigation label (absent keeps the page out of the navigation)
 * and an optional permission code (data; this package never decides on
 * it). The route layer's authorization is the host's composition.
 */
export interface ConsoleRoute {
  /** The path pattern, e.g. '/tenants' or '/audit/records/:id'. */
  readonly path: string
  /** The view rendered when a URL matches this declaration. */
  readonly view: (context: ConsoleRouteContext) => ReactNode
  /** The navigation label; absent keeps this declaration out of the navigation. */
  readonly label?: ReactNode
  /** The permission code the page needs (data; this package never decides on it). */
  readonly permission?: string
}

/** The console's page set: a collection of declarations. */
export type ConsoleRouteTable = readonly ConsoleRoute[]
