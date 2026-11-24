import type { ReactNode } from 'react'
import type { FeatureName } from '@speed/api-sdk/features'

/** The path parameters a matched declaration's `:name` segments collect. */
export type NotificationRouteParams = Readonly<Record<string, string>>

/** One match's context: the matched path and its parameters. */
export interface NotificationRouteContext {
  /** The matched path, query string excluded. */
  readonly path: string
  /** The matched path's parameters. */
  readonly params: NotificationRouteParams
}

/** One page declaration, structurally route-kit's RouteDeclaration. */
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
  /** The feature names this page needs (data; this package never decides
   * on it). */
  readonly requires?: readonly FeatureName[]
}

/** The notification surface's page set: a collection of declarations. */
export type NotificationRouteTable = readonly NotificationRoute[]
