/**
 * Public entry of @speed/layout-kit: the auth-agnostic app chrome (AppShell,
 * RouteGuard, Avatar), their namespace bundle, and the plain-primary-click
 * judgement.
 */

export { LAYOUT_KIT_NAMESPACE, layoutKitResources } from './resources.js'
export { isPlainPrimaryClick } from './isPlainPrimaryClick.js'
export {
  AppShell,
  type AppShellNavEntry,
  type AppShellNavGroup,
  type AppShellNavItem,
  type AppShellNavSlot,
  type AppShellNavSlotState,
  type AppShellProps,
  type AppShellShellLayout,
} from './components/AppShell.js'
export {
  Avatar,
  avatarInitials,
  type AvatarProps,
} from './components/Avatar.js'
export {
  RouteGuard,
  type RouteGuardProps,
  type RouteGuardStatus,
} from './components/RouteGuard.js'
