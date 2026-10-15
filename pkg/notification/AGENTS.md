# pkg/notification

The notification capability: turn one business event into a message a recipient
can read, and hand it to that recipient. It covers four things and one seam
each — an in-app inbox, a per-type channel preference matrix, an outbound
delivery pipeline, and a server-sent-event push. The root package is the
interface package: it carries the seams, the common types and the sentinels, and
registers no module.

The capability splits into modules, each with its own table and migrations:

- **`notification.inbox`** keeps the in-app inbox — the `in_app_messages` table
  and the `Write`, `List`, `UnreadCount`, `MarkRead` and `ReadAll` behind the
  `Inbox` seam. It publishes `notification.inbox.created` after a write, and it
  is the only publisher of that event: whoever writes the row announces it.
- **`notification.preferences`** keeps the per-type channel matrix — the
  `notification_preferences` table and the `Preferences` seam — and is one of
  the two consumption points of the type taxonomy.
- **`notification.delivery`** is the outbound pipeline: it delivers the
  `Dispatcher` submission seam, registers a job handler, and records one
  `notification_sends` row per channel attempt. It publishes no event — the
  in-app row is announced by the inbox that wrote it.
- **`notification.stream`** is the SSE push: one in-process hub per replica,
  subscribed to `notification.inbox.created`, served on `GET /stream`.

The seams the root package defines:

- **`Inbox`** is the in-app inbox a recipient's messages live in: `Write`,
  `List`, `UnreadCount`, `MarkRead` and `ReadAll`. Every method acts on the
  tenant in the context and a recipient passed explicitly.
- **`Preferences`** is the per-type channel matrix: `Types` and `Type` read the
  taxonomy, `Effective` reports the channels a recipient's dispatches of a type
  would go out under, and `Set` replaces a recipient's channel set for a type.
- **`Dispatcher`** is the submission seam a business module calls:
  `Dispatch(ctx, d)` validates a `Dispatch` and enqueues it as one delivery job,
  returning the job identifier. It only enqueues — it does not render, does not
  deliver, and writes no ledger. The delivery handler validates and delivers
  again when it consumes the job.

`Message` is one rendered in-app message; the inbox reads and writes through it.
`ValidateTypeSpec` is the one criterion a declaration is held to, applied by both
consumption points of the taxonomy; a consumption point wraps its refusal in
`ErrInvalidConfig` and its own diagnostic, and the criterion's set-level rule —
one key two modules declare — is applied where the declarations are collected.
The package's godoc is the API — read `Inbox`'s, `Preferences`'s, `Dispatcher`'s
and `TypeSpec`'s comments before adding a module or a consumer.

## Boundaries

- **A notification type is the declaring business module's, not this
  capability's.** A business module places a `TypeSpec` in its module
  descriptor's `Resources`; the two consumption points — `notification.preferences`
  and `notification.delivery` — collect every declaration in their `New` and use
  it as the reference for a preference write and a dispatch. This capability
  defines no type and owns no type vocabulary: it does not know what
  `billing.invoice_paid` means. The taxonomy's source of truth is the set of
  module descriptors, which is frozen before the driver runs, so there is no
  runtime-mutable registry to register a type with. Adding a type is a
  declaration, not a change to this capability.
- **The channels are a closed platform vocabulary.** A channel is one of
  `in_app`, `email` and `sms` — the values of `Channel`, with `KnownChannel`
  deciding membership. A business module picks the channels a type goes out
  under from this set and cannot extend it; a new channel is a change to this
  capability. A channel's availability is a matter of assembly: `email` needs
  the mail sender seam, `sms` needs the sms registry and sender seams, and
  `in_app` is always available. A dispatch that meets an absent seam records a
  `skipped` row and moves on — it is not a failure and is not retried, because
  the absent seam is the assembly's choice, not a defect of the message.
- **This capability declares no permission point.** Its routes carry no
  permission gate of their own: no module declares a permission for them, so
  they authenticate as usual and decide nothing. Who may read a recipient's
  inbox or change a recipient's preferences is the host's authorization
  decision — the host adds its own gate or constrains the endpoints by its own
  model. This capability therefore does not appear in a permission-spec or a
  permission-translation-spec declaration.
- **The non-user external-contact consent ledger is deliberately deferred, not
  omitted.** Sending to a non-user external contact — a phone number or an email
  address outside this system's accounts — needs its own consent ledger: a
  verified-contacts table, a contact-type unsubscribe table, a platform-level
  blocklist, and a `/contacts` family of routes. None of it lands in this
  release. `RecipientClass` keeps `external` in its closed set so the set is
  stable, but there is no producer: a dispatch to an external recipient is
  refused, and the `notification.contact.*` audit actions are not declared.
- **This capability does not send email or sms itself.** Each outbound external
  channel goes through a seam: email through the mail capability's sender, sms
  through the sms capability's registry and sender. This capability chooses a
  template, renders text and builds a request; the actual delivery and vendor
  interaction belong to those capabilities. Email is not implemented in this
  release — the mail capability is not in the tree yet — so the `email` channel
  is always unavailable, exactly as an absent seam would be, and records a
  `skipped` row.
- **This capability does not aggregate or rate-limit.** One dispatch writes one
  message and sends once; merging several messages of one type into one, and
  capping sends per recipient, are not this capability's. A host that needs a
  per-recipient send cap imposes it through the rate-limit capability.
- **This capability produces no platform-domain row.** Every row belongs to one
  tenant: a recipient is an account, and an account belongs to a tenant.
  Platform-level broadcast and the external-contact consent ledger are the same
  shape — they need a recipient that belongs to no tenant, and this release has
  none.
- **The root package registers no module.** Each module is registered by its own
  implementation subpackage. Among this repository's packages the root package
  reaches one only — the jobs interface package, for the job identifier
  `Dispatcher.Dispatch` returns — and `deps_test.go` holds the root package's
  non-test dependency face to the standard library and that package alone. The
  jobs interface package is itself standard-library only, so a business module
  that only builds a notification type depend on this package without carrying
  storage or the tenant.

## How a consumer uses it

A business module declares a type and dispatches a notification:

```go
// The declaring module registers its type alongside its other resources.
core.ProcessRegistry.Register(core.Module{
    Name:      "billing",
    Resources: []any{notification.TypeSpec{
        Key:                    "billing.invoice_paid",
        Group:                  "billing",
        DefaultChannels:        []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
        Unsubscribable:         true,
        RecipientVisibleParams: []string{"invoice_id", "amount"},
    }},
})

// It dispatches through the submission seam.
dispatcher, _ := core.Resolve[notification.Dispatcher](reg)
_, err := dispatcher.Dispatch(ctx, notification.Dispatch{
    TypeKey:      "billing.invoice_paid",
    Recipient:    notification.Recipient{Class: notification.RecipientUser, UserID: userID},
    Locale:       "zh-CN",
    Params:       map[string]any{"invoice_id": inv.ID, "amount": inv.Total},
    OccurrenceID: inv.ID, // stable identity of the logical event, for dedupe
})
```

A consumer that reads the inbox or the preferences resolves `notification.Inbox`
or `notification.Preferences` the same way and passes the recipient explicitly.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

`make check` is what CI runs. On this capability alone:

```
go -C pkg build ./notification/...
go -C pkg test ./notification/...
go -C pkg test -race ./notification/...
env -C pkg golangci-lint run ./notification/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages.
