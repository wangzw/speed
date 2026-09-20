# examples/authenticated-host

A host that puts an HTTP entry point behind authentication: an entry point
implementation, the authentication gateway, one credential backend, and one
application module that binds a guarded route and a public one.

It is a sibling of `examples/minimal-host` rather than an extension of it.
That host exists to show the smallest assembly there is — configuration,
logging and capability resolution — and it has no HTTP at all; adding an entry
point and an authentication layer to it would change what it demonstrates.

## What it is for

Two things neither a unit test nor a module's own suite can hold:

- **The assembly actually guards traffic.** The middleware layer is registered
  on an endpoint during Init, and whether that registration happened is not
  observable from inside the gateway's own package. `e2e_test.go` starts the
  compiled binary, reaches it over a socket and reads status codes.
- **A host's imports decide what its binary can do.** Importing
  `pkg/authn/gate` is what gives this binary an authentication entry;
  importing `pkg/authn/static` is what decides which credentials it can check.
  Neither is configuration.

## Boundaries

- **The command tree is the host's, and it is in `main.go`.** No module
  declares a command and none can learn which command took it in.
- **The serving command names every module it assembles.** `IncludeAll()` is
  the shorter spelling and the one a command carrying authentication must not
  use: it makes the participating set a function of the import graph, so a
  credential-verifying backend that arrived through somebody's dependency — and
  that nobody wrote down here — could issue identities.
- **Every account is a hash in the configuration, never a password.** The
  `hash` command is how one is produced. A case that wrote a readable password
  into a config file would be showing the wrong thing.
- **A guarded route's handler reads the identity from the context.** Reading
  the `Authorization` header in a handler would be a second authentication
  decision, made somewhere nobody is looking; `listOrders` fails loudly instead
  when the context carries no identity, which is what a route reached by a path
  that skipped the gateway looks like.

## Running it

```
go -C examples/authenticated-host build ./...
go -C examples/authenticated-host test ./...
```

The cases compile the host once in `TestMain` and run it as a child process on
a port taken from the operating system. They need the Go toolchain and a
loopback interface, and nothing else installed.

By hand:

```
go -C examples/authenticated-host run . hash            # reads a password on stdin
go -C examples/authenticated-host run . serve --config file:///path/to/config.yaml
```

A configuration this host serves on:

```yaml
http:
  endpoints:
    public:
      address: "127.0.0.1:8080"
authn:
  gate:
    endpoints:
      - public
  static:
    name: local
    subjects:
      alice: "pbkdf2-sha256$600000$...$..."
```

`authn.gate` is the gateway's own section and `authn.static` is the backend's,
beside it. Each module hangs under the release unit's heading and none of them
owns the heading itself.

With `authn.gate.endpoints` absent the gateway states itself disabled with a
reason naming that key — and this host then **fails to start** rather than coming up
with its routes unguarded. What stops it is the application module: it takes
the gateway up in its own Init to register the exemption, finds no provider,
and ends the startup. That is not the framework catching the mistake. It is a
consequence of the route and its exemption being registered in one place, and
it is held by a case in `e2e_test.go` so that a later refactor cannot quietly
give it up.
