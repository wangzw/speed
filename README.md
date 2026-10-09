# speed

A configuration-driven modular application framework, delivered as Go
libraries.

speed gives a Go program a modular skeleton and a set of general-purpose
modules. It does not take over the entry point: a host writes its own
`main`, imports the modules it wants, and drives startup and shutdown.
What the program is — an HTTP service, a gRPC service, a background
process with no inbound entry point at all — is the host's decision, not
the framework's.

A module declares what it provides, what it requires and what
configuration it accepts. Which modules exist in a binary is settled by
what is imported; which of them run is settled by the host — the modules
the selected command takes in at run level, together with the
non-exclusive providers of what they require and, in turn, of what those
providers require (optional requirements are not followed), plus any
module the host supplies an instance for — and by resolving exclusive
claims among them. Under a command tree, a provider that claims its
capability exclusively takes part only where the command names it or the
host supplies an instance for it. A host that writes no command tree takes
in every registered module. The configuration holds no list of modules.

## Getting started

Build, test and check everything in the workspace:

```
make check
```

Ask a command what it takes. The arguments it prints are the ones its modules
declared as flags; the rest reaches them through the configuration file and the
environment:

```
go -C examples/orders-host run . serve --help
```

Run it for real — every module in this repository, one process, no external
service:

```
ORDERSHOST_CONFIG=file:testdata/local.yaml go -C examples/orders-host run . migrate
ORDERSHOST_CONFIG=file:testdata/local.yaml go -C examples/orders-host run . serve
```

It migrates, seeds four tenants (one of them disabled) and orders for three of
them, serves and waits. Ctrl-C stops the HTTP entry point first, letting the
requests it admitted finish within its drain timeout, then stops the other
modules in reverse dependency order; once every stop has returned, every module
is closed in reverse dependency order. The account it serves is `alice` with
the password `s3cret`.

`examples/crm-host` is the larger host: a multi-tenant CRM that assembles the
whole module set on five deployment stacks — in-memory SQLite, and four
combinations of PostgreSQL, Redis, NATS and Memcached — and drives the same
end-to-end cases against each, so every implementation of the database,
key-value, event and telemetry capabilities runs the same product-shaped
traffic. It exists to evaluate the modules, and a defect it runs into is fixed
in the module rather than worked around; its `AGENTS.md` says what it needs to
run (`examples/crm-host/AGENTS.md`).

`make help` lists the other entry points. The toolchain versions the
checks need are pinned in `.mise.toml`.

## Layout

| Path | What it is |
|---|---|
| `pkg/` | The modules: one capability directory each, in a single Go module. |
| `examples/` | Hosts that assemble modules into running programs. |
| `docs/` | The design documents and decision records. |
| `tools/` | Repository self-checks. |
| `go.work` | The module roster every command derives its module set from. |

## Documentation

The design lives in [`docs/design`](docs/design): the whole picture in
`architecture.md`, one document per module under `modules/`. The
reasoning behind each decision, with the alternatives and the price, is
in [`docs/adr`](docs/adr). Terms mean what
[`docs/glossary.md`](docs/glossary.md) says they mean. Those three are
written in Chinese; everything else in the repository is English.

Each module carries its own `AGENTS.md` with the boundaries a change
inside it must respect. `CLAUDE.md` orients an AI coding agent in the
repository as a whole.
