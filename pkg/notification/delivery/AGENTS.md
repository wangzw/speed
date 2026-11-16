# pkg/notification/delivery

The notification capability's outbound delivery pipeline. One delivery intent
goes in, is validated, and is enqueued as one durable job; when the job is
consumed the intent is validated again, a carried destination is held to the
recipient once more, the recipient's effective channels are resolved, and each
channel is rendered, sent through its seam and recorded as one row in the send
ledger.

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
| `(*tenant.Catalog)(nil)` | it delivers the statement filter that scopes, fills and checks the tenant column of every send-ledger statement. |
| `(*notification.Preferences)(nil)` | `Effective` answers a recipient's channels. |
| `(*notification.Inbox)(nil)` | the in-app channel writes through it. |
| `(*i18n.Localizer)(nil)` | the message text is rendered through it. |
| `(*i18n.Resolver)(nil)` | the email channel reads the recipient's stored time zone through it, so a time parameter renders at the recipient's local instant; the in-app channel writes a marker instead. |
| `(*authn.Accounts)(nil)` | the recipient's identifiers are read through it, and the account a destination belongs to is looked up through it. |
| `(*kv.KV)(nil)` | the fixed-window counters the email budgets are charged on. |
| `(*budget.Store)(nil)` | the shared SMS attempt ledger: every sms attempt is written to it, and every sms budget is a count over its rows. |

The optional dependencies are `(*settings.Store)(nil)`,
`(*observability.MeterProvider)(nil)`, `(*log.Logger)(nil)`,
`(*mail.Sender)(nil)`, `(*sms.Registry)(nil)`, `(*sms.Sender)(nil)` and
`(*authn.TenantNameResolver)(nil)`. No
absence fails the startup: a channel is available exactly when the seams it
sends through are present — email on the mail sender, sms only when **both** its
seams are there — and a missing seam records a `skipped` row rather than a
failure. A missing settings store leaves each budget at its declared default. A
missing metric capability only removes the utilization gauge's report; it
changes what is observed, never what is refused. The logging capability is taken
up in `New` and named after the module; without it the module keeps the process
default logger, as it did before the capability was declared. The key-value
capability and the shared SMS ledger are required and not optional: a delivery
budget that cannot count would let the message through, which is the direction
the requirement forbids — the key-value counters for email, the ledger for sms.
The account store is required for the same reason: a destination that cannot be
held against the recipient's own identifiers would be let through. The resolver
is required because the email handler runs on a context the queue created, which
names no time zone: without it an email's `{{name, time, layout}}` parameter
could only render in UTC rather than at the recipient's local instant. The
in-app channel does not read it, so a zone read failure blocks email alone.

The database's root key is required as well, although only `db.Database` names
it. Every external delivery folds the recipient into a keyed digest through
`Database.BlindIndex` — the ledger's `destination_mac` and the email counter
keys — so a database assembled without the key has no digest to compute. `Start`
fails with `authn.ErrNoEncryptionKey` rather than writing an unkeyed digest
anyone could reverse, the same refusal the authentication SMS module makes.

The descriptor declares no route and no bus. It names no HTTP entry point — this
module mounts no route, so declaring one would only carry `pkg/http` into a host's
closure — and no event bus: the in-app row is announced by the module that wrote
it, so this module publishes nothing on a bus and has none to reach. Its remaining
resources are the send ledger's migration, the message catalog, the two channel
features and the delivery-budget settings schema.

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
reads. A dispatch whose parameters name no `to`, or whose `to` cannot be
stringified, fails that channel with `notification.ErrInvalidRequest` and this
module's own reason, and the carrier is never reached. The destination is not
free-form: `Dispatch` holds a `to` the dispatch carries against the recipient's
own identifiers through `authn.Accounts` before the job is queued (see
[Destination ownership](#destination-ownership)).

## The two halves

**`Dispatcher.Dispatch` validates, holds a destination to the recipient, charges
the delivery budgets and enqueues.** It runs the shape checks (a non-empty
`TypeKey`, a recipient whose class is `user`, a non-empty `Recipient.UserID`, a
non-empty `Locale`), then the taxonomy checks (the type is declared, and the
parameters do not exceed the type's `RecipientVisibleParams` whitelist), then the
destination ownership rule when the parameters carry a `to`, resolves the
recipient's effective channels to decide which external attempts will happen,
charges the email and sms budgets for those, and enqueues one job. It renders no text and
writes no per-channel ledger row. The tenant comes from the context; a context
with none is refused with the tenant capability's own error. A spent budget is a
`*authn.ThrottledError` carrying `RetryAfter`, and nothing is enqueued.

**The handler consumes the job.** `jobs.db` claims it, enters its tenant and
hands it over, so the handler's context already carries the tenant. The handler
decodes the payload, runs the same validation again — a worker can meet a
payload the submitter never wrote, or a row that has since been changed — holds
a carried destination against the recipient's identifiers once more, resolves
the recipient's effective channels through `Preferences.Effective`, and walks
them. The job carries the identifier of the charged sms attempt, and the sms
channel refunds that row when the message certainly did not leave the process. A
channel that fails does not starve the others and is not hidden by their
successes: every channel is walked and their errors are joined into what the
handler returns, which is what makes the job retry.

**The in-app row is written, not announced.** The handler writes it through
`notification.Inbox`, which is the module that publishes `notification.inbox.created`
once that row has committed. This module publishes no event of its own: one row
announced twice would push twice to a connected client.

## Delivery budgets

An external delivery is bounded, checked in `Dispatch` **before** the job is
queued. Each channel is counted on the store that fits it: an email delivery
charges fixed-window counters on `kv.KV`, and an sms attempt is counted over the
shared SMS attempt ledger, where its row is written in the same transaction that
takes the counts, under the cross-channel advisory lock the authentication path
also takes.

| Setting (under `notification.delivery`) | Default | Domain | Bounds |
|---|---|---|---|
| `per-recipient-hourly` | 20 | 1–1000 | email or sms messages to one recipient in an hour, counted per channel |
| `per-recipient-daily` | 100 | 1–10000 | email or sms messages to one recipient in a day, counted per channel |
| `global-hourly` | 10000 | 1–10000000 | email messages across the deployment in an hour |
| `tenant-distinct-hourly` | 20 | 1–10000 | different recipients one tenant is sent an sms message in an hour |
| `attempt-hourly` | 300 | 1–100000 | sms attempts one tenant makes in an hour, accepted or not |

The cross-channel ceiling is `sms.ceiling-hourly` (default 5000, domain 1 to
1000000), declared by the `pkg/sms/budget` module because it spans both sending
channels; this module reads it by its full key and declares no copy of its own.

A dispatch whose effective channels are in_app alone, or whose external seam
this assembly did not deliver, spends nothing: it produces no external message.
The per-recipient and deployment windows are read from settings on every
dispatch, so an operator's change reaches the next one without a restart; a
failed or out-of-domain read falls back to the declared default and writes one
`Error` line. Each channel counts its own deliveries, so the shared limit values
do not merge the two counters; one email delivery spends one unit, and one sms
attempt spends its own unit of the ledger.

The email checks run in order — per recipient hourly, per recipient daily, then
deployment hourly — on keys whose bucket width is their expiry, and the
increment happens before the comparison so a refused dispatch still spends. The
sms checks run in order — the shared ceiling, per recipient hourly, per
recipient daily, per tenant distinct destination hourly, then per tenant attempt
hourly — and a dispatch that fails one does not read the later ones. The
distinct-destination budget bounds the different recipients a tenant reaches,
not the messages it sends: a destination already inside the window adds no new
one, so a repeat is admitted and the per-recipient budgets still bound it. An
over window answers `*authn.ThrottledError` and enqueues nothing: the email
windows set `RetryAfter` to the rest of the fixed bucket, and the sms windows to
the moment the oldest counted row leaves the sliding window, each at least one
second. A counter or ledger that cannot be read or written answers an error
carrying `authn.ErrUnavailable`, and nothing is enqueued: a budget that cannot
count does not pass.

A refund frees a send budget and keeps an attempt budget. A skipped channel, a
local refusal before the carrier is reached, and a definitive carrier failure — a
throttle, a rejection or an unroutable destination — mark the charged attempt
refunded, so it leaves the per-recipient and tenant-distinct budgets and the
cross-channel ceiling while it stays in the tenant attempt budget. An uncertain
outcome is not refunded: the message may have gone out, so it keeps counting.

The attempt budget is deliberately asymmetric with the authentication path's.
Authentication counts only determinate failures, because there the count names the
account guessing codes and a success should not be charged to the attacker.
Notification counts every attempt, because here the caller is the producer: a
producer caught in a retry loop is what the bound exists for, and such a storm's
attempts may each be accepted by the carrier or each fail definitely and refund
their send budget. Counting only determinate failures would let the storm vanish
from the attempt window.

The budget is charged per `Dispatch` call, not per distinct logical event: the
queue's idempotency key settles convergence only at enqueue time, after the
charge. A caller that can submit can vary the occurrence to create distinct jobs
anyway, so the budget is the bound and the convergence is not.

## Destination ownership

A dispatch that carries an external destination must carry the recipient's own.
`Dispatch` reads the recipient's identifiers through `authn.Accounts.Identifiers`
and, for each kind an external channel can address — `email` and `phone` — asks
`authn.Accounts.Lookup` which account the destination belongs to. The lookup
normalises the value the way the account store wrote it, so the rule reuses the
one normalisation rather than adding a second. A username is a login handle and
is not a destination.

A destination bound to another account, bound to no account, or named for a
recipient that holds no address identifier is refused with
`notification.ErrInvalidRequest`, and nothing is enqueued. A recipient that names
no live account has no identifiers to own a destination and is refused the same
way. A failed account read is an error and not a pass.

The consuming handler runs the rule once more before it resolves the effective
channels: the submission seam is the trust boundary for a producer choosing an
address, and the second read is defence in depth, so a payload written past the
seam or changed afterwards cannot reach an arbitrary mailbox or number. A failed
account read fails closed there too.

**What the rule does not cover.** A dispatch with no `to` is not checked: the
in-app channel needs no destination, and an email or sms dispatch with no `to`
fails that channel at send time. A `to` that cannot be stringified is likewise
left to the channel: there is no address to compare. The recipient classes are the
closed set `user` and `external`: `user` is an account and `external` is refused
by the shape check, so no account-less class reaches the ownership rule; a new
account-less class would have to decide its own rule.

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

## The send ledger and at-least-once delivery

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

The channels render differently. `in_app` renders its title and body in the
recipient's language and writes every `{{name, time, layout}}` segment as a
marker naming the parameter — U+E000, `time:`, the name, U+E001 — not as a
formatted instant. The client resolves it from the message's `Params` in the
viewer's display zone, so changing the zone or language re-renders a message
already delivered. A string parameter has the two delimiters removed, so a value
carrying a private-use character cannot be mistaken for a marker. `link` renders
as ordinary text: it is a URL the client opens, not display text.

`email` renders on the server, in the recipient's language and stored time zone.
The handler reads the zone once per job through `i18n.Resolver.TimeZoneFor`,
before anything is rendered. "No zone stored" renders in UTC; a read failure
records the channel `failed`, writes one Error line naming the type and the
cause and no recipient, and returns the error so the job retries — a failed read
must not freeze a wrong instant into a mail the recipient cannot re-render. The
text is fixed at send time; a later zone change does not rewrite it.

`sms` renders only the template identifier in the recipient's language. Its
parameters are stringified: a string as it is, an integer or a boolean in its
text, a time in UTC RFC 3339 at whole-second precision — Go's `Format` omits the
sub-second part, so `09:30:00.123Z` becomes `09:30:00Z`. A parameter that is
missing, empty or cannot be stringified fails the channel with
`notification.ErrInvalidRequest`.

**A producer passes a `time.Time`; the pipeline wraps it.** `renderValue` maps a
`time.Time` parameter to `i18n.Time`, the engine's typed value: the marked
in-app render needs only the parameter name, while the email render needs the
type to expand the segment. The queue codec preserves that type across the job
payload. `deliveryPayload`'s own JSON codec replaces every `time.Time` at any
depth in `map[string]any` and `[]any` with an object under the reserved key
`$speed.time`, and restores it when the job is read. A parameter map carrying
that key at any level — the top level included — is refused with
`notification.ErrInvalidRequest` where the dispatch is submitted; a decoded
payload whose object under that key carries more than one member stays a map and
is refused again, while a single-member object is the codec's own restore and
comes back as a `time.Time`. The same sentinel refuses a shape the codec cannot
walk: a `*time.Time`, a `[]time.Time`, a `map[string]time.Time`, or any other
typed container. A bare `time.Time` at any depth is admitted.

A part is keyed
`<type_key>.<channel>.<part>` — `title` and `body` for `in_app`; `subject`,
`body_text` and an optional `body_html` for `email`; and a template identifier for
`sms` — and belongs to the business module that **declares** the type, not to this
module. This module owns only its own text: the reasons it writes into the ledger
for the channels it judged itself, under `locales/`, one file per locale with the
same key set. The parameter whitelist has already been enforced by the time text
is rendered, so only the visible parameters enter the rendering context.

**A parameter named `tenant` is resolved to the tenant's name, never left as
the internal identifier.** The submitting module knows the identifier; every
text a recipient can read — the in-app title, body and payload parameters, the
email subject and both bodies, and the sms template variables — names the name
instead. The handler resolves the one parameter before any channel renders or
copies it, through the optional `authn.TenantNameResolver`; the idempotency key
is still derived from the dispatch as submitted. An assembly with no resolver,
or one that fails, resolves the parameter to the empty name rather than falling
back to the identifier, which is internal.

A part the engine falls back to the key for is written out like any other: the
key is text, and a missing catalog entry is not this module's to refuse. The two
parts a client would not survive as a key are the exception, and they are written
only when the catalog carries them — `link` for `in_app`, and `body_html` for
`email`, which would otherwise put a key string into markup a mail client
renders.

Sms parameters are stringified before the vendor is reached: a string as it is,
an integer or a boolean in its text, a time in UTC RFC 3339 at second precision.
A parameter that is missing, empty or cannot be stringified fails the channel
with `notification.ErrInvalidRequest` — no empty string and no placeholder goes
onto the wire.

**An external channel's destination is a dispatch parameter.** A recipient is an
account identifier, not a phone number and not an address, so the destination
travels in the dispatch's parameters under `destinationParam` — the constant
`"to"`, declared in `render.go` — as an E.164 number for `sms` and an RFC 5322
address for `email`. The module does not derive it from the account; the account
store is read only to hold it against the recipient's identifiers (see
[Destination ownership](#destination-ownership)). A dispatch whose parameters
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
| `ErrInvalidRequest` | an unreadable shape, a destination the recipient does not own, a parameter that cannot be stringified, a parameter shape the queue codec cannot carry, a reserved `$speed.time` key, a payload that cannot be decoded |
| `ErrUnknownType` | a type key that is not in the declared taxonomy |
| `ErrParamsNotAllowed` | a parameter outside the type's whitelist |
| `ErrInvalidConfig` | an inconsistent type declaration, or two modules declaring one key — `New` ends the startup |

A spent delivery budget is not one of those sentinels: it is the platform's
retryable rejection, `*authn.ThrottledError`, which reads as `authn.ErrThrottled`
and carries `RetryAfter`. A ledger that cannot be read or written answers an
error that reads as `authn.ErrUnavailable`. Both refusals enqueue nothing.

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
module at enqueue time. The only runtime parameters are the delivery-budget
settings under the module's own `notification.delivery` namespace, and the shared
cross-channel ceiling `sms.ceiling-hourly` that the `pkg/sms/budget` module
declares and this module reads.

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
  an email delivery's per-recipient hourly and daily windows and the
  deployment-wide hourly window on the key-value counters, and an sms attempt's
  shared cross-channel ceiling and per-recipient, per-tenant
  distinct-destination and per-tenant attempt budgets over the shared SMS
  ledger, refusing a spent one with the platform's throttled error and a retry
  time. A dispatch that resolves to no external attempt in this assembly spends
  nothing; an in-app send spends no budget.

- **A dispatch's destination must be the recipient's own.** `Dispatch` holds a
  carried `to` against the recipient's identifiers through `authn.Accounts` and
  refuses one bound to another account, to no account, or named for a recipient
  with no address identifier; nothing is enqueued. The rule reads the account
  store and does not write it (see [Destination ownership](#destination-ownership)).

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
