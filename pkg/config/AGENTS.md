# pkg/config

The configuration module: it collects what every other module declares it
needs, reads the primary source, applies the environment and command-line
layers over it, and hands each module its own values back.

The root package carries the declarations, the manifest and the layers,
and defines the `Source` and `Format` extension points and the `Defaults`
resource. Concrete transports and formats are subpackages: `source/file`,
`format/json`, `format/yaml`.

The API is the package's godoc, the root package's and each subpackage's.

## Boundaries

- **The root package carries the parsing tools, and nothing else
  third-party.** Command-line parsing, the command tree, layering and type
  conversion are delegated, and those imports sit here because the manifest
  and the command tree do. Every module that declares configuration imports
  this package, so that budget is spent for all of them at once; spending it
  again on anything else needs the same kind of decision that spent it the
  first time.
- **A transport or a format still belongs to the subpackage that needs it.**
  The split exists so a host that configures itself in JSON carries no YAML
  parser, and delegating the parsing does not change that.
- **A transport or a format reaches the assembly by being imported**, as a
  resource of a module registered in `init`. Nothing about it is named in
  the root package.

```go
import _ "github.com/vislake/speed/pkg/config/format/yaml"
```

- **This module depends on `pkg/core` and on nothing else in the
  repository.** Its loading runs before assembly begins, so anything it
  required from another module would have to exist before configuration
  does.
- **The command tree is the host's.** Nothing here names a subcommand, and
  no module contributes one: which commands exist, and which modules each
  one includes, is written in the host's `main`.
- **The root package registers one module, `config`, and it declares only
  the delivery of the reader** (`pkg/config/module.go`): no callback, so no
  `New` and no `Prepare`, no `Resources` and no `Exclusive` claim. Loading
  runs before assembly, so `Execute` supplies the reader to it by name,
  `ModuleName`, before the drive begins. Without an exclusive claim the
  closure of a module that requires the reader takes it in, so a reader
  nobody supplied fails the startup, saying the module has no `New` and was
  handed no instance. A registry made with `core.New` registers
  `config.Module()` by hand ahead of `NewApp`, which panics without it.
  Transports and formats register one module each, carrying only
  `Resources`.
- **No value is interpreted on a module's behalf.** The declaring module
  gets its own values back and decides what they mean.
- **A declared namespace is a section even with no input items under it.** A
  module whose items have all moved to the settings store still owns its
  namespace: a source that writes a key under it is refused by that key's full
  path, not by the namespace alone. A section and another module's input item
  may not share a path, since no source can read one path as a value on one
  side and a section on the other; two modules sharing a namespace may, since
  that only divides paths between them.

## The host defaults document

A host may compile one configuration document into its binary and deliver it
as a `Defaults` resource on a module it registers. It is a second source for
the default layer, below the primary source and above each module's prototype
field values. It is not a second primary source: a run still has at most one
primary config source.

- **At most one per run.** Two declarations fail the startup with
  `ErrConfigConflict`, naming both modules.
- **Format.** `Defaults.Format` names a registered `Format`, matched exactly
  after folding to lower case, the same way the primary source's name is
  matched. There is no content sniffing; an empty or unknown name is
  `ErrUnknownFormat`, and data the parser refuses is `ErrMalformedConfig`.
- **Keys.** The document is judged like the primary source: a key no module
  declares, or one the owning module does not accept from the primary source,
  fails the startup. A container leaf it gives replaces the prototype's whole
  value, and a primary source giving the same leaf replaces the document's
  whole value; no layer merges inside a container.
- **Required.** The document satisfies `Required`: naming a value here is
  the host declaring it, the same act as writing the key in the configuration
  file. A key written as null gives nothing and satisfies nothing. A value
  only the prototype supplies never satisfies `Required`. A required item the
  document supplies carries no required mark in the help output, because
  leaving the argument out starts the run; its value is rendered as the
  argument's default the way any other default is.
- **No new dependency.** The document arrives as bytes and leaves through a
  registered `Format`. Adding this resource imports nothing, and the root
  package still carries no third-party dependency of its own.
- **The content is the host's business.** This module applies the document to
  the keys the manifest carries. It does not know any module's topology, does
  not interpret what a container holds, and names no module in the document.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make lint
make check
```

`make check` is what CI runs. The subpackages are covered by the same
commands; nothing needs to be installed beyond the Go toolchain.
