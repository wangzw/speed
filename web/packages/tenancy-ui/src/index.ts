/**
 * Public entry of @speed/tenancy-ui: the tenant-switch affordance over a
 * host-owned tenant list.
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
