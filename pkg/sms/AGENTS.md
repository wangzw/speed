# pkg/sms

The SMS sending capability: send one message by template through a pluggable
carrier. It maps a caller's template name, parameters and locale to a vendor
template, lays the parameters out the way the vendor wants them, calls the
carrier once and classifies the answer into a closed set of sentinels. It does
not know what the message is for.

The root package carries the seam, the first-class template declaration and the
factory. Concrete carriers are subpackages — `aliyun` and `tencent` — and each
registers one module, binds its own vendor SDK, and declares the sending seam
exclusively.

The API is the package's godoc, the root package's and each subpackage's. Read
`Sender`'s and `Message`'s comments before adding a carrier.

## Boundaries

- **The capability is sending, and only sending.** No queue, no worker pool, no
  budgets, no throttling accounting, no challenge state, no code generation or
  verification, and no retry. Each of those belongs to a consumer: the
  authentication module owns the queue, the budgets and the challenge row, and
  hands this capability one message at a time. A carrier answers every `Send`
  synchronously after one vendor call; a carrier that retries is the shape to
  reject, because the vendor send call is not idempotent and a timeout leaves the
  caller unable to tell whether the message was delivered.
- **A template is a first-class declaration, not free text.** Domestic vendors
  accept only a pre-approved sign and template, approve templates per language,
  and a positional vendor requires the parameters in the approved order. A
  carrier declares its templates as a `map[string]TemplateSpec`, each with an
  ordered parameter list and a language-to-code map; the factory freezes it into
  a `Declaration`, and `Resolve` turns one `Message` into the vendor code and the
  parameters in the declared order. A carrier that renders a free-text body is
  the shape to reject.
- **The locale is opaque, and an empty one is a caller defect.** This capability
  declares no closed set of languages; it uses the locale only to index a
  template's language-to-code map. An empty or unlisted locale fails `Resolve`
  with `ErrRejected` and no vendor call is made. Normalizing an empty locale to a
  default is the caller's job, not this capability's.
- **A carrier guarantees only that its own declaration is self-consistent.**
  `NewDeclaration` checks each template has at least one parameter, no duplicate
  parameter name, at least one language and no empty code, in the carrier's
  `Prepare`; a defect is `ErrInvalidConfig`, which ends the startup. Whether the
  declaration covers what a consumer needs is the consumer's check, against
  `Sender.Declared()`, in its own `New`. This capability declares no consumer
  needs and holds no coverage set.
- **The root package registers no module and imports no vendor SDK.** Each
  carrier's subpackage does both. `deps_test.go` holds the root package's closure
  to no vendor SDK and pins the two SDK versions; it also holds that the root
  package imports nothing of `pkg/authn`, so the capability stands alone beside
  `pkg/kv` and `pkg/event`.
- **A carrier registers its secrets before it takes its logger.** `Spec.Secrets`
  returns the credential strings to register as literal redaction matchers, and
  the factory registers them through `pkg/internal/literal` before it hands the
  carrier a `Named` logger and calls `Spec.Open`. The register lives one level up
  at `pkg/internal/literal`, not here, because a package under `pkg/sms` cannot
  import an `internal` package of `pkg/authn` and the login leaves there use the
  same register.

## How a carrier contributes

A subpackage binds its vendor client, fixes its namespace, and registers the
module this package builds from its `Spec`. The exclusive delivery, the
input-item declaration, the config decode, the secret registration and the stage
behaviour all stay here.

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
		Open:            open,         // the client, and the only place it appears
	})
}
```

The carrier then implements `Name`, `Send` and `Declared`, and holds its
`Declaration` and a dedupe table from `internal/dedupe`.

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
