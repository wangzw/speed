/**
 * Public entry of @speed/route-kit: the declarative registration surface and
 * its consumers (RouteView, the navigation and match hooks, link construction,
 * history moves). It imports no sibling frontend package.
 */

export {
  RouteProvider,
  type RouteNavigationFocus,
  type RouteProviderProps,
} from './RouteProvider.js'
export { RouteView } from './RouteView.js'
export { RouteLink, type RouteLinkProps } from './RouteLink.js'
export {
  useHasRoute,
  useLocationPath,
  useMatch,
  useNavItems,
  useNavigate,
  useRedirect,
  useRouteLinkProps,
  type RouteLinkTarget,
  type RouteLocationPath,
} from './hooks.js'
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
