/**
 * Public entry of @speed/layout-kit.
 *
 * Shared, auth-agnostic app chrome: AppShell (responsive header + nav
 * drawer + content region) and RouteGuard (gates children on an
 * injected `RouteGuardStatus`, no concrete auth dependency) with their
 * layout-kit-namespace translations (LAYOUT_KIT_NAMESPACE +
 * layoutKitResources) for the host to register, plus the shared
 * plain-primary-click judgement (`isPlainPrimaryClick`) in-app navigation
 * links use. Everything else stays internal: helpers shared between
 * components live in src/internal/ and are deliberately not exported.
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
export { Avatar, type AvatarProps } from './components/Avatar.js'
export {
  RouteGuard,
  type RouteGuardProps,
  type RouteGuardStatus,
} from './components/RouteGuard.js'
