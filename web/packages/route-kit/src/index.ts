/**
 * Public entry of @speed/route-kit.
 *
 * A product's routing and navigation in one package: a declarative
 * registration surface (RouteTable of RouteDeclaration) that RouteProvider
 * installs over react-router-dom, and the consumers that read it --
 * RouteView (the matched page or the fallback), useNavItems (the
 * label-derived navigation entries with selection), RouteLink and
 * useNavigate. The package imports no sibling frontend package and no
 * concrete business page; it is consumed by a product's assembly, never by
 * the shell. Everything not listed here is internal.
 */

export { RouteProvider, type RouteProviderProps } from './RouteProvider.js'
export { RouteView } from './RouteView.js'
export { RouteLink, type RouteLinkProps } from './RouteLink.js'
export { useNavItems, useNavigate } from './hooks.js'
export type {
  NavItem,
  RouteContext,
  RouteDeclaration,
  RouteParams,
  RouteTable,
} from './types.js'
