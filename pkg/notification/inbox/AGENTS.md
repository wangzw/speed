# pkg/notification/inbox

The in-app inbox module, registered as `notification.inbox`: it holds one
account's in-app message rows and their listing, unread count and marking-read,
and it mounts the inbox routes under `/api/v1/notifications`. It delivers one
capability, `notification.Inbox`, exclusively.

The interface package is `pkg/notification`; this package is the implementation
and registers the module. The API is the package's godoc, and the capability's is
the godoc of `notification.Inbox` in the interface package.

## What it does and does not do

A message row is written by the delivery pipeline through `notification.Inbox.Write`
— the one downstream writer of this module. The HTTP surface reads rows out and
marks them read; it never writes a message row. The title and body of a row are
rendered before they arrive: rendering, channel choice and outbound delivery
belong to the delivery module, and this module neither delivers nor pushes.

It is deliberately narrow:

- **No permission point.** Its routes carry no permission gate of their own; who
  may read a recipient's inbox is the host's authorization decision. The handler
  reads the caller's identity only to know whose inbox is being read.
- **No notification-type checking.** A `type_key` is stored as given; whether it
  is declared is the taxonomy consumption point's question, not this one's.
- **No read-time rewrite.** `read_at` moves from empty to one instant and is
  never rewritten, so a first-read timestamp is stable. The HTTP surface changes
  that column alone.

## Boundaries

- **A row is tenant-scoped.** The `in_app_messages` table embeds the tenant
  convention (`tenant.Model`), so its key leads with `tenant_id` and the roster
  module's filter scopes, fills and checks every statement this module issues
  through the model path. The tenant comes from the context, never from an
  argument; a statement with no tenant in the context is refused, and a write
  under it lands nothing. The recipient is passed explicitly — delivery has no
  request path and knows its own recipient, and a route reads the recipient from
  the identity.
- **`Write` is idempotent on the dedupe key.** The row is inserted under the
  unique `dedupe_key` index with `ON CONFLICT DO NOTHING`; a key already held
  writes nothing and the row already there is returned instead, so a repeat lands
  one row and publishes no second event. The dedupe key already carries the
  tenant, so one global unique index is enough. An empty recipient or an empty
  dedupe key is refused with `notification.ErrInvalidRequest`, with nothing
  written.
- **The row commits before the event.** `Write` inserts the row as its own
  committed statement and publishes `notification.inbox.created` after it,
  outside any transaction. A publish that fails does not roll the row back and
  does not reach the caller; a missing bus publishes nothing and the row still
  lands. The event is an acceleration and never a correctness dependency — a
  consumer that has to be sure reads the row back.
- **`MarkRead` writes only an unread row and answers one not-found.** The
  statement is conditional on `read_at IS NULL`, so it never rewrites a
  first-read timestamp. When it matches no row the recipient's own range is read
  for the identifier: a row that is there is one already read — a successful
  no-op that writes nothing — and one that is not there is an unknown identifier,
  another recipient's or another tenant's, all answered with the same
  `notification.ErrMessageNotFound` so no existence leaks.
- **`ReadAll`'s idempotency is the predicate.** Its statement is `WHERE
  recipient_user_id = ? AND read_at IS NULL`. A repeat matches nothing and
  returns zero, and an already-read row keeps its first-read timestamp. Whether a
  row is expired is not part of the predicate: an expired row is still marked
  read.
- **`List` and `UnreadCount` hide expired rows.** The judgement is `expires_at
  IS NULL OR expires_at > <now>`: an expired row is not listed and not counted.
  The listing's only filter is `group`; its order is fixed as `created_at DESC,
  id DESC`, so two rows written in one instant keep a stable order across pages;
  and `Total` is the number the filter matches, without the page bound.
- **The group column is a reserved word.** The migration quotes it (`"group"`)
  and the model reaches it through GORM's builder, which quotes it in turn; a
  raw statement naming it has to quote it too.
- **The HTTP surface writes nothing.** The four routes read, count and mark read;
  none calls `Write`.
- **The table name is one constant in three places.** `tableName`, the two
  migration files and the read-back statements all have to agree.

## Dependencies

The database and the tenant roster are hard requirements — the table lives in the
delivered database, and the roster is what installs the statement filter. The
event bus, the logging capability and the HTTP entry point are optional and are
declared as such: without a bus no created event is published, without logging the
records reach the process default logger, and without an entry point the routes
are not mounted. The whole capability is delivered exclusively, so a second
provider fails the startup.

It imports the authentication interface package for one thing: the package-level
`authn.FromContext`, which reads the caller's identity off the request context.
That is a context access, not a construction-time dependency, so the module
declares no requirement on the authentication capability.

## Configuration

The module's input items are mounted under its own namespace, `notification.inbox`
(the module name). It accepts one:

- **`endpoints`** (a list of strings, primary source): the listening endpoints the
  inbox routes are mounted on, by the entry point's own names. Concrete names
  only — the name that stands for every endpoint is refused, because it would put
  the routes on the public endpoints where no gateway guards them. The list is a
  set: a name written twice is refused, and an empty name is refused.

The endpoint list is read only when the HTTP entry point is present, and then it
is required: a router present with an empty list fails the startup naming the
key, rather than silently mounting nothing. A command with no entry point mounts
nothing and reads no endpoint list.

## Routes

Under the fixed base path `/api/v1/notifications`, with no permission gate. The
handler reads the caller's identity from the request context; no identity is a
401.

| Method and path | Answer |
|---|---|
| `GET /api/v1/notifications/messages` | 200, `{"messages": [Message...], "total": N}`; `group` filters, `limit` defaults to 50 and is refused outside 0..200, a negative offset is refused, all whole |
| `GET /api/v1/notifications/messages/unread-count` | 200, `{"unread": N}` |
| `POST /api/v1/notifications/messages/read-all` | 200, `{"read_count": N}` |
| `POST /api/v1/notifications/messages/:messageId/read` | 204, no body; 404 for a message this recipient cannot see |

A refused request writes a problem-details object whose `type` is under
`urn:speed:notification:` — `bad-request` for a request whose shape cannot be
read and `message-not-found` for a message this recipient cannot see. A database
that could not answer is a 503 with no problem type; its cause stays in the log.

## The event

`EventInboxCreated` (`notification.inbox.created`) is published after every
committed row. Its payload is the closed set `Created`:
`{message_id, tenant_id, recipient_user_id, type_key}`. The module name, the
configuration key, the route paths, the event type name and its payload fields,
and the problem `type` prefix are all host-visible surfaces.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

This module on its own:

```
go -C pkg build ./notification/inbox
go -C pkg test ./notification/inbox
go -C pkg test -race ./notification/inbox
env -C pkg golangci-lint run ./notification/inbox/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages.

The cases drive a real assembly — the database module, the logging module, the
roster module and this one — over SQLite and over PostgreSQL, the latter on the
server the compose stack brings up through `pkg/internal/pgpool/pgtest`. The
PostgreSQL leg carries the `integration` tag, so a default run compiles none of
it, and fails rather than skips when nothing answers there. Each case enters its tenant
through the roster and acts through the delivered `notification.Inbox`. The route
cases mount the module's own route set on a real gin engine and drive it with an
identity in the request context.
