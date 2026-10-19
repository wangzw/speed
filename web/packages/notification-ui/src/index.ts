/**
 * Public entry of @speed/notification-ui.
 *
 * The notification-center component family over the generated
 * notification operations of @speed/api-sdk: the header bell with its
 * unread badge (NotificationBell), the inbox surface with per-message and
 * mark-all read (NotificationInbox), the per-type channel preference
 * matrix (NotificationPreferences), the composed page
 * (NotificationCenter), the live server-sent-event subscription
 * (NotificationStreamProvider), the host's feature-presence facts
 * (NotificationPresenceProvider), and the declarative route (notificationRoutes).
 * Every built-in string renders from the bilingual notification-ui
 * namespace (NOTIFICATION_UI_NAMESPACE + notificationUiResources, which
 * the host registers alongside ui-kit's); the surfaces render through
 * ui-kit's shared primitives.
 *
 * The reads and writes go through the generated react-query hooks over
 * the host's QueryClient, keyed under the acting tenant (auth-core's
 * tenantQueryKey); nothing here builds a client, writes a query key by
 * hand, reads storage, navigates or touches the network directly. The
 * live stream is opened through the host-supplied opener -- the assembled
 * client's openEventStream (@speed/api-client) -- so the transport seams
 * stay in one place; a host that mounts no opener simply gets no
 * real-time updates. Helpers shared between the components live in
 * src/internal/ and are deliberately not exported.
 */

export { NOTIFICATION_UI_NAMESPACE, notificationUiResources } from './resources.js'
export {
  NotificationBell,
  type NotificationBellProps,
} from './NotificationBell.js'
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
