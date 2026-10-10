# pkg/config

The configuration module: it collects what every other module declares it
needs, reads the primary source, applies the environment and command-line
layers over it, and hands each module its own values back.

The root package carries the declarations, the manifest and the layers,
and defines the `Source` and `Format` extension points. Concrete
transports and formats are subpackages: `source/file`, `format/json`,
`format/yaml`.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-config.md`](../../docs/design/modules/design-config.md).
- The decisions behind it: [`docs/adr/`](../../docs/adr/) — its scope, the
  source extension point, the lifecycle position, the assembly bootstrap, the
  supplied instance.
- The API itself: the godoc in this package and in each subpackage.

This file states only what neither of those states: the boundaries a
change here must not cross, and how to run it.

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
