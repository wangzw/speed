# AGENTS.md for `@speed/notification-ui`

The notification-center component family of a speed frontend: the header
bell with its unread badge (`NotificationBell`), the inbox surface with
per-message and mark-all read (`NotificationInbox`), the per-type channel
preference matrix (`NotificationPreferences`), the composed page
(`NotificationCenter`), the live server-sent-event subscription
(`NotificationStreamProvider`), the host's feature-presence facts
(`NotificationPresenceProvider`), and the declarative route
(`notificationRoutes`). It sits at the capability-UI tier: it renders over
the host's React/MUI tree, reads and writes through the react-query hooks
generated into `@speed/api-sdk` over the host's QueryClient, and opens its
live stream through the assembled client's event-stream opener.

## What this package is

- **Controlled surfaces over a host-owned QueryClient.** Reads are the
  generated react-query hooks wrapped in `src/internal/queries.ts` and
  keyed under the acting tenant (`@speed/auth-core`'s `tenantQueryKey`,
  via `useTenantQueryKey`); writes are generated mutations whose success
  invalidates the tenant's notification reads. Nothing here builds a
  client, hand-writes a query key, reads storage, navigates or touches
  HTTP directly.
- **The live stream lives in the transport, not here.** The subscription
  opens `GET /notifications/stream` through the host-supplied opener
  (`@speed/api-client`'s `EventStreamOpener`, the assembled client's
  `openEventStream`) and, on a `message` frame, invalidates the tenant's
  reads -- it never patches a cache from the frame, which names only a
  message id (the stream module's contract). Reconnecting with capped
  exponential backoff, pausing while the tab is hidden, and stopping the
  loop on the admission layer's bare 404 are this provider's policy,
  because the transport neither times out nor reconnects. A refusal by
  the admission layer cannot change through retry -- every attempt would
  refresh a console error -- so a rejected connection stops reconnecting
  until the tenant or effective identity changes or a hidden tab becomes
  visible. The endpoint path is taken from the generated contract's own
  query-key constructor (`getNotificationStreamSubscribeQueryKey`), never
  hand-written.
- **The stream gate is the host's.** `NotificationStreamProvider`'s
  `enabled` prop defaults true; a host passes false while its session is
  anonymous, because the stream is authenticated and the package reads no
  session state for it. (The package does read the acting tenant through
  auth-core, for query-key namespacing and the app's tenant provider.)
- **Every built-in string is bilingual and bundled.** All text renders
  from the `notification-ui` namespace (`NOTIFICATION_UI_NAMESPACE`, one
  `zh-CN.json` and one `en-US.json` under `src/locales/`, identical leaf
  keys), registered by the host exactly once through `@speed/i18n`
  alongside the `ui-kit` namespace. `resources.test.ts` pins the leaf-key
  parity and the error-code-to-bundle pairing.
- **The error surface is a code whitelist.** Every failure path resolves
  to one error code rendered in one `role="alert"` banner; codes outside
  the whitelist (`notification.bad_request`, `notification.message_not_found`,
  `notification.not_unsubscribable`, the session-lifecycle and transport
  codes) render `errors.unknown`, never a raw key.
- **Times render through `@speed/i18n`'s `formatInstant`** in the current
  UI language and the host's display time zone (`@speed/ui-kit`'s
  `useDisplayTimeZone`); with no provider the zone chain falls to the
  device's own zone, then UTC. A field the engine cannot parse renders
  as-is.
- **Feature presence is the host's fact, carried by context.**
  `NotificationPresenceProvider` takes whether the preference matrix is
  served and which optional channels exist; the package reads no feature
  state itself. Without a provider both facts fail closed: the center
  renders the inbox alone and issues neither the taxonomy nor the
  preference read, and a standalone matrix offers the in-app column only.
- **The optional surfaces are additive.** `NotificationInbox` accepts a
  group filter, a page size with previous/next paging driven by the
  response total, an `onOpen` action and a per-row detail renderer, and an
  embedded variant; `NotificationPreferences` accepts a type label and a
  group label (grouping renders only when the label is given);
  `NotificationCenter` passes optional `inbox` and `preferences` props
  through; `NotificationBell` accepts an `icon` and hands `onClick` the
  activation event. None of them changes behaviour when absent.

## Invariants (code review enforces; do not weaken)

- **The package imports no router and does no path matching.**
  `notificationRoutes` is structurally `@speed/route-kit`'s
  `RouteDeclaration` (see `src/route.ts`); the shapes are copied, not
  imported, so same-tier packages never depend on one another. The host's
  composition layer collects the declaration.
- **The tenant refusal is auth-core's classification.** The read surfaces,
  the write banner's bare-404 text and the stream's reconnect stop all
  classify the admission layer's bare 404 through `@speed/auth-core`'s
  `isTenantAdmissionRefusal`; no copy of that predicate or its synthesized
  code lives here, and a typed module refusal at the same status is never
  read as the tenant refusal.
- **No hand-written HTTP, no hand-written query keys.** Endpoint paths and
  response types come from `@speed/api-sdk`; the only path this package
  names is derived from the generated contract.
- **`read_at` may be `null` at runtime.** The server's message view
  carries a `*time.Time` with no omitempty, so an unread message's
  `read_at` is JSON `null`; the generated type models the field as an
  optional string. Treat `null`, `undefined` and `""` alike.
- **The package never reads the session for the stream.** The `enabled`
  gate is the host's; this package keeps only the tenant read (auth-core's
  `useCurrentTenant`) that the query keys need.
- **Keep the framework neighbours as peers.** react, react-dom,
  `@mui/material`, `@emotion/*` and `@tanstack/react-query` are peers
  (`src/package.json.test.ts` pins the manifest); a second copy of any of
  them in a consumer's bundle breaks the theme or the query context.

## The jsdom canvas shim

`test-utils/setup.ts` replaces jsdom's not-implemented
`HTMLCanvasElement.getContext` with a quiet null-returning stub. axe-core
probes a canvas to detect icon ligatures in a short visible text node (a
badge's count is one), and jsdom's stock stub emits a `jsdomError` for the
probe. The scan's verdict is unchanged -- axe handles the absent context
without failing -- so the shim only removes the environment's noise; it
changes no asserted result.

## Building and testing

`pnpm lint`, `pnpm typecheck`, `pnpm test`, `pnpm build` from the package
directory, all zero warnings. The build rebuilds the five workspace
siblings it consumes (`@speed/api-client`, `@speed/api-sdk`,
`@speed/auth-core`, `@speed/i18n`, `@speed/ui-kit`) before emitting its own
`dist/`.
