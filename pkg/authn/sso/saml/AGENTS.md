# pkg/authn/sso/saml

The SAML 2.0 enterprise single sign-on method of the authentication capability:
the login method named `saml`, the service provider metadata route, and the
on-demand refresher of a provider's identity provider metadata. It delivers
`authn.LoginMethod` and `authn.SSOMetadataRefresher` non-exclusively and requires
`authn.SSOProviders`, `authn.Accounts` and `mount.Mounter`. This is the only
package of the module that imports gosaml2 and goxmldsig.

The API is the package's godoc. Read the `LoginMethod` comments in
[`pkg/authn/login.go`](../../login.go) before changing the method's shape.

## Boundaries

- **The supplementary checks are the security part, and every one of them is
  fail-closed.** The library's `RetrieveAssertionInfo` is never called: it uses
  the first assertion only, does not check `InResponseTo` and skips a comparison
  against an empty issuer or destination. The order in `consume` is fixed; a
  check is added with a negative test that fails before it lands, and the
  outcome it is counted under is one of the closed set.
- **One parse, and the verified element is the used element.** The response is
  decoded and its signature validated once by `ValidateEncodedResponse`, and the
  returned, verified `types.Response` is what every later check reads. A compiled
  assertion in `assertion_test.go` holds the library's return type, and a test
  holds that a response is taken as signed only when the envelope or an assertion
  carried a validated signature.
- **The self-check cannot refuse an algorithm, so a lexical pre-check does.** The
  library's validation context accepts SHA-1; `usesSHA1` scans every `Algorithm`
  attribute of the decoded document and refuses `sha1` unless the provider sets
  `allow_sha1`. It is conservative by design and may refuse a document that
  carries no SHA-1 signature; the vectors in `sha1.go`'s tests hold the boundary.
- **The assertion is single-use through the register's replay line.** The
  identifier is recorded through the `assertionLog` the provider register
  implements, and a repeat is `authn.ErrAssertionReplay`. A register that does not
  implement the replay line refuses to build the module. The table belongs to
  `authn.sso`; this package never reaches it directly.
- **The subject and the attributes are bounded and never taken from a value the
  client controls.** The subject is the `NameID` when its format is stable, or the
  configured subject attribute; `transient` and `emailAddress` `NameID`s are not
  subjects. The `idp.*` attributes are cut to the session payload's per-entry
  bound, and no secret, token or assertion value reaches a log, an error or a
  metric.
- **Every outbound request goes through `internal/egress`.** The metadata
  refresher fetches through the restricted client, bounded by the configured
  timeout; its URL comes from a tenant administrator, so the client's destination
  check is the protection. A parse or validation failure leaves the stored config
  as it was and returns the error.
- **The metadata is derived from the configured public address alone.** The entity
  identifier and the ACS address come from `public-url` and the base path, never
  from the `Host` header or a forwarded header, so a provider's IdP sees a stable
  service provider.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

On this package alone:

```
go -C pkg build ./authn/sso/saml/...
go -C pkg vet ./authn/sso/saml/...
go -C pkg test ./authn/sso/saml/...
go -C pkg test -race ./authn/sso/saml/...
env -C pkg golangci-lint run ./authn/sso/saml/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages.

The tests build their own identity provider: `fixture_test.go` generates an RSA
key and a self-signed certificate and signs each response with goxmldsig, so a
vector changes one field of `responseOpts` to make exactly one check fail.
`metadata_test.go` serves a fixed metadata document through a stubbed transport
and drives the metadata route through the shared `mounttest` stand-in.
`assembly_test.go` drives a real registry through the descriptor with a stand-in
module for each required capability.
