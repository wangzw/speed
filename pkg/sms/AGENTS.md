# pkg/sms

The SMS capability: send one templated message through a pluggable carrier, and
manage the templates a message may go out under. The root package is the
interface package — it carries the capability's seams, the template types and
the carrier factory, and registers no module.

The seams it defines:

- **`Sender`** is the sending seam a carrier delivers: `Name`, and `Send` of one
  `Resolved` message. A carrier lays the already-ordered parameters out the way
  its vendor wants them, calls the vendor once and classifies the answer into a
  closed set of sentinels.
- **`Provider`** is the template-management seam a carrier delivers beside
  `Sender`: `Offer`, `Approval` and `Retire`. It is the operations only the
  vendor's own API can perform, and the only place a vendor's numbering is read
  or written.
- **`Registry`** is the template seam a registry module delivers: it holds the
  set of templates in force and decides which one a message goes out under. It
  reaches a vendor only through `Provider`, and a consumer takes both it and a
  `Sender` — resolve a message against the registry, then send the result through
  a carrier.

A carrier module delivers `Sender` and `Provider` as one `Product`, over one
vendor client, and declares both exclusively. A host writes a carrier's module
name into a command; two carriers at once fail in resolution.

`Message` is the caller's view of one message — a template name, a language and
the parameters. `Resolved` is what a carrier is handed: the vendor code the
registry chose, the keyed parameters, and the same values in the declared order.
The package's godoc is the API, the root package's and each subpackage's; read
`Sender`'s, `Message`'s and `Registry`'s comments before adding a carrier or a
consumer.

The subpackages split by role. `aliyun` and `tencent` are carriers: each binds
its own vendor SDK and registers one module. `registry` is the template-registry
module — it holds the templates in force and delivers `Registry`. `admin` is the
management HTTP surface; it delivers no capability, every route being a call on
`Registry`.

## Boundaries

- **The capability is sending and template management, and nothing else.** No
  queue, no worker pool, no budgets, no throttling accounting, no challenge
  state, no code generation or verification, and no retry. Each of those belongs
  to a consumer: the authentication module owns the queue, the budgets and the
  challenge row, and hands this capability one message at a time. A carrier
  answers every `Send` synchronously after one vendor call; a carrier that
  retries is the shape to reject, because the vendor send call is not idempotent
  and a timeout leaves the caller unable to tell whether the message was
  delivered.
- **A template is a first-class declaration, not free text.** Domestic vendors
  accept only a pre-approved sign and template, approve templates per language,
  and a positional vendor requires the parameters in the approved order. One
  template is a `TemplateSpec` — an ordered parameter list and a language-to-code
  map — and `NewDeclaration` freezes a set of them into a `Declaration` after
  checking each is self-consistent. The template registry holds the sets in
  force; a carrier holds no template set and is handed a `Resolved`. `Resolve`
  turns one `Message` into the vendor code and the parameters in the declared
  order. A carrier that renders a free-text body is the shape to reject.
- **The locale is opaque, and an empty one is a caller defect.** This capability
  declares no closed set of languages; it uses the locale only to index a
  template's language-to-code map. An empty or unlisted locale fails `Resolve`
  with `ErrRejected` and no vendor call is made. Normalizing an empty locale to a
  default is the caller's job, not this capability's.
- **Self-consistency is the declaration's; coverage is the consumer's.**
  `NewDeclaration` checks each template has at least one parameter, no duplicate
  parameter name, at least one language and no empty code, and a defect is
  `ErrInvalidConfig`, which ends the startup. Whether the templates in force
  cover what a consumer needs is the consumer's check, against
  `Registry.Declared()`, in its own `New`. This capability declares no consumer
  needs and holds no coverage set.
- **The root package registers no module and imports no vendor SDK.** Each
  carrier's subpackage binds its own vendor SDK, and every module is registered
  by a subpackage. Among this repository's packages the root package imports only
  `pkg/config`, `pkg/core`, `pkg/internal/literal` and `pkg/log` — neither
  `pkg/db` nor any `pkg/authn`. `deps_test.go` holds the root package's non-test
  dependency face to no vendor SDK and no `pkg/authn`, and pins the two SDK
  versions, so the capability stands alone beside `pkg/kv` and `pkg/event`.
- **A carrier registers its secrets before it takes its logger.** `Spec.Secrets`
  returns the credential strings to register as literal redaction matchers, and
  the factory registers them through `pkg/internal/literal` before it hands the
  carrier a `Named` logger and calls `Spec.Open`. The register lives one level up
  at `pkg/internal/literal`, not here, because a package under `pkg/sms` cannot
  import an `internal` package of `pkg/authn` and the login leaves there use the
  same register.

## How a carrier contributes

A subpackage binds its vendor client, fixes its namespace, and registers the
module this package builds from its `Spec`. The exclusive delivery of both seams,
the input-item declaration, the config decode, the secret registration and the
stage behaviour all stay here.

```go
func init() { core.ProcessRegistry.Register(Module()) }

func Module() core.Module {
	prototype := Config{}
	return sms.NewModule(sms.Spec[Config]{
		ModuleName:      ModuleName,   // "sms.aliyun"
		ConfigNamespace: ModuleName,   // where this carrier's items are mounted
		Config:          &prototype,   // the prototype, carrying the defaults
		Items:           items,        // the declared input items
		Stance:          stance,       // how the exclusive claim yields, or what is missing
		Secrets:         secrets,      // the credential strings to redact
		Open:            open,         // builds the Product, and the one place a vendor type appears
	})
}
```

`Open` answers a `Product`, one value that satisfies both `Sender` and `Provider`
over one vendor client: the carrier implements `Name` and `Send` for the sending
seam, and `Name`, `Offer`, `Approval` and `Retire` for the management seam, and
holds a dedupe table from `internal/dedupe`.

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
go -C pkg build ./sms/...
go -C pkg test ./sms/...
go -C pkg test -race ./sms/...
env -C pkg golangci-lint run ./sms/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages. The carrier cases need
no real credentials: a case builds the vendor client against a loopback
`httptest` endpoint and drives `Send` through it, so the request shape and the
classification are held against the real SDK.
