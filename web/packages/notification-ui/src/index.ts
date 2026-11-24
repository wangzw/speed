/**
 * Public entry of @speed/notification-ui: the notification-center
 * component family over the generated notification operations.
 */

export { NOTIFICATION_UI_NAMESPACE, notificationUiResources } from './resources.js'
export {
  resolveMessageText,
  type FormatInstant,
} from './internal/message-text.js'
export {
  NotificationBell,
  type NotificationBellProps,
} from './NotificationBell.js'
export {
  NOTIFICATION_BADGE_MAX,
  useNotificationUnread,
  type NotificationUnread,
} from './useNotificationUnread.js'
export {
  NotificationInbox,
  type NotificationInboxProps,
} from './NotificationInbox.js'
export {
  NotificationPreferences,
  type NotificationPreferencesProps,
} from './NotificationPreferences.js'
export {
  NotificationCenter,
  type NotificationCenterProps,
} from './NotificationCenter.js'
export {
  NotificationPresenceProvider,
  type NotificationPresenceProviderProps,
} from './NotificationPresence.js'
export {
  NotificationStreamProvider,
  useNotificationStreamOpener,
  type NotificationStreamProviderProps,
} from './NotificationStream.js'
export {
  notificationRoutes,
} from './routes.js'
export type {
  NotificationRoute,
  NotificationRouteContext,
  NotificationRouteParams,
  NotificationRouteTable,
} from './route.js'
