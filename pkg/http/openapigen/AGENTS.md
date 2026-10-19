# pkg/http/openapigen

The importable entry point to the fragment generator, for a module the Go
internal rule does not admit to `pkg/internal/openapigen` — a host module under
`starters/`, which declares its own API but lives outside the `pkg` tree.

It holds one function and no logic:

```go
func Generate(title, version string, operations http.Operations) ([]byte, error)
```

`Generate` forwards to `pkg/internal/openapigen`, so a host's generation test
runs exactly the code every capability module's test runs and the generator
keeps one implementation.

## Dependency face

This package is a named subpackage in `pkg/http`'s dependency budget: it
reaches the generator's build-time libraries (`swaggest/openapi-go`,
`swaggest/jsonschema-go`) and nothing else. Nothing imports it from a module's
non-test code, so no host binary links it or those libraries; the budget test
in `pkg/http/deps_test.go` names it so the widening is deliberate rather than
something a change slips in.

## Running it

```
go -C pkg build ./http/openapigen/...
go -C pkg test ./http/...
env -C pkg golangci-lint run ./http/openapigen/...
```
