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
		ConfigNamespace: ModuleName,   // where this carrier's items are mounted
		Config:          &prototype,   // the prototype, carrying the defaults
		Items:           items,        // the declared input items
		Stance:          stance,       // what this carrier cannot start without
		Secrets:         secrets,      // the credential strings to redact
		Open:            open,         // the carrier, and the only place it appears
	})
}
```

The carrier then implements `Name` and `Send`, holds a `Table` from
`pkg/internal/dedupe`, and delivers the sending seam. `Open` returns the
`Sender`; `Secrets` returns the credentials the factory registers as literal
redaction rules before a logger is bound; `Stance` states what the carrier cannot
start without, so an unsound section fails the startup ahead of any message.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

`make check` is what CI runs. On this capability alone, inside the `pkg` module:

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
