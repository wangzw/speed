/**
 * Public entry of @speed/tenancy-ui.
 *
 * TenantSwitcher, the tenant-switch affordance over a host-owned tenant
 * list: a trigger showing the current tenant (host-supplied name, or the
 * noCurrentTenant text when the host has no current tenant yet) that
 * opens the host-supplied list; picking a row that is not the current
 * tenant closes the menu, calls onSelect(id) exactly once and announces
 * the switched-to tenant in a role="status" notice. The current-tenant
 * row is disabled and never re-triggers a selection.
 *
 * The tenant is a request's routing fact, so switching tenants is a host
 * route change, not a session operation: the component is controlled and
 * knows no session -- the tenant list and the current tenant id are host
 * data. Nothing here consumes the auth-core hooks, reads storage,
 * navigates, or touches the network. The host decides what a selection
 * means for its own state: it switches its route, updates the tenant id
 * it passes back as currentTenantId (so the trigger converges), updates
 * the tenant context it mounts on auth-core's TenantProvider, and evicts
 * the departing tenant's query cache (auth-core's evictTenantQueries).
 * Helpers shared between the component and its suite live in
 * src/internal/ and are deliberately not exported.
 */

export {
  TENANCY_UI_NAMESPACE,
  tenancyUiResources,
} from './resources.js'
export { TenantSwitcher, type TenantSwitcherProps } from './TenantSwitcher.js'
export type { TenantOption } from './TenantSwitcher.js'
export {
  TenantMenuItems,
  type TenantMenuItemsProps,
} from './TenantMenuItems.js'
export {
  TenantSwitchAnnouncement,
  type TenantSwitchAnnouncementProps,
} from './TenantSwitchAnnouncement.js'
