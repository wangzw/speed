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

`make check` runs the unit cases and needs nothing installed beyond the
toolchain. The integration cases — the ones that run against PostgreSQL, Redis,
NATS and memcached — carry the `integration` build tag and are not part of it;
the backends they need come from the compose stack at the repository root:

```
make deps-up             # docker compose up -d --wait
make test-integration    # brings that stack up, then runs the tagged cases
make deps-down
```

`starters/demo` is the copyable product skeleton: a runnable program that
assembles this repository's modules on six deployment stacks. Ask a command
what it takes. The arguments it prints are the ones its modules declared as
flags; the rest reaches them through the configuration file and the
environment:

```
go -C starters/demo/server run . sqlite-memory serve --help
```

Run it for real — every module in this repository, one process, no external
service. A run has to be pointed at a configuration: the locator is a URI, so a
local file is written `file:testdata/…`, and the environment variable
`DEMO_CONFIG` carries it in place of `--config`:

```
go -C starters/demo/server run . --config file:testdata/sqlite-memory.yaml sqlite-memory migrate
go -C starters/demo/server run . --config file:testdata/sqlite-memory.yaml sqlite-memory serve
```

The `sqlite-memory` stack needs nothing installed, so it is the one to start
with; the stack is one level of the command tree — `demo <stack> <command>` —
and the other four add PostgreSQL, Redis, NATS and Memcached, whose backends
`starters/demo/compose.yaml` brings up. Ctrl-C stops the HTTP entry point
first, letting the requests it admitted finish within its drain timeout, then
stops the other modules in reverse dependency order; once every stop has
returned, every module is closed in reverse dependency order. Copy the
directory, rename it and grow a product on it; `starters/demo/AGENTS.md` says
what the program carries.

`make help` lists the other entry points. The toolchain versions the
checks need are pinned in `.mise.toml`.

## Layout

| Path | What it is |
|---|---|
| `pkg/` | The modules: one capability directory each, in a single Go module. |
| `starters/` | Runnable, copyable product skeletons; `starters/demo` assembles the modules into one program. |
| `docs/` | The requirements, the design documents and the decision records. |
| `tools/` | Repository self-checks. |
| `go.work` | The module roster every command derives its module set from. |

## Documentation

What the product must do, seen from outside, is in
[`docs/prd`](docs/prd): one document per capability and one per business
process. The design lives in [`docs/design`](docs/design): the whole
picture in `architecture.md`, one document per module under `modules`.
The reasoning behind each decision, with the alternatives and the price,
is in [`docs/adr`](docs/adr). Terms mean what
[`docs/glossary.md`](docs/glossary.md) says they mean. Those four are
written in Chinese; everything else in the repository is English.

Each module carries its own `AGENTS.md` with the boundaries a change
inside it must respect. `CLAUDE.md` orients an AI coding agent in the
repository as a whole.
