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
	Title       string
	Version     string
	Description string
	Servers     []string
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
(title, version and description from `Options`), `servers` (the origins from
`Options`), `paths`, `components`, `tags`, and the extension key `x-fragments` —
a list of each fragment's source, `{module, endpoint}`, in module order. A
member whose `Options` value is empty is left out.

**Each fragment path is resolved against its Spec's `Prefix`, `Version` and
`Scope` before it reaches the document.** A fragment declares `/auth/me`
relative to the API root; `Spec.ResolvePath` composes `/api/v1/auth/me`, and a
management surface's fragment composes `/api/v1/tenants/...` or
`/api/v1/system/...`. That absolute path is what the document holds, what a
conflict is judged on, and what a client addresses. A Spec that serves at the
root (every field empty) carries absolute paths already.

Each operation gains three members the merge owns:

- `x-endpoint`, naming the endpoint the operation is served on: one document
  describes several listening endpoints, and a path alone does not say which one
  answers it.
- `tags`, carrying the name of the module that declared the fragment. The
  document's top-level `tags` lists one entry per such module, sorted by name,
  so a reader groups operations by the same vocabulary `x-fragments` uses.
- `security`. A fragment that declares none gets the requirement of an
  identified caller — `bearerAuth` or `sessionCookie` — because a route is
  guarded unless it says otherwise; a fragment that declares one keeps it as
  written, and the empty array is how a public operation says it admits every
  caller.

`components.securitySchemes` is written by the merge, once, from the platform's
vocabulary: `bearerAuth` (an `Authorization: Bearer` header) and
`sessionCookie` (the `sid` cookie). A fragment that declares either itself is
held to the same content. These three members and the two schemes are the
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
