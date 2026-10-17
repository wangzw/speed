/**
 * Public entry of @speed/route-kit.
 *
 * A product's routing and navigation in one package: a declarative
 * registration surface (RouteTable of RouteDeclaration) that RouteProvider
 * installs over react-router-dom, and the consumers that read it --
 * RouteView (the matched page, the fallback, or the pending/failed view of a
 * feature-gated page), useNavItems (the label-derived navigation entries with
 * selection), useMatch and useHasRoute (exact matches over the same
 * feature-narrowed surface), RouteLink and useNavigate. The package imports
 * no sibling frontend package and no concrete business page; it is consumed
 * by a product's assembly, never by the shell. Everything not listed here is
 * internal.
 */

export { RouteProvider, type RouteProviderProps } from './RouteProvider.js'
export { RouteView } from './RouteView.js'
export { RouteLink, type RouteLinkProps } from './RouteLink.js'
export { useHasRoute, useMatch, useNavItems, useNavigate } from './hooks.js'
export { presentDeclarations } from './presence.js'
export type { RouteMatch } from './pathing.js'
export type {
  NavItem,
  RouteContext,
  RouteDeclaration,
  RouteParams,
  RoutePresence,
  RouteTable,
} from './types.js'
