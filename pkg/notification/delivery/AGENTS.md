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

The optional dependencies are `(*sms.Registry)(nil)` and `(*sms.Sender)(nil)`.
Neither absence fails the startup, and sms is available only when **both** seams
are present; a missing seam records a `skipped` row rather than a failure.

The descriptor declares nothing else. It names no HTTP entry point — this module
mounts no route, so declaring one would only carry `pkg/http` into a host's
closure — and no event bus: the in-app row is announced by the module that wrote
it, so this module publishes nothing on a bus and has none to reach.

## Email is not available in this release

The mail capability is not in the tree yet, so this module references no mail
sender seam at all: the `email` channel is always unavailable, exactly as an
absent seam would be, and a delivery to it records a `skipped` row. The rest of
the pipeline treats it like any other channel whose seam is missing.

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
`<type_key>.<channel>.<part>` — `title` and `body` for `in_app`, `subject` and
`body_text` for `email`, and a template identifier for `sms` — and belongs to the
business module that **declares** the type, not to this module. This module owns
only its own text: the reasons it writes into the ledger for the channels it
judged itself, under `locales/`, one file per locale with the same key set. The
parameter whitelist has already been enforced by the time text is rendered, so
only the visible parameters enter the rendering context.

Sms parameters are stringified before the vendor is reached: a string as it is,
an integer or a boolean in its text, a time in RFC 3339. A parameter that is
missing, empty or cannot be stringified fails the channel with
`notification.ErrInvalidRequest` — no empty string and no placeholder goes onto
the wire.

**The sms destination is a dispatch parameter.** The design fixes no source for
`sms.Message.To`, and this module reaches no account store: a recipient is an
account identifier, not a phone number. The number therefore travels in the
dispatch's parameters under `destinationParam` — the constant `"to"`, declared
in `render.go` — as an E.164 number. A dispatch whose parameters name no `to`, or
whose `to` cannot be stringified, **fails that channel** with
`notification.ErrInvalidRequest`, recorded as a `failed` row with this module's
own reason, and the message never reaches the vendor. `to` is not exempt from
the type's `RecipientVisibleParams` whitelist: a type that declares one and does
not list `to` refuses the dispatch before the channel is reached.

## Sentinels

Every refusal uses a sentinel of the `pkg/notification` root package, and callers
tell them apart with `errors.Is`:

| Sentinel | When |
|---|---|
| `ErrInvalidRequest` | an unreadable shape, a parameter that cannot be stringified, a payload that cannot be decoded |
| `ErrUnknownType` | a type key that is not in the declared taxonomy |
| `ErrParamsNotAllowed` | a parameter outside the type's whitelist |
| `ErrInvalidConfig` | an inconsistent type declaration, or two modules declaring one key — `New` ends the startup |

A channel whose seam is absent is **not** an error: it records `skipped` and does
not enter the joined error.

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
