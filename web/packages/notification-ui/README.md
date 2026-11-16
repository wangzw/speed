# @speed/notification-ui

The notification-center component family of the speed frontend: the
header bell with its unread badge, the inbox surface with per-message and
mark-all read, the per-type channel preference matrix, the composed page,
and the live server-sent-event subscription -- each over the generated
notification operations of `@speed/api-sdk` and the host's `QueryClient`.
Nothing here builds a client, reads storage, navigates or touches the
network directly; the reads and writes are the generated react-query hooks
keyed under the acting tenant, and the live stream is opened through the
host-supplied event-stream opener (the assembled client's
`openEventStream`).

## What ships

| File | Export |
|---|---|
| `src/NotificationBell.tsx` | `NotificationBell`, `NotificationBellProps` |
| `src/NotificationInbox.tsx` | `NotificationInbox`, `NotificationInboxProps` |
| `src/NotificationPreferences.tsx` | `NotificationPreferences`, `NotificationPreferencesProps` |
| `src/NotificationCenter.tsx` | `NotificationCenter`, `NotificationCenterProps` |
| `src/NotificationPresence.tsx` | `NotificationPresenceProvider`, `NotificationPresenceProviderProps` |
| `src/NotificationStream.tsx` | `NotificationStreamProvider`, `NotificationStreamProviderProps`, `useNotificationStreamOpener` |
| `src/routes.tsx` | `notificationRoutes` |
| `src/route.ts` | `NotificationRoute`, `NotificationRouteContext`, `NotificationRouteParams`, `NotificationRouteTable` |
| `src/resources.ts` | `NOTIFICATION_UI_NAMESPACE`, `notificationUiResources` |

`src/internal/` holds the family's shared pipeline (the tenant-keyed
read/write hooks, the error-text resolver, the inline error banner, the
date formatter and the translation hook) and is deliberately not exported.

## Quick start

Register the two namespaces (notification-ui's own and ui-kit's, because
the surfaces compose ui-kit components), mount the assembled client's
event-stream opener for live updates, hand the surfaces the host's
feature-presence facts, and place the bell in the shell's header beside
the notification page:

```tsx
import { registerNamespace } from '@speed/i18n'
import { AppThemeProvider, UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import { TenantProvider } from '@speed/auth-core'
import {
  NotificationBell,
  NotificationPresenceProvider,
  NotificationStreamProvider,
  notificationRoutes,
  NOTIFICATION_UI_NAMESPACE,
  notificationUiResources,
} from '@speed/notification-ui'

// One-time, before render:
registerNamespace(i18n, NOTIFICATION_UI_NAMESPACE, notificationUiResources)
registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)

// The header bell (opens the notification page), the live stream, and the
// feature-presence facts the surfaces read:
<TenantProvider tenantId={currentTenantId}>
  <NotificationPresenceProvider
    preferences={features.has('notification.preferences')}
    channels={[
      ...(features.has('notification.channel.email') ? ['email' as const] : []),
      ...(features.has('notification.channel.sms') ? ['sms' as const] : []),
    ]}
  >
    <NotificationStreamProvider openStream={api.openEventStream} enabled={signedIn}>
      <AppShell
        headerActions={<NotificationBell onClick={() => navigate('/notifications')} />}
        ...
      >
        {/* the route table carries notificationRoutes, whose view is the page */}
      </AppShell>
    </NotificationStreamProvider>
  </NotificationPresenceProvider>
</TenantProvider>
```

`openStream` is the assembled `@speed/api-client` client's
`openEventStream` (the one `bootstrapSpeedApp` binds the generated
operations onto, exposed on `SpeedAppServices.api`). `enabled` gates the
connection: pass your own session's authenticated flag so an anonymous
viewer never opens the authenticated stream.

`NotificationPresenceProvider` carries the host's feature facts, never a
feature state the package reads itself. Without it both fail closed: the
center renders the inbox alone and a standalone preference matrix offers
the in-app column only.

## The pieces

- **`NotificationBell`** reads the caller's unread count
  (`GET /notifications/messages/unread-count`) and renders it as an
  icon-only button whose accessible name carries the count ("Notifications:
  3 unread", or the bare label at zero). It is controlled: `onClick`
  receives the activation event and is the host's navigation, and an
  optional `icon` replaces the built-in glyph.
- **`NotificationInbox`** reads one page of messages
  (`GET /notifications/messages`) and renders each with title, body, time
  and an unread/read chip, plus a per-row "mark as read" action (POST
  `/notifications/messages/:id/read`) and a header "mark all as read"
  action (POST `/notifications/messages/read-all`). Both writes invalidate
  the acting tenant's reads, so the list and the bell move together.
  Optional props narrow and shape it: `group` filters the listing,
  `pageSize` adds previous/next paging driven by the response total,
  `renderDetail` appends per-row content, `onOpen` makes a message with a
  link activatable (it is marked read first, then handed to the host),
  and `variant: 'embedded'` with `headingLevel` drops the page header for
  a heading at the caller's level.
- **`NotificationPreferences`** reads the per-type preference matrix
  (`GET /notifications/preferences`) and the type taxonomy
  (`GET /notifications/types`, for the per-type unsubscribable flag), and
  PUTs one channel's target state (`PUT
  /notifications/preferences/:typeKey/:channel`). The in-app column is
  always present; the optional `email` and `sms` columns come from the
  presence provider, in the closed vocabulary's order. `typeLabel`
  relabels a type and `groupLabel` groups the rows (grouping renders only
  when the label is given).
- **`NotificationCenter`** is the default page: the inbox with the
  preference matrix beneath it when the presence provider says the
  feature is served. Optional `inbox` and `preferences` props pass through
  to the two surfaces.
- **`NotificationPresenceProvider`** carries the host's feature facts:
  whether the preference matrix exists and which optional delivery
  channels the deployment serves. It reads no feature state itself, and
  without it both facts fail closed.
- **`NotificationStreamProvider`** opens the server-sent-event stream
  (`GET /notifications/stream`) through the host opener and, on every
  `message` frame, invalidates the acting tenant's unread count and
  message list. Reconnecting (capped exponential backoff) and pausing
  while the tab is hidden are the provider's policy; the transport itself
  neither times out nor reconnects.
- **`notificationRoutes`** is the package's contribution to the
  declarative registration seam: the notification center at
  `/notifications`, labelled so it adds a navigation entry. The package
  imports no router and does no path matching.

## Notes

- Query keys are namespaced under the acting tenant (auth-core's
  `tenantQueryKey`), so a tenant switch never serves the previous tenant's
  rows.
- The generated type for a message's `read_at` is an optional `string | null`;
  the server writes JSON `null` for an unread message, so the surfaces treat
  `null`, `undefined` and `""` alike as unread.
- Times render through `@speed/i18n`'s `formatInstant` in the current UI
  language and the display zone supplied by `@speed/ui-kit`'s
  `DisplayTimeZoneProvider`; with no provider the zone falls to the
  device's own, then UTC.
- A deployment without the stream simply mounts no opener (or `enabled`
  false): the surfaces still read their data on mount and when the tab
  becomes visible again, and real-time is an enhancement, never a
  prerequisite.
