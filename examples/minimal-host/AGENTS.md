# examples/minimal-host

The smallest host that assembles a run: a `main` that imports the modules
it wants, writes the command tree, and lets that tree drive the lifecycle.
It is a module of the workspace like any other, and it is the working
proof that the mechanism assembles something — what its `main` does not
need to contain is as much the point as what it does.

Everything in the directory except `main.go` and `host.go` is an ordinary
module: two implementations of one capability, and an application module
that takes the capability up without naming either. The command tree —
which commands there are, what each assembles, how far each drives — lives
in `main.go`, and nothing outside it contributes to the tree.

## Where the authority is

- The mechanism it exercises:
  [`docs/design/modules/design-core.md`](../../docs/design/modules/design-core.md)
  and
  [`docs/design/modules/design-config.md`](../../docs/design/modules/design-config.md).
- The logging this host narrates its run through:
  [`docs/design/modules/design-log.md`](../../docs/design/modules/design-log.md).
- The decisions behind the shape of a host:
  [`docs/adr/`](../../docs/adr/) — the assembly bootstrap above all.
- What this host itself demonstrates: the package comment in `main.go`
  lists the cases, each with a test in `e2e_test.go` — or in
  `wiring_test.go`, for the few whose property leaves no trace in a run.

## Boundaries

- **The host contributes its standing declarations as resources on a module
  it registers**, not as arguments of the driver. Host identity goes that
  way. What one run is asked to do — the participating set, the drive depth,
  the `ctx` — is a parameter of the drive instead: it describes that run
  rather than the registry, and a registry carrying it as a resource could
  not drive a second command. A change that grows the driver's signature to
  carry a standing declaration is still the shape to reject.
- **An example stays an example.** Nothing here is imported by a released
  module, and nothing here is a place to put code the modules should
  carry: if a host would have to write it again, it belongs in a module.
- **Keep the demonstrated cases and their tests together.** A case the
  package comment claims and `e2e_test.go` does not cover is a lie the
  next reader has no way to catch.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make lint
make check
```

`make check` is what CI runs. The host's own behaviour is covered by
`e2e_test.go`, which builds the binary and runs it; it needs nothing
installed beyond the Go toolchain.
