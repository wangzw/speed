# pkg/feature

The feature capability directory: publishing what this process serves, so a
frontend can gate the surfaces it already carries code for. The declaration
surface is the root package a module imports to declare a feature or post a
conditional one; the publisher is the `board` module.

- `pkg/feature` — the root package: `Spec`, `Board`, the five sentinels and
  `ValidName`. It registers no module and imports the standard library alone, so
  a module that declares a feature carries no third-party dependency because of
  it.
- `pkg/feature/board` — the module `feature.board`, which validates every
  declaration, collects the conditional ones posted during Init, and answers
  `GET /api/v1/features` with the frozen set.

## Boundaries

- The root package's non-test files import the standard library only.
  `deps_test.go` holds that, and the reason is the import graph: every declaring
  module imports this package, so anything reached from here lands in each of
  their closures.
- The publisher sits above `pkg/http` and the authentication and authorization
  root packages. It imports those roots and no implementation subpackage of
  either, and no module imports the publisher: a host puts `feature.board` on a
  command tree by name.
- `pkg/core` does not import this directory. A `Spec` is an ordinary resource
  that `core` stores unread; the board is the only reader.

## Declaring a feature

A module puts a `feature.Spec` in its descriptor's `Resources`. A static feature
must hold whenever the declaring module is in force and the process has a router;
a feature whose presence depends on configuration or on an optional seam is
declared `Conditional: true` and posted in `Init`, in the same judgement that
decides whether the surface is mounted:

```go
if b, err := core.Resolve[feature.Board](reg); err == nil {
    b.Post(ModuleName, "notification.channel.email")
}
```

`Board` is not listed in `Requires`: a process without the board (a worker, for
example) skips the post silently. `Post` is legal only between the board's
`Migrate` and its `Start`, which contains the whole `Init` stage; outside it it
panics.

A name has at least two dot-separated segments, each beginning with a lower-case
letter; its first segment equals the first segment of the declaring module's
name; and no name is declared twice in the available set.

## Running the checks

From the module directory:

```
go -C pkg test ./feature/...
go -C pkg vet ./feature/...
golangci-lint run ./feature/...
```

`golangci-lint` has to run with `pkg` as the working directory; run from the
workspace root with a path argument it reports a typechecking error and exits 0.
