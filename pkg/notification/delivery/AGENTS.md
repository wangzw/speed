# pkg/notification/delivery

The notification capability's outbound delivery pipeline. One delivery intent
goes in, is validated, and is enqueued as one durable job; when the job is
consumed the intent is validated again, the recipient's effective channels are
resolved, and every channel is rendered, sent through its seam and recorded as
one ledger row.

## What it delivers

The module delivers `(*notification.Dispatcher)(nil)` **exclusively**: one
process has exactly one outbound pipeline, so a second provider fails the
startup. It registers a `jobs.Handler` under the job type `notification.delivery`
— the module's own name — in its `Init`, before the queue starts, so every
delivery job has a handler to run it.

The dependencies the descriptor requires, non-optionally, are:

| Token | Why |
|---|---|
| `(*jobs.Queue)(nil)` | `Dispatcher.Dispatch` enqueues through it. |
| `(*jobs.Handlers)(nil)` | the handler is registered through it. |
| `(*db.Database)(nil)` | the `notification_sends` table lives in it. |
| `(*tenant.Catalog)(nil)` | it delivers the statement filter that scopes, fills and checks the tenant column of every ledger statement. |
| `(*notification.Preferences)(nil)` | `Effective` answers a recipient's channels. |
| `(*notification.Inbox)(nil)` | the in-app channel writes through it. |
| `(*i18n.Localizer)(nil)` | the message text is rendered through it. |

The optional dependencies are `(*mail.Sender)(nil)`, `(*sms.Registry)(nil)` and
`(*sms.Sender)(nil)`. No absence fails the startup: a channel is available
exactly when the seams it sends through are present — email on the mail sender,
sms only when **both** its seams are there — and a missing seam records a
`skipped` row rather than a failure.

The descriptor declares nothing else. It names no HTTP entry point — this module
mounts no route, so declaring one would only carry `pkg/http` into a host's
closure — and no event bus: the in-app row is announced by the module that wrote
it, so this module publishes nothing on a bus and has none to reach.

## Email

The `email` channel is delivered through `pkg/mail`'s `Sender`: the module
renders the subject and the two bodies, assembles one `mail.Message` and hands it
to `Send` once. Whether the channel can send is a matter of assembly, like every
other seam: an assembly that delivered a carrier has it, and one that did not
records a `skipped` row rather than a failure.

**`Message.From` is left empty**, which takes the carrier's own configured
default sender — this module names no sender and has nowhere to get one. **Each
attempt builds its own `mail.Message` value**: a message carries its attachments
as `io.Reader` values, so one value can be sent only once. This module attaches no
file, so nothing here is spent, and the value is never carried across attempts
either.

**The email destination is a dispatch parameter**, the same `to` the sms channel
reads: a recipient is an account identifier, and this module reaches no account
store. A dispatch whose parameters name no `to`, or whose `to` cannot be
stringified, fails that channel with `notification.ErrInvalidRequest` and this
module's own reason, and the carrier is never reached.

## The two halves

**`Dispatcher.Dispatch` only validates and enqueues.** It runs the shape checks
(a non-empty `TypeKey`, a recipient whose class is `user`, a non-empty
`Recipient.UserID`, a non-empty `Locale`), then the taxonomy checks (the type is
declared, and the parameters do not exceed the type's `RecipientVisibleParams`
whitelist), then enqueues one job. It resolves no channel, renders no text and
writes no storage. The tenant comes from the context; a context with none is
refused with the tenant capability's own error.

**The handler consumes the job.** `jobs.db` claims it, enters its tenant and
hands it over, so the handler's context already carries the tenant. The handler
decodes the payload, runs the same validation again — a worker can meet a
payload the submitter never wrote, or a row that has since been changed —
resolves the recipient's effective channels through `Preferences.Effective`, and
walks them. A channel that fails does not starve the others and is not hidden by
their successes: every channel is walked and their errors are joined into what
the handler returns, which is what makes the job retry.

**The in-app row is written, not announced.** The handler writes it through
`notification.Inbox`, which is the module that publishes `notification.inbox.created`
once that row has committed. This module publishes no event of its own: one row
announced twice would push twice to a connected client.

## The idempotency key

Every channel attempt derives a key from a canonical encoding:

```
idempotency_key = hex(sha256(canonicalJSON{
    tenant, type_key, recipient_class, user_id, channel, locale, occurrence_id, params
}))
```

Object keys are sorted and values are normalized through one JSON round trip, so
equal values always encode to the same text. The key a job is enqueued under
drops the `channel` dimension, so two dispatches of one logical event — same
tenant, type, recipient, language, occurrence and parameters — converge into one
job through the queue's own `(tenant_id, idempotency_key)` uniqueness.

## The ledger and at-least-once delivery

`notification_sends` is this module's own table; no other module reads or writes
it, and its columns are an operations view of what went out, not a contract face.
One row is one channel's terminal state: `succeeded`, `failed` or `skipped`. The
unique index `(tenant_id, idempotency_key)` is what makes the ledger idempotent.

Before a channel is attempted its key is looked up. A channel already settled —
`succeeded` or `skipped` — is not attempted again: a success is final, and a
skipped channel stays skipped because the seam this assembly did not deliver is
not delivered by a retry either. A channel whose row is `failed` **is** attempted
again, which is what a retry of the job is for; the new result updates the same
row rather than leaving the first failure beside it. A delivery whose ledger
write fails returns an error, so the job retries.

The result is at-least-once delivery: a repeated consumption does not deliver a
settled channel twice, and only the channels that are not settled are made up.

## Rendering

The message text is rendered in the backend, in the recipient's language
(`Dispatch.Locale`), through `i18n.Localizer`. A part is keyed
`<type_key>.<channel>.<part>` — `title` and `body` for `in_app`; `subject`,
`body_text` and an optional `body_html` for `email`; and a template identifier for
`sms` — and belongs to the business module that **declares** the type, not to this
module. This module owns only its own text: the reasons it writes into the ledger
for the channels it judged itself, under `locales/`, one file per locale with the
same key set. The parameter whitelist has already been enforced by the time text
is rendered, so only the visible parameters enter the rendering context.

A part the engine falls back to the key for is written out like any other: the
key is text, and a missing catalog entry is not this module's to refuse. The two
parts a client would not survive as a key are the exception, and they are written
only when the catalog carries them — `link` for `in_app`, and `body_html` for
`email`, which would otherwise put a key string into markup a mail client
renders.

Sms parameters are stringified before the vendor is reached: a string as it is,
an integer or a boolean in its text, a time in RFC 3339. A parameter that is
missing, empty or cannot be stringified fails the channel with
`notification.ErrInvalidRequest` — no empty string and no placeholder goes onto
the wire.

**An external channel's destination is a dispatch parameter.** This module
reaches no account store and takes the destination from nowhere else: a recipient
is an account identifier to it, not a phone number and not an address. The
destination therefore travels in the dispatch's parameters under
`destinationParam` — the constant `"to"`, declared in `render.go` — as an E.164
number for `sms` and an RFC 5322 address for `email`. A dispatch whose parameters
name no `to`, or whose `to` cannot be stringified, **fails that channel** with
`notification.ErrInvalidRequest`, recorded as a `failed` row with this module's
own reason, and the message never reaches the vendor. `to` is not exempt from
the type's `RecipientVisibleParams` whitelist: a type that declares one and does
not list `to` refuses the dispatch before the channel is reached.

## Sentinels

Every refusal this module makes uses a sentinel of the `pkg/notification` root
package, and callers tell them apart with `errors.Is`:

| Sentinel | When |
|---|---|
| `ErrInvalidRequest` | an unreadable shape, a parameter that cannot be stringified, a payload that cannot be decoded |
| `ErrUnknownType` | a type key that is not in the declared taxonomy |
| `ErrParamsNotAllowed` | a parameter outside the type's whitelist |
| `ErrInvalidConfig` | an inconsistent type declaration, or two modules declaring one key — `New` ends the startup |

A channel whose seam is absent is **not** an error: it records `skipped` and does
not enter the joined error.

A vendor's own failure is not one of the sentinels above. It reaches the joined
error as its own package wrote it — `mail.ErrThrottled`, `mail.ErrRejected` and
`mail.ErrUncertain` from `pkg/mail`, the sms sentinels from `pkg/sms` — and every
one of them lands the channel at `failed`. The retry a `failed` row invites is the
job's, not this module's: one handler call reaches each channel once.

## Configuration

None. The module declares no configuration namespace and reads no input item: the
parameters of a delivery job (priority, timeout, retry budget) are fixed by this
module at enqueue time.

## Routes and permissions

None. The module mounts no route and declares no permission point. The submission
seam is a code seam; who may call it is the host's authorization decision.

## Running it

From the repository root:

```
make build
make test
make lint
```

On this module alone, from inside `pkg`:

```
go build ./notification/delivery/...
go test ./notification/delivery/...
go test -race ./notification/delivery/...
env -C pkg golangci-lint run ./notification/delivery/...
```

`golangci-lint` must be run from inside `pkg`: handed an absolute path from
outside the module it prints zero issues without loading the packages.

## Boundaries

- **This module owns no type and no preference matrix.** A notification type is
  the declaring business module's `notification.TypeSpec`; this module collects
  every declaration in `New` as the reference a dispatch is validated against,
  and reads a recipient's channels through `Preferences.Effective`. It never
  reads the preferences table.
- **This module owns no inbox storage.** The in-app row is written through
  `notification.Inbox`; this module neither reads nor writes `in_app_messages`.
- **This module owns no job execution.** Claiming, leases, backoff and the
  distribution across replicas belong to `jobs.db`; this module only registers a
  handler and enqueues through the submission face.
- **The tenant comes from the context.** Every ledger statement is scoped by the
  roster module's filter. `Dispatcher.Dispatch` refuses a context with no tenant
  and writes nothing.
- **The pipeline is not a replaceable backend.** The submission seam and the
  handler are delivered exclusively; a different delivery strategy is a change to
  this module's code.
