# pkg/i18n

The user-facing text of the server: the interface package a module reads a
locale from and renders a message with, the engine that collects every declared
catalog and matches a request's language, the middleware that settles the locale
of a request, and the bridge to account preferences. It is a capability
directory in the shape `pkg/authn` takes: the root package declares the
interfaces, the value types and the resource type and registers no module, and
each subpackage registers a module of its own.

- `pkg/i18n/catalog` registers the engine, module name `i18n`: it delivers
  `Localizer`, `Negotiator` and `Resolver` exclusively, collects the declared
  catalogs over the available set, reads the default locale from the
  `i18n.default-locale` config item and an optional directory of extra catalogs
  from `i18n.catalogs-dir`, parses and validates every message at assembly, and
  takes an optional `Profile`.
- `pkg/i18n/http` registers `i18n.http`: it walks `http.Router`'s `Endpoints`
  and binds two middleware layers on each, one negotiating `Accept-Language`,
  one reading an account's stored language and time zone. It imports `pkg/http`
  and the `pkg/authn` root package.
- `pkg/i18n/account` registers `i18n.account`: it delivers a non-exclusive
  `Profile` over `authn.Accounts` and implements `authn.Provisioner`.

The API is the package's godoc. Read `Localizer`, `Resolver` and `Catalog`'
field and method comments before a module declares a catalog or renders a
message.

## Boundaries

- **The root package depends on the standard library only.**
  `golang.org/x/text` — its `language`, `feature/plural`, `message` and `number`
  packages — is bound in the engine (`pkg/i18n/catalog`) and nowhere else in
  this capability, so a module that only reads a locale imports this package and
  carries no language-matching or number-formatting library. `pkg/i18n/http` takes the negotiation result
  through the `i18n.Negotiator` capability and does not bind the library itself.
  A dependency added to the root package lands in the dependency list of every
  module that reads a locale; keep it out.
- **A message key is flat and namespaced by the module that renders it.**
  `authn.login-failed`, `http.404`. Two modules never share a key, and a key
  read off a problem's `Type` is unambiguous. The engine refuses a duplicate key
  at startup and names the two modules; declaring one is what keeps the refusal
  from firing.
- **A catalog is a compile-time static fact, with one assembly-time addition.**
  A module declares one with `//go:embed` over its own `locales/{tag}.json`
  files, one per locale, and puts the `Catalog` in its descriptor's `Resources`.
  The engine collects them over the available set: a catalog lives in the
  binary, so it counts whether or not the module is included in a given command,
  and the consumer does no subtraction. The `i18n.catalogs-dir` config item
  names a directory of extra catalogs in the same shape — one `{tag}.json` file
  per locale, the base name the locale, the body a JSON object of key to message
  — read through the same loader. When it is set, the engine reads it at
  assembly, after the declared catalogs, and merges it into the same index: the
  overlay wins on a key a declared catalog also carries, and a locale only the
  overlay carries enters the supported set and the negotiation matcher. An
  unset item, a path that is not an existing directory, and a directory holding
  no `*.json` file all mean no extras and are legal; a file the loader cannot
  read terminates startup under `catalog.ErrInvalidOverlay`, naming the file,
  and a message the parser cannot read terminates startup under
  `catalog.ErrInvalidMessage`, naming the key and its source.
  The index is read-only, IO-free and never reloaded afterward — the overlay
  too is read only at assembly — so the request path carries no lock and no IO
  for text, count, plural and number. A time form is the one exception: it reads
  the render context's zone through a small cache behind a mutex, taken only when
  such a message is rendered (see the time-form bullet below).
  There is no interface for changing a catalog or a message at runtime, and
  adding one would put a lock on the request path.
- **A message renders its typed arguments.** `Args` maps a parameter name to a
  typed `Value`, built with `i18n.String`, `i18n.Int`, `i18n.Float` or
  `i18n.Time`. A string is written into the text verbatim. An integer or a
  number is formatted for the message's language by the engine — a
  `message.Printer` on the locale's tag, a decimal `number.Decimal` for a float
  so that a value at or above 1e6 stays `1,000,000` rather than becoming an
  exponent, and not `fmt.Sprintf` — so the same value groups as `1,234,567` in
  en-US and `1.234.567` in de-DE. The syntax keeps
  `{{name}}` and the rule that a name with no value is left as written, and adds
  two constructs: `{{name, plural, <category>=<text>, ...}}` and
  `{{name, time, <layout>}}`. The plural selects a
  branch by the count's CLDR category for the language — the six categories
  `zero one two few many other` — with `#` in the branch standing for the count,
  formatted for the language; a count whose category the message does not carry
  falls back to `other`. The parameter must be an integer: a missing or
  non-integer one leaves the segment as written, like any missing parameter. The
  engine parses each message at assembly and refuses a malformed plural segment —
  a category that is not one of the six, a missing `other`, a repeated category —
  under `catalog.ErrInvalidMessage`. A plural branch is literal text and `#` and
  may not contain a nested placeholder; the engine refuses a nested placeholder
  under the same sentinel, rather than reporting it as a missing `other` branch.
- **A date or time inside a composite message is rendered by the backend.** The
  responsibility follows the payload shape: a message the backend assembles
  renders all of it — text, count, plural, number and date/time — while a
  timestamp returned as its own field travels as a typed value in the standard
  ISO format for the client to render. The two cases are not decided by the kind
  of data but by whether it is embedded in a composite string.
- **A `{{name, time, <layout>}}` segment formats an instant with the message's
  own layout and in the render context's time zone.** The parameter must carry a time
  (`i18n.Time`); a missing or wrong-kind value leaves the segment as written, the
  rule a plural segment follows. The layout is everything after the second comma
  up to the closing braces — a layout may itself contain commas, spaces and
  colons, as the Go layout `Mon, 02 Jan 2006 15:04` does, so the segment is split
  on its first two commas only and never on the commas inside the layout. The
  instant is formatted with `time.Time.Format` in the zone the context names
  through `i18n.TimeZoneFromContext`: an unset zone, and a zone name this process
  cannot load, both read as UTC rather than failing the render. The empty layout
  is malformed and terminates startup under `catalog.ErrInvalidMessage` naming
  the key and its source. A layout is a Go layout and that is the accepted cost —
  a month or weekday name comes out in English, so a locale wanting its own names
  writes them in its own message text. `Translate` carries no context, so a time
  form rendered through it reads the zone as unset, which is UTC.
- **The read path takes no lock and does no IO, with one guarded exception.**
  The catalog index is read-only, IO-free and never reloaded, and the request
  path carries no lock for text, count, plural or number. A time form is the one
  case that reads a zone: `time.LoadLocation` re-reads the zoneinfo files on
  every call — the standard library keeps no cross-call cache — so the engine
  holds a small cache of loaded locations behind a mutex. The lock is taken only
  when a message carrying a time form is rendered, and only until the zone's
  first lookup; a run that renders no time form never touches it.
  
- **`i18n.locale` and `i18n.tz` are preferences, not authorizing facts.** They
  are stored in `Account.Attributes` under `LocaleAttributeKey` and
  `TimeZoneAttributeKey`, read back into a request's context, and used for
  nothing a decision rests on. This is why the write side of the context — the
  `WithLocale` and `WithTimeZone` functions — is exported, unlike the write side
  of an identity in `pkg/authn`: a host starting a job for a known account writes
  the locale it resolved there directly. Directory and time zone change nothing
  about what a caller may reach; that is decided elsewhere.
- **The default locale is configuration, not a constant.** The set of locales a
  run supports is the set the declared catalogs and the configured overlay
  directory carry, and which of them a request that names none resolves to — and
  which a message falls back to when its key is missing — is the
  `i18n.default-locale` config item, "en-US" when unset. The check runs after
  the overlay is merged, so the overlay may supply the default locale. Startup
  refuses a set of catalogs that does not carry that locale (`ErrNoCatalog`),
  because the fallback every message relies on would then render bare keys. The
  interface package exports the namespace (`i18n.ConfigNamespace`) and the item
  name (`i18n.DefaultLocaleItem`) so the SMS module resolves the same single
  statement of the default.
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
- **`pkg/http` imports this package's root only, and the `pkg/authn` root
  package does not import it at all.** `pkg/http` takes the root package up to
  render the four refusals it writes itself — the no-route 404, the
  method-not-allowed 405, the body-too-large 413 and the recovered-panic 500 —
  through an optional `Localizer`, and declares its own catalog for them. The
  root package is standard-library only, so this adds nothing to `pkg/http`'s
  dependency budget and a module that only renders a message carries no
  matching library. The `pkg/authn` root package depends on the standard library
  and its own internal packages alone; the packages that need both an identity
  and a locale — `pkg/authn/login` among them — import `pkg/i18n`, and the
  direction is one-way. A dependency test in `pkg/authn` holds that direction;
  keep it out of the root package.
- **The module that produces a message renders it.** A problem carries finished
  text in `Title` and `Detail`; the module that raises it renders that text, and
  the entry point's `ErrorResponder` decides the shape alone and carries no
  language. `pkg/http` renders its own four refusals from its own catalog under
  `http.<status>` keys, falling back to `net/http`'s standard text for the
  status when no localizer is assembled or a catalog misses the key; a module
  that raises a problem of its own sets the title it wants. `Type` and `Status`
  are written unchanged — a machine code does not move with the language.

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
built on catalogs constructed in the test, or written into a temporary directory
the test makes, and the middleware's bring up a registry with an entry point and
a gateway. `subpackagesInBudget`-style dependency tests, where a subpackage runs
one, are what hold the standard library-only boundary of the root package and the
single-importer boundary of `golang.org/x/text` within this capability.
