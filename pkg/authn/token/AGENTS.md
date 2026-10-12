# pkg/authn/token

The token module: the access tokens and the refresh tokens of the identity
system. An access token is a short-lived JWT signed with a key from the
configuration; a refresh token is an opaque random string whose digest is
stored in `authn_refresh_tokens`. Both are revoked through the login session
they name, so the token side has no revocation surface of its own.

The module `authn.token` delivers the token surface (`authn.Tokens`,
exclusively) and registers as a credential-verifying backend for the bearer
scheme (`authn.Verifier`, non-exclusively). It owns the signing and
verification key set, loaded from the files the configuration names, and the
refresh-token table; it publishes the public half of its keys as a JWK set. It
is the one package of the capability that speaks to the JOSE library.

This package holds the module registration, the configuration and settings
schemas, the key set with its startup validation and the JWK-set publication,
the access-token issuing path and the verifying backend, the refresh-token table
with its migration, rotation, reuse detection and sweep, revocation, the token
and revocation endpoints, the client_credentials grant, the operations metric,
and the cache-clearing event subscriptions.

The API is the package's godoc.

## Boundaries

- **This package is the one user of the JOSE library in the tree, and
  `jose.go` is the one file in the package that imports it.** Every signature
  a token's signing, verification or publication needs of the library is in
  `jose.go`; the rest of the package reads a token through `signedToken` and
  builds a key set through `marshalJWKS`, so a change of library reaches one
  file. `deps_test` in the root package holds both facts, and holds
  `pkg/go.mod` to a requirement of the library at or above v4.1.5, the
  version that fixes GHSA-6mp9-8xrw-74r4. `UnsafeClaimsWithoutVerification`
  never appears in a non-test file.
- **The key set is read from files at startup and never changes.** The
  startup validation is a closed set — the file reads and parses, the key's
  type agrees with its algorithm and the algorithm with
  `authn.token.algorithm`, an RSA modulus is at least 2048 bits, every kid is
  distinct, and at least one key exists — and a failure is `ErrInvalidKey`
  naming the file. A private key file other users can read is a `Warn`, not a
  failure. The `kid` is the key's RFC 7638 thumbprint, never a name from the
  configuration.
- **No signing key, refresh token, service secret or password reaches a log
  line, an error text, an event or a metric attribute.** A refresh token is
  held as its digest; the `spd_rt_` and `spd_cs_` prefixes let the log
  redactors recognise what must not be written.
- **The table is this module's.** `migrations/{postgres,sqlite}` hold the one
  migration that creates `authn_refresh_tokens`; `Start` probes the table and
  fails with `ErrNotMigrated` when it is absent, because `New` does not touch
  it (on a first start it is not there until the migration runs). Rows carry
  no foreign key; every time in them is UTC.
- **The issuer and the audience default off the gateway's address.** When
  neither is configured, `authn.token.issuer` takes
  `authn.gate.mount.public-url` and the audience takes the issuer, so an
  assembly that names neither still issues tokens a verifier can compare. An
  audience that resolves empty is `ErrInvalidConfig`.
- **The relaxation switch takes the primary source alone.** `check-session`
  is `config.OriginPrimary`, so neither the environment nor the command line
  can turn the session check off; the signing key file is `Sensitive` and
  carries no command-line flag. A switch that is off is a `Warn` startup line.
- **The key set is public; every other response is not.** `GET B/jwks.json`
  is registered through `mount.Public` and writes its own
  `Cache-Control: public, max-age=300` over the mounter's `no-store`, because
  its body holds no secret and its lifetime is the wait a key rotation counts
  on.
- **The refresh-token credential format and the stable names are a closed set.**
  The claim names, the `typ` value, the routes, the two token prefixes and the
  JWKS content type are written down by a reader outside the module; a change to
  one is a breaking change.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make lint
make check
```

`make check` is what CI runs. To run this package alone, from `pkg/`:

```
go test -race ./authn/token/...
```
