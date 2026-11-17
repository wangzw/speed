# pkg/mail

The mail sending capability: send one already-rendered message through a
pluggable carrier. A `Message` carries the addresses, the subject and the one or
two bodies the caller has already rendered; a `Sender` hands that message to one
carrier, which calls its relay once and classifies the answer into a closed set
of sentinels. The capability holds no template and does no rendering — the caller
renders — and it knows nothing about what the message is for.

The root package carries the seam, the message and the factory. A concrete
carrier is a subpackage — `smtp` — and it registers one module, binds its own
carrier library, and declares the sending seam exclusively.

The API is the package's godoc, the root package's and each subpackage's. Read
`Sender`'s, `Message`'s and `Attachment`'s comments before adding a carrier.

## Boundaries

- **The capability is sending, and only sending.** No queue, no worker pool, no
  budget, no throttling accounting, no retry and no delivery tracking. Each of
  those belongs to a consumer: this package takes one already-rendered message at
  a time and hands it to a carrier, and a consumer builds the rest over it. A
  carrier that retries is the shape to reject, because the send is not
  idempotent — a second call delivers and bills a second message — and a timeout
  leaves the caller unable to tell whether the message went out.
- **The content is the caller's, and this package composes none of it.** A
  `Message` carries the body the caller rendered; the capability holds no
  template, does no rendering and localizes nothing. A carrier that renders a
  body is the shape to reject. A consumer that needs a rendered message builds it
  and hands the result here.
- **The sender defaults to the carrier's configured address, and a message may
  name another.** An empty `Message.From` takes the carrier's own configured
  default sender. A message that names a sender overrides it, and the carrier
  accepts that sender only when it is one the carrier was configured with — its
  default and the additional senders it was given — refusing one outside the set
  with `ErrRejected` before the carrier's own call. The set and where it is
  configured are each carrier's.
- **An attachment's `Data` is read to the end during `Send`, so one `Message`
  value can be sent only once.** A carrier reads every `Attachment.Data` reader
  to the end as it assembles the message, and a reader handed in once is spent. A
  dedupe hit does not re-read it: a repeat answered from the dedupe table returns
  the earlier outcome and never touches the reader, so a caller that re-sends with
  a fresh `DedupeID` and the same exhausted readers would carry empty attachments
  and see no error for it. A caller that retries rebuilds its readers.
- **A field the interface names is either delivered faithfully or refused, never
  dropped.** Every field a `Message` carries — the addresses, the subject, the
  bodies and the attachments — is either written into the message the carrier
  sends or refused with `ErrRejected`. A carrier that silently drops a field it
  cannot carry is the shape to reject: the caller is told a message went out that
  did not hold what it wrote. `Message.Validate` catches the shape the package can
  judge on its own, and each carrier catches the rest its own relay refuses; both
  answer `ErrRejected`. This is about a field being dropped, not about an
  attachment's Name surviving byte for byte: a carrier's library sanitizes a file
  name it writes, and what it makes of one is the library's, not this package's.
- **The root package registers no module and imports no third-party library.**
  Each carrier's subpackage does both: it binds its own carrier library and
  registers the module the factory builds. `deps_test.go` holds the root
  package's closure to configuration, the registry, the logger and the literal
  redaction rules; it holds each carrier's library to its own leaf package and
  pins that library to the one version the table states, so what a host compiles
  by importing this package carries no carrier but the one it deploys on.

## Carrier selection and the fallback

The capability compiles several carriers and runs exactly one. `mail.carrier`
chooses it at run time (ADR-carrier-selection):

- **A carrier name** — the `CarrierName` the carrier declares, which is also
  what its `Sender.Name` answers. The named carrier runs; every other compiled
  carrier stands itself down with a reason. The named carrier has to be
  runnable: a configuration gap there is an error.
- **`none`** — the capability is off. No carrier runs, the log fallback
  included, so the sending seam has no provider and every module that requires
  it stands itself down (the email verification sender among them).
- **An empty value (the key absent)** — automatic selection. A carrier whose
  configuration is complete runs. The log carrier states `StateAuto`: core's
  exclusive resolution leaves it running when no other carrier was explicitly
  enabled and stands it down when one was. That is the fallback.
- **A name no compiled carrier answers to** — an `ErrInvalidConfig` naming the
  value, the capability and the compiled carrier names. A typo must not look
  like `none`.

What a carrier's own configuration means:

- **Unconfigured**: every item that identifies the carrier is empty. The
  carrier states `StateDisabled` with a reason.
- **Half-configured**: some identity items are present and others are missing.
  `Prepare` returns `ErrInvalidConfig` naming the missing items.
- **Selected but not compiled in**: the selection check above; it is an error,
  not a stand-down.
- **Selected and complete**: the carrier runs. Two carriers complete with no
  selection is an exclusive violation naming both modules.

The log carrier (`pkg/mail/log`) is compiled into every build, including a
production one, and declares the `mail.carrier` item because it is the carrier
that is always present. It writes each message to the process log and warns at
startup that the body and any verification code reach the log. When it runs as
the fallback the warning is the only sign that no real channel was configured.

## How a carrier contributes

A subpackage binds its carrier library, fixes its namespace, and registers the
module this package builds from its `Spec`. The exclusive delivery, the
input-item declaration, the config decode, the secret registration and the stage
behaviour all stay here.

```go
func init() { core.ProcessRegistry.Register(Module()) }

func Module() core.Module {
	prototype := Config{}
	return mail.NewModule(mail.Spec[Config]{
		ModuleName:      ModuleName,   // "mail.smtp"
		CarrierName:     "smtp",       // the value mail.carrier names this carrier by
		ConfigNamespace: ModuleName,   // where this carrier's items are mounted
		Config:          &prototype,   // the prototype, carrying the defaults
		Items:           items,        // the declared input items
		Stance:          stance,       // unconfigured, half-configured or ready
		Secrets:         secrets,      // the credential strings to redact
		Open:            open,         // the carrier, and the only place it appears
	})
}
```

The carrier then implements `Name` and `Send`, holds a `Table` from
`pkg/internal/dedupe`, and delivers the sending seam. `Open` returns the
`Sender`; `Secrets` returns the credentials the factory registers as literal
redaction rules before a logger is bound; `Stance` judges the carrier's own
configuration in the three states above, so an unconfigured carrier stands down
and a half-configured one fails the startup ahead of any message. `Fallback`
and `DeclaresSelection` belong to the log carrier alone; every other carrier
leaves them false.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

CI runs the segments of `make check` as parallel jobs.
On this capability alone, inside the `pkg` module:

```
go -C pkg build ./mail/...
go -C pkg test ./mail/...
go -C pkg test -race ./mail/...
env -C pkg golangci-lint run ./mail/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages, so the run passes
without having judged anything. The carrier cases need no live relay: a case
drives the carrier's library against a loopback stand-in for that carrier's own
protocol, so the message shape and the classification are held against the real
library.

## Changing it

Every fix to this capability ships with a case that fails before the fix and
passes after, and the review names the line that made it fail before.
