/**
 * The console's route-declaration types, kept structurally identical to
 * @speed/route-kit's registration surface. This package imports no route-kit;
 * a declaration it exports is assignable to `RouteDeclaration`.
 */

import type { ReactNode } from 'react'
import type { FeatureName } from '@speed/api-sdk/features'

/** The path parameters a matched declaration's `:name` segments collect. */
export type AdminRouteParams = Readonly<Record<string, string>>

/** One match's context: the matched path and its parameters. */
export interface AdminRouteContext {
  /** The matched path, query string excluded. */
  readonly path: string
  /** The matched path's parameters. */
  readonly params: AdminRouteParams
}

/**
 * One page declaration: the path pattern, the view, an optional navigation
 * label (absent keeps it out of the navigation) and an optional permission code
 * (data; this package never decides on it).
 */
export interface AdminRoute {
  /**
   * A stable per-area identity a sibling declaration is resolved by; absent, the
   * navigation key falls back to the path.
   */
  readonly id?: string
  /** The path pattern, e.g. '/tenants' or '/audit/records/:id'. */
  readonly path: string
  /** The view rendered when a URL matches this declaration. */
  readonly view: (context: AdminRouteContext) => ReactNode
  /** The navigation label; absent keeps this declaration out of the navigation. */
  readonly label?: ReactNode
  /** The permission code the page needs (data; this package never decides on it). */
  readonly permission?: string
  /**
   * The generated `FeatureName`s this page needs (data; this package never
   * decides on it). The host narrows its route table by these names before the
   * shell derives the navigation; the shell itself does not filter.
   */
  readonly requires?: readonly FeatureName[]
  /**
   * The navigation group this page belongs to, by an id from the console's group
   * table; ignored when the host passes no group table or no matching group.
   */
  readonly group?: string
  /**
   * The navigation entry's leading icon: decorative, since the label is the
   * accessible name. Ignored on a declaration without a `label`.
   */
  readonly icon?: ReactNode
}

/**
 * One console navigation group: a stable id, a translated heading, an optional
 * decorative icon and an optional initial collapse state.
 */
export interface AdminNavGroup {
  /** Stable identity, matching the `group` a declaration names. */
  readonly id: string
  /** The group's visible heading, resolved through the admin-ui namespace. */
  readonly label: ReactNode
  /** Optional decorative icon shown in the sidebar's collapsed icon rail. */
  readonly icon?: ReactNode
  /** Whether the group starts collapsed; absent means expanded. */
  readonly defaultCollapsed?: boolean
}

/** The console's page set: a collection of declarations. */
export type AdminRouteTable = readonly AdminRoute[]
