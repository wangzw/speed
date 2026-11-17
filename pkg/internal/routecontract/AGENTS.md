# pkg/internal/routecontract

The test support for the check that a module's declared OpenAPI operations are
the routes it registers. It drives a real registry holding the module under
test to `Init`, through a stand-in entry point that records every `Route` call,
reads the module's own `speedhttp.Spec` fragments back out of the registry, and
reports every difference between the two sets in either direction — a route
registered and not declared, an operation declared and not registered, a method
or a path written wrong.

## Boundaries

- **No module is registered here and no capability is delivered.** A carrier
  case supplies the module under test and the stand-ins for what it requires.
  The two stand-ins the package owns are the recording entry point and a
  logging capability that discards, so that a module's startup diagnostics do
  not reach standard output during a test.
- **It sits here, outside a capability directory, because two capability
  directories use it.** Cases under `pkg/authn`, `pkg/audit`, `pkg/authz`,
  `pkg/notification`, `pkg/objectstore`, `pkg/ops`, `pkg/settings`, `pkg/sms`
  and `pkg/tenant` all call `Check`.
- **It knows nothing about the authentication capability.** The route-mounting
  module's interface is internal to `pkg/authn` and cannot be implemented from
  here, so the cases that need it take the real module in themselves; its
  registrations reach the same recording entry point as any other module's,
  which is why one comparison covers both shapes. `pkg/authn/internal/routefixture`
  holds the stand-ins those cases share.
- **It compares sets, not documents.** The fragment's `components` and its
  operation bodies are not read: what a generated client calls is the pair of a
  method and a path, and that pair is what this check holds to the routes the
  module serves. `pkg/ops/openapi` is where a fragment's whole content is
  merged.
- **It is imported by tests alone.** Every holder reaches it from a `_test.go`
  file.

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
On this package alone, inside the `pkg` module:

```
go -C pkg build ./internal/routecontract/...
go -C pkg vet ./internal/routecontract/...
go -C pkg test ./internal/routecontract/...
```

## Adding a check for a module

A case is the module, the endpoint names the stand-in entry point declares, the
stand-ins for what the module requires, and the configuration document:

```go
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	routecontract.Check(t, routecontract.Case{
		Module:    Module(),
		Endpoints: []string{"api"},
		Stubs:     []core.Module{ /* one stand-in per capability the module requires */ },
		Config:    routecontract.Config(),
	})
}
```

A stand-in for a capability the module only resolves and holds is a struct with
the interface embedded and no value in it — `&struct{ tenant.Catalog }{}` —
handed to `StandIn`. One interface per struct, and no more: embedding two
leaves every method they share ambiguous, and an ambiguous method is not
promoted, so the struct would satisfy neither. A capability the module calls
during `Init` — the entry point, a database, a bus — has to be real.

A module that mounts part of its declared surface per capability it takes up in
`Init` — `authn.admin` and `authn.login` both do — needs the assembly that
carries every one of those capabilities, or it mounts less than it declares.
