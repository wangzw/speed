# pkg/internal/openapigen

The generator that turns a module's operation declarations into its OpenAPI
fragment. It registers no module, delivers no capability and touches no
lifecycle: it is a build-time library called by a module's generation test, and
it never enters a running process.

## API

```go
func Generate(title, version string, operations http.Operations) ([]byte, error)
```

`Generate` reads `pkg/http`'s declaration types — `Operation`, `Body`,
`Response` — and returns the module's fragment as JSON. `title` and `version`
become the fragment's `info` section. Paths stay relative to the API root, the
form every fragment in this repository uses.

The declarations describe:

- an operation's id, method, path, summary and description;
- its parameters as an `Input` struct whose fields carry `path:"name"`,
  `query:"name"`, `header:"name"` or `cookie:"name"` tags, with the field type
  and its tags giving the schema and its description;
- its request body as a media type and a structure type;
- its responses as status, description, an optional headers struct (fields
  tagged `header:"Name"`) and an optional body.

Descriptions and schema constraints are struct tags read by
`github.com/swaggest/jsonschema-go`: `description`, `required`, `format`,
`pattern`, `minimum`, `maximum`, `enum` and the rest, plus `nullable:"false"`
to keep a slice out of a null union. A struct-level description goes on a blank
field:

```go
_ struct{} `description:"..."`
```

## The request and reply forms

The generator mirrors the entry point's binding and reply model:

- **Request bodies** follow the declared `RequestBody`: JSON bodies carry the
  `json`-tagged fields, form bodies the `form`-tagged ones, multipart bodies the
  form fields plus file fields published as binary strings, and an
  octet-stream body is a binary string. The request body's description and
  required flag are published.
- **Responses** follow the reply kind the declaration allows: a JSON body is
  the declared structure's schema; an octet-stream or other non-text payload is
  a binary string; a text payload is a string; `text/event-stream` publishes
  the media type with no body schema; a redirect publishes its `Location`; a
  204/304 publishes no content. A declared media type of `*/*` publishes a
  `*/*` content entry with a binary schema.
- **Declarations the entry point refuses are refused here too**: a body on a
  no-body status, a body-tagged input without a media type, an unknown media
  type, a JSON answer without a structure, and a simple operation without
  exactly one 2xx.

## The dependency

The generator bridges `github.com/swaggest/openapi-go` and
`github.com/swaggest/jsonschema-go`: the library reflects Go types into JSON
Schemas and assembles an OpenAPI 3.1 document, and this package maps the
declarations onto it and normalises the output.

Both are **build-time dependencies**. Nothing in a module's non-test code
imports this package, so no host binary links them, and `pkg/http`'s dependency
budget is unchanged. `pkg/go.mod` carries them because this package lives in
that module.

## Normalisation

The library's defaults are not the platform's shape, so `Generate` normalises:

- `ProblemDocument` is published under the component name `Problem`, and every
  `$ref` with it, because the component is shared verbatim by every fragment;
- `null` is dropped from type unions — no published fragment declares a
  nullable field;
- a parameter's schema does not repeat the parameter's description;
- response headers carry no `style`;
- components are named by their Go type without the declaring package's prefix.

Changing a normalisation rule changes the contract bytes for every generated
fragment; do it together with the fragments it affects.

## Generating and checking

A migrated module carries a test that generates its fragment and compares it
with the committed file, writing it when `UPDATE_FRAGMENTS=1`:

```
UPDATE_FRAGMENTS=1 go -C pkg test ./feature/board -run TestTheFragmentIsGeneratedFromTheDeclarations
```

Without the variable the same test is the drift gate: a declaration that changed
without regenerating the fragment fails it. `make check` runs it.

## Running it

```
go -C pkg build ./internal/openapigen/...
go -C pkg test ./internal/openapigen/...
env -C pkg golangci-lint run ./internal/openapigen/...
```
