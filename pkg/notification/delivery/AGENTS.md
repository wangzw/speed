# pkg/notification/delivery

The notification capability's outbound delivery pipeline. One delivery intent
goes in, is validated, and is enqueued as one durable job; when the job is
consumed the intent is validated again, the recipient's effective channels are
resolved, and every channel is rendered, sent through its seam and recorded as
one ledger row.

The descriptor declares the conditional features `notification.channel.email` and
`notification.channel.sms`. `Init` posts each from the same seam judgement the
send path reads: email on `mail.Sender`, sms only when both `sms.Registry` and
`sms.Sender` are present. A host-supplied instance posts neither.

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
| `(*kv.KV)(nil)` | the delivery budgets are counted on its counters. |

The optional dependencies are `(*settings.Store)(nil)`, `(*log.Logger)(nil)`,
`(*mail.Sender)(nil)`, `(*sms.Registry)(nil)` and `(*sms.Sender)(nil)`. No absence
fails the startup: a channel is available exactly when the seams it sends through
are present — email on the mail sender, sms only when **both** its seams are
there — and a missing seam records a `skipped` row rather than a failure. A
missing settings store leaves each budget at its declared default. The logging
capability is taken up in `New` and named after the module; without it the module
keeps the process default logger, as it did before the capability was declared.
The key-value counters are required and not optional: a budget that cannot count
would let every dispatch through, which is the direction the requirement forbids.

The descriptor declares no route and no bus. It names no HTTP entry point — this
module mounts no route, so declaring one would only carry `pkg/http` into a host's
closure — and no event bus: the in-app row is announced by the module that wrote
it, so this module publishes nothing on a bus and has none to reach. Its remaining
resources are the ledger migration, the message catalog, the two channel features
and the delivery-budget settings schema.

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

**`Dispatcher.Dispatch` validates, charges the delivery budgets and enqueues.**
It runs the shape checks (a non-empty `TypeKey`, a recipient whose class is
`user`, a non-empty `Recipient.UserID`, a non-empty `Locale`), then the taxonomy
checks (the type is declared, and the parameters do not exceed the type's
`RecipientVisibleParams` whitelist), resolves the recipient's effective channels
to decide whether an external send will happen, charges the delivery budgets for
that, and enqueues one job. It renders no text and writes no ledger. The tenant
comes from the context; a context with none is refused with the tenant
capability's own error. A spent budget is a `*authn.ThrottledError` carrying
`RetryAfter`, and nothing is enqueued.

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

## Delivery budgets

An external delivery is bounded by three fixed-window budgets, charged in
`Dispatch` **before** the job is queued:

| Setting (under `notification.delivery`) | Default | Domain | Bounds |
|---|---|---|---|
| `per-recipient-hourly` | 20 | 1–1000 | external messages to one recipient in an hour |
| `per-recipient-daily` | 100 | 1–10000 | external messages to one recipient in a day |
| `global-hourly` | 10000 | 1–10000000 | external messages across the deployment in an hour |

The counters live in `kv.KV`, one key per window bucket, and the key's expiry is
the bucket width. The per-recipient keys are a digest of the tenant and the
recipient identifier; the deployment key carries no tenant. The limits are read
from settings on every dispatch, so an operator's change reaches the next one
without a restart; a failed or out-of-domain read falls back to the declared
default and writes one `Error` line.

The budget is charged **after** the recipient's effective channels are resolved,
and only for the external channels this assembly can actually send on. A
dispatch whose effective set is `in_app` alone, whose external seam is absent, or
whose recipient turned the external channels off spends nothing: it produces no
external message. One delivery spends one unit per external channel it will
attempt. The increment happens before the comparison, so a refused dispatch
still spends. A window that is over answers `*authn.ThrottledError` with
`RetryAfter` set to the rest of the bucket (at least one second) and enqueues
nothing; windows are checked per-recipient hourly, per-recipient daily, then
global, and a `Dispatch` that fails the first check does not spend the later
ones. A counter that cannot be written answers an error carrying
`authn.ErrUnavailable`, and nothing is enqueued: a budget that cannot count does
not pass.

The budget is charged per `Dispatch` call, not per distinct logical event: the
queue's idempotency key settles convergence only at enqueue time, after the
charge. A caller that can submit can vary the occurrence to create distinct jobs
anyway, so the budget is the bound and the convergence is not.

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
it, and its columns are an operations view of what each channel's send ended
with, not a contract face. One row is one channel's terminal state: `succeeded`,
`failed` or `skipped`. **A `succeeded` row means the carrier accepted the
message — a receipt, not a delivery.** A carrier whose receipts are asynchronous
answers a send with the vendor's message identifier alone; whether the recipient
received it arrives through the vendor's own channel, which this module does not
follow. For `in_app`, `succeeded` means the inbox row was written. The unique
index `(tenant_id, idempotency_key)` is what makes the ledger idempotent.

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

A spent delivery budget is not one of those sentinels: it is the platform's
retryable rejection, `*authn.ThrottledError`, which reads as `authn.ErrThrottled`
and carries `RetryAfter`. A counter that could not be written answers an error
that reads as `authn.ErrUnavailable`. Both refusals enqueue nothing.

A channel whose seam is absent is **not** an error: it records `skipped` and does
not enter the joined error.

A vendor's own failure is not one of the sentinels above. It reaches the joined
error as its own package wrote it — `mail.ErrThrottled`, `mail.ErrRejected` and
`mail.ErrUncertain` from `pkg/mail`, the sms sentinels from `pkg/sms` — and every
one of them lands the channel at `failed`. The retry a `failed` row invites is the
job's, not this module's: one handler call reaches each channel once.

## External send logs

Every call that leaves the process — an email or sms the carrier accepted, and
one it did not — writes exactly one line through **the module's own logger**. The
pipeline takes the optional `log.Logger` up in `New`, names it after this module
and holds it for the life of the product. The handler's context is not consulted:
the delivery job runs on a context `jobs.db` created, which carries no injected
logger, so `log.FromContext` would land these records on the process default
chain and the configured file or JSON destination would never see them. Reading
from the module's logger is what makes a report of a message that never arrived
matchable against what the carrier answered and how long the call took.

| Field | Meaning |
|---|---|
| `channel` | `email` or `sms` |
| `type_key` | the declared notification type |
| `outcome` | `sent`, `throttled`, `rejected`, `unroutable`, `uncertain` or `failed` |
| `provider_id` | the carrier's own identifier, on a success |
| `duration_ms` | how long the carrier call took |
| `cause` | the carrier's own error text, on a failure |

The outcome is the log line's classification, not the ledger's: every carrier
failure lands one `failed` row. A success is written at Info. A failure is written
at Warn when the carrier's error is one of its sentinels — `ErrThrottled`,
`ErrRejected`, `ErrUnroutable` or `ErrUncertain` — and at Error when it is outside
them, which is a defect of the carrier. `ErrUnroutable` keeps its own name: the
message has no defect, but this deployment cannot carry it to that destination.
The line never carries the recipient, the message text or a parameter value. A
channel this module judges before the carrier is reached — a missing destination,
a template that will not resolve — writes no such line: no request left the
process.

`TestTheExternalSendLogsToTheModuleLogger` holds the destination: a context
logger is present and stays empty while a refused send, a send outside the
sentinels and an unroutable one each write their line through a stand-in logging
capability, carrying the module attribute.
`TestTheEmailSendWritesOneLineWithoutSecrets` and
`TestTheSMSSendWritesOneLineWithoutSecrets` hold one line per outcome and the
absence of the recipient, the body and the parameter values.

## Configuration

None. The module declares no configuration namespace and reads no input item: the
parameters of a delivery job (priority, timeout, retry budget) are fixed by this
module at enqueue time. The only runtime parameters are the three delivery-budget
settings, under the module's own `notification.delivery` namespace.

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

- **Every external delivery is bounded before it is queued.** `Dispatch` charges
  the per-recipient and deployment budgets on the key-value counters and refuses
  a spent one with the platform's throttled error and a retry time. A dispatch
  that resolves to no external channel in this assembly spends nothing.

- **Every external send is logged once, and the line is safe to keep.** A
  carrier call writes one line with its outcome, duration and the carrier's
  identifier, never the recipient, the body or a parameter value (see
  [External send logs](#external-send-logs)).
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
