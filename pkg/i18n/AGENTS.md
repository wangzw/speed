# pkg/i18n

The user-facing text of the server: the interface package a module reads a
locale from and renders a message with, the engine that collects every declared
catalog and matches a request's language, the middleware that settles the locale
of a request, the responder that renders an error in that locale, and the bridge
to account preferences. It is a capability directory in the shape `pkg/authn`
takes: the root package declares the interfaces, the value types and the
resource type and registers no module, and each subpackage registers a module of
its own.

- `pkg/i18n/catalog` registers the engine, module name `i18n`: it delivers
  `Localizer`, `Negotiator` and `Resolver` exclusively, collects the declared
  catalogs over the available set, reads the default locale from the
  `i18n.default-locale` config item, and takes an optional `Profile`.
- `pkg/i18n/http` registers `i18n.http`: it walks `http.Router`'s `Endpoints`
  and binds two middleware layers on each, one negotiating `Accept-Language`,
  one reading an account's stored language and time zone. It imports `pkg/http`
  and the `pkg/authn` root package.
- `pkg/i18n/respond` registers `i18n.respond`: it delivers a non-exclusive
  `http.ErrorResponder` that renders a `Problem` in the request's locale.
- `pkg/i18n/account` registers `i18n.account`: it delivers a non-exclusive
  `Profile` over `authn.Accounts` and implements `authn.Provisioner`.

The API is the package's godoc. Read `Localizer`, `Resolver` and `Catalog`'
field and method comments before a module declares a catalog or renders a
message.

## Boundaries

- **The root package depends on the standard library only.**
  `golang.org/x/text/language` is bound in the engine (`pkg/i18n/catalog`) and
  nowhere else, so a module that only reads a locale imports this package and
  carries no language-matching library. `pkg/i18n/http` takes the negotiation
  result through the `i18n.Negotiator` capability and does not bind the library
  itself. A dependency added to the root package lands in the dependency list of
  every module that reads a locale; keep it out.
- **A message key is flat and namespaced by the module that renders it.**
  `authn.login-failed`, `http.404`. Two modules never share a key, and a key
  read off a problem's `Type` is unambiguous. The engine refuses a duplicate key
  at startup and names the two modules; declaring one is what keeps the refusal
  from firing.
- **A catalog is a compile-time static fact.** A module declares one with
  `//go:embed` over its own `locales/{tag}.json` files, one per locale, and puts
  the `Catalog` in its descriptor's `Resources`. The engine collects them over
  the available set: a catalog lives in the binary, so it counts whether or not
  the module is included in a given command, and the consumer does no
  subtraction. A catalog is loaded once, at `Init`, and is read-only, IO-free and
  never reloaded afterward. There is no interface for changing a catalog or a
  message at runtime, and adding one would put a lock on the request path.
- **`i18n.locale` and `i18n.tz` are preferences, not authorizing facts.** They
  are stored in `Account.Attributes` under `LocaleAttributeKey` and
  `TimeZoneAttributeKey`, read back into a request's context, and used for
  nothing a decision rests on. This is why the write side of the context — the
  `WithLocale` and `WithTimeZone` functions — is exported, unlike the write side
  of an identity in `pkg/authn`: a host starting a job for a known account writes
  the locale it resolved there directly. Directory and time zone change nothing
  about what a caller may reach; that is decided elsewhere.
- **The default locale is configuration, not a constant.** The set of locales a
  run supports is the set the declared catalogs carry, and which of them a
  request that names none resolves to — and which a message falls back to when
  its key is missing — is the `i18n.default-locale` config item, "en-US" when
  unset. Startup refuses a set of catalogs that does not carry that locale
  (`ErrNoCatalog`), because the fallback every message relies on would then
  render bare keys. The interface package exports the namespace
  (`i18n.ConfigNamespace`) and the item name (`i18n.DefaultLocaleItem`) so the
  SMS module resolves the same single statement of the default.
- **An empty locale or time zone is not written.** `WithLocale` and
  `WithTimeZone` leave the context unchanged for an empty value, so a context
  naming nothing reads as naming nothing. The fallback lives in the resolver, not
  in the context: `Resolver.LocaleOf` answers the run's configured default for a
  context that carries none.
- **A message is never rendered empty.** `Localize` falls back from the requested
  locale to the run's configured default and then to the key itself, and reports
  no error; an
  unsupported locale or an unknown key is a fallback, not a failure. `Translate`
  does not fall back — it reports whether a catalog carried the key through its
  second return value, and returns the empty string when none did.
- **Negotiation reports no match as `false`, not as the default.** A caller reads
  `false` as "leave the locale unset", which is a different thing from "use the
  default", so a request whose header names no supported language gets no locale
  written and the resolver decides further down.
- **The engine is the one writer of a locale onto the request chain, and the two
  layers each do half.** `i18n:accept-language` is in the observe stage at the
  front of it and never refuses a request; it negotiates the header and writes
  the locale, which holds for an anonymous request too. `i18n:profile-locale` is
  in the identify stage, anchored after `(*authn.Gate)(nil)` — it reads an
  account's attributes only once the gateway has stamped an identity — and does
  not overwrite a locale the first layer already wrote. A layer that negotiates
  and reads the account in one place would either run before an identity exists
  or refuse the header for anonymous requests.
- **The engine delivers three capabilities exclusively and takes one
  optionally.** `Localizer`, `Negotiator` and `Resolver` come from one engine: a
  run has one set of languages, taken by `core.Resolve`, and two engines fail in
  resolution. `Profile` is non-exclusive and optional — the engine takes every
  implementation with `core.ResolveAll`, and none is a legal configuration in
  which the resolution chain falls through to the configured default.
- **`pkg/http` does not import this package, and neither does the `pkg/authn`
  root package.** `pkg/http` hands a `Problem` to the delivered
  `ErrorResponder` and knows nothing about a locale. The `pkg/authn` root package
  depends on the standard library and its own internal packages alone; the
  packages that need both an identity and a locale — `pkg/authn/login` among
  them — import `pkg/i18n`, and the direction is one-way. A dependency test in
  `pkg/authn` holds that direction; keep it out of the root package.
- **`pkg/http`'s `Problem` carries the message key and its arguments.** `Key
  string` and `Args map[string]string` were added to it for this package's
  responder; the default response leaves them out of the body, so adding them
  changed nothing a client sees. A handler that renders a localizable error sets
  `Key` and `Args`; one that does not leaves them empty and the responder derives
  a key from `Type` (dropping the `urn:speed:` prefix and turning `:` into `.`)
  or falls back to `net/http`'s `StatusText`. `Type` and `Status` are written
  unchanged — a machine code does not move with the language.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make lint
make check
```

This directory is a package tree of the `pkg` module, which owns the only
`go.mod`, so it is built and tested through that module:

```
go -C pkg build ./i18n/...
go -C pkg vet ./i18n/...
go -C pkg test ./i18n/...
go -C pkg test -race ./i18n/...
env -C pkg golangci-lint run ./i18n/...
```

The tests need no service, no database and no environment: the engine's are
built on catalogs constructed in the test, and the middleware's bring up a
registry with an entry point and a gateway. `subpackagesInBudget`-style
dependency tests, where a subpackage runs one, are what hold the standard
library-only boundary of the root package and the single-importer boundary of
`golang.org/x/text`.
