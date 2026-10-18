# pkg/ops/openapi

The merge function shared by the `ops` module and a build tool. It registers no
module: it has no lifecycle, delivers no capability and declares no resource. It
is the one face of the operations capability that something outside a module
calls — a build's static-inclusion path reaches it to write the merged document
into an artifact without constructing any module.

## API

```go
func Merge(specs []core.Resource[http.Spec], opts Options) ([]byte, error)

type Options struct {
	Title   string
	Version string
}
```

`Merge` combines the fragments it is given into one document and returns its
JSON, or an error. It is a **pure function**: its inputs are the fragment slice
and the two options, and it reads no registry, takes up no capability and does
not touch a lifecycle. The same declarations and the same options produce the
same bytes whether the `ops` module merges them in `Init` or a build merges them
offline.

Sentinels the errors carry, to test against with `errors.Is`:

- `ErrUnreadable` — a fragment that cannot be read as a JSON object. The error
  names the module that declared it.
- `ErrOperationConflict` — the same path and method declared by two fragments.
  The error names both declaring modules and the endpoint each declared it for.
- `ErrComponentConflict` — one component declared under one section and name
  with different content. The error names the module that declared the later one
  and the component.

## The document it produces

Top level: `openapi` (the version the fragments are written against), `info`
(title and version from `Options`), `paths`, `components`, and the extension key
`x-fragments` — a list of each fragment's source, `{module, endpoint}`, in module
order.

**Each fragment path is resolved against its Spec's `Prefix` and `Version`
before it reaches the document.** A fragment declares `/auth/me` relative to the
API root; `Spec.ResolvePath` composes `/api/v1/auth/me`, and that absolute path
is what the document holds, what a conflict is judged on, and what a client
addresses. A Spec that serves at the root (both fields empty) carries absolute
paths already.

Each operation carries the extension key `x-endpoint`, naming the endpoint the
operation is served on: one document describes several listening endpoints, and
a path alone does not say which one answers it. These two keys are the
host-visible shape a gateway or a frontend reads; changing them is a breaking
change.

## Conflict semantics

An operation declared for the same resolved path and method by two fragments is a
conflict, judged **across endpoints as well as within one**: one document holds
an operation once, and a route served on two endpoints is two operations the
paths cannot tell apart. A component declared under one section and name with
different content is a conflict too; the **same content** declared twice merges
into one entry, because several fragments sharing an error schema is ordinary.
Content is compared by value, so key order and whitespace do not matter.

The merge reads declarations, not the routes a module registered: a fragment
that declares a route nobody registered, or a registered route with no fragment,
is not an error.

## Running it

```
go -C pkg build ./ops/openapi/...
go -C pkg test ./ops/openapi/...
env -C pkg golangci-lint run ./ops/openapi/...
```

`go:embed` reads the module's own fragment, `openapi/ops.json`, from the parent
package; it describes the three routes the `ops` module serves.
