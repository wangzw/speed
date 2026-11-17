# pkg/notification/stream

The notification capability's real-time push module (`notification.stream`). It
pushes "the inbox just gained a row" to the clients already connected over
`GET /stream`, so they do not have to poll. It delivers no capability of its
own: it subscribes to an event and pushes to a connection, and neither end goes
through a capability token.

The descriptor declares the static feature `notification.stream`, present
whenever the module is in force; the frontend opens the SSE connection only when
it is.

It subscribes to `notification.inbox.created` on the event bus — the type name
the inbox module declares, spelled out here as a literal and never imported, so
a subscriber reaches no publisher's package — and delivers each event to the
connections whose scope matches the event's `(tenant_id, recipient_user_id)`.

## What the Hub is and is not

- **One Hub per replica, in memory.** It holds only this replica's connections,
  by `(tenant, recipient)` scope. There is no cross-replica Hub and no
  Pub/Sub seam: a message written on replica A cannot reach a client connected
  to replica B.
- **The Hub's only value is latency.** The row is committed and the event
  published after it, so a push that reaches nobody loses nothing — the client
  reads the row back from the inbox. The Hub stores no message, guarantees no
  delivery and does not retry or reconnect.
- **`Publish` never blocks and never reports an error.** Each connection has a
  fixed buffer of 64 frames. A matched connection gets one non-blocking send; a
  connection whose buffer is full drops its own frame, and dropping is
  per-connection — one slow client never delays another or the publisher.
- **Matching happens before any buffering.** An event is looked up by its
  `(tenant, recipient)` scope, so a connection outside that scope never takes a
  send at all.
- **The message channel is never closed.** `Close` only signals a
  per-connection `done` and unregisters the connection. Closing a channel a
  concurrent `Publish` may be sending on is the race that would panic a
  concurrent Hub; leaving it open means a late send either lands in the
  still-existing buffer or is dropped by the non-blocking branch, and neither
  panics.
- **The buffer depth of 64 is a fixed value, not a configuration item.** It
  bounds one connection's tolerance.

## The route

- **`GET /api/v1/notifications/stream`** (base path `/api/v1/notifications`),
  mounted on the endpoints the module's configuration lists. Its answer is one
  `text/event-stream`. The module declares no permission point: who may connect
  is the host's authorization decision.
- **Reading the caller.** The recipient is the identity's effective account
  (`Identity.Account`), so an impersonating caller sees the impersonated
  account's inbox. A request with no identity is a 401.
- **The tenant** comes from the request context, written by the resolving layer;
  the route reads it and does not judge it.
- **The writer.** The module walks the response-writer chain by `Unwrap()`
  looking for an `http.Flusher` — a layer that replaced the writer leaves both
  the flusher and `Unwrap` behind — and answers 500 when the chain ends with
  none. There is no fallback to a one-shot body.
- **The opening timing.** The status and headers are flushed before the first
  event is built, so a client learns the stream is open without waiting for an
  event to arrive. Every event is flushed as it is written.
- **The frame** is `event: message\n`, `data: <json>\n`, and a blank line:
  `event: message\ndata: {…}\n\n`. The event name, the JSON field names and the
  trailing blank line are a promise — a client parses on them. The JSON is the
  subscribed event's payload (`message_id`, `tenant_id`, `recipient_user_id`,
  `type_key`); a client uses `message_id` to read the row back from the inbox.

## Boundaries

- **The module delivers no capability and registers `notification.stream`.** It
  carries no product another module resolves; it is taken in only to serve the
  stream route. Its stance is `StateEnabled`, never `StateAuto`: whether to
  serve the route is a decision on the command tree.
- **The event bus is required.** With no bus there is no event to push, so an
  assembly that takes this module in without one fails resolution. The HTTP
  entry point and the logging capability are optional: the route is mounted only
  when an entry point is present, and the logger is the optional diagnostic
  requirement. The gateway is not declared at all — this module never resolves
  one, and where the stream route sits relative to a gateway is the host's
  assembly rather than a line in this descriptor.
- **A push that reaches nobody is not lost.** There is no retry, no reconnect
  and no make-up push; the inbox row is the truth, and a client that must have
  the content reads it back from the inbox.

## Configuration

- **`notification.stream.endpoints`** (configuration, a list of strings): the
  endpoints the stream route is mounted on, by the names the entry point's own
  configuration gives them. It takes concrete names only, never `*`, because
  mounting the route on every endpoint would put it on the public ones, where no
  gateway guards it. A shape the module cannot run on — an empty list, an empty
  name, `*`, a name written twice — is `notification.ErrInvalidConfig`; a name
  the entry point does not declare is `http.ErrUnknownEndpoint` at startup.
- **The list is required of a run that mounts the route, and unread by a run
  that does not.** It is no `Required` configuration item: a command that
  assembles no HTTP entry point mounts nothing, reads the list nowhere, and
  starts with an empty list or none at all. The judgement runs in `Init`, and
  only when the entry point is in hand — a run that mounts the route has to name
  the endpoints, and a list that names none would drop the route silently.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

CI runs the segments of `make check` as parallel jobs. On this module alone:

```
go -C pkg build ./notification/...
go -C pkg vet ./notification/...
go -C pkg test ./notification/stream/...
go -C pkg test -race ./notification/stream/...
env -C pkg golangci-lint run ./notification/stream/...
```

The tests ask for no service and no environment. The Hub cases drive the
concurrency model directly (a slow consumer does not block a publish, `Close` is
idempotent and never closes the message channel, scope matching isolates one
pair from another). The route cases mount the route on a real `gin` engine and
drive it over an `httptest` recorder with a real identity from `authntest` and a
stand-in event bus, so a case can assert the stream content type, the frame
shape, the 401 with no identity, and the 500 with a writer that cannot flush.
The assembly cases drive the descriptor through a real registry, which is where
the required bus and the optional surfaces are observed.
