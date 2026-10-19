/**
 * The console's route-declaration types -- the structural mirror of
 * @speed/route-kit's registration surface.
 *
 * The platform console reuses the declarative registration seam the
 * tenant-facing product shell uses: each page exports one declaration --
 * a path pattern, a view callback, an optional navigation label, an
 * optional permission code and the console's own optional metadata (a
 * stable id, a navigation group and an icon) -- and the application's composition layer
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
import type { FeatureName } from '@speed/api-sdk/features'

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
  /**
   * A stable identity for this declaration, per administration area
   * ('tenants', 'accounts', 'audit-detail', ...). A page that links to a
   * sibling console page resolves that page's declaration by this id
   * rather than by guessing its path, because the console may be mounted
   * under any prefix. Absent, the declaration has no sibling address (a
   * host-supplied page may omit it; the package's own declarations carry
   * one) and the navigation entry falls back to the path as its React key.
   */
  readonly id?: string
  /** The path pattern, e.g. '/tenants' or '/audit/records/:id'. */
  readonly path: string
  /** The view rendered when a URL matches this declaration. */
  readonly view: (context: ConsoleRouteContext) => ReactNode
  /** The navigation label; absent keeps this declaration out of the navigation. */
  readonly label?: ReactNode
  /** The permission code the page needs (data; this package never decides on it). */
  readonly permission?: string
  /**
   * The feature names this page needs (data; this package never decides
   * on it). The elements are the generated `FeatureName` type, narrower
   * than route-kit's string array, so a declaration still assigns to
   * RouteDeclaration while a misspelled name fails to compile. The host
   * narrows its route table by these names before the shell derives the
   * navigation; the shell itself does not filter.
   */
  readonly requires?: readonly FeatureName[]
  /**
   * Which navigation group this page belongs to, by a group id from the
   * console's own group table (`ConsoleNavGroup`). It is data the console
   * shell reads when it builds a grouped navigation; a host that passes
   * no group table renders the flat single column and this field is
   * ignored. Absent (or naming a group the table does not declare) leaves
   * the page's entry ungrouped.
   */
  readonly group?: string
  /**
   * The navigation entry's leading icon; host content, already resolved.
   * The console shell passes it to the frame's `AppShellNavItem.icon`, so
   * a collapsed sidebar rail shows the icon with its label clipped to the
   * entry's accessible name. Decorative by contract -- the label is the
   * entry's accessible name, so the icon should render `aria-hidden`.
   * Ignored on a declaration without a `label` (that page has no
   * navigation entry).
   */
  readonly icon?: ReactNode
}

/**
 * One console navigation group: a stable id, an already-translated
 * heading, an optional decorative icon and an optional initial collapse
 * state. The console shell renders groups in the order its group table
 * lists them, each group's entries in declaration order beneath it. The
 * shape is host-suppliable so a host can reorder or relabel the console's
 * groups.
 */
export interface ConsoleNavGroup {
  /** Stable identity, matching the `group` a declaration names. */
  readonly id: string
  /** The group's visible heading, resolved through the console-ui namespace. */
  readonly label: ReactNode
  /**
   * Optional decorative icon, shown in the sidebar's collapsed icon rail
   * in place of the clipped heading.
   */
  readonly icon?: ReactNode
  /** Whether the group starts collapsed; absent means expanded. */
  readonly defaultCollapsed?: boolean
}

/** The console's page set: a collection of declarations. */
export type ConsoleRouteTable = readonly ConsoleRoute[]
