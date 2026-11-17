# pkg/authn/sso

The enterprise single sign-on module of the authentication capability: the
register of the identity providers (IdPs) of tenants, the ownership proof of
their email domains and the domain discovery route. It holds the three tables
`authn_idps`, `authn_idp_domains` and `authn_saml_assertions` and delivers
`authn.SSOProviders` exclusively. The SAML and OIDC login methods are the
separate modules `authn.sso.saml` and `authn.sso.oidc` under this directory,
which this module does not import.

An IdP is data and not a module: it appears at run time, its number is not fixed
and it carries a secret, so it lives in a table rather than in the settings or
the configuration. The client secret of an OIDC provider sits in an encrypted
column; the module also declares the `authn.sso` namespace's inputs
(`allow-private-egress`, `dns-resolver`, `oidc-ca-file` and the
`assertion-max-age`, `provider-cache-ttl` and `egress-timeout` settings) that the
protocol modules read.

The API is the package's godoc. Read the `SSOProviders` comments in
[`pkg/authn/sso.go`](../sso.go) before changing an interface.

## Boundaries

- **The client secret never leaves the module except through `Secret`.** No read
  handed out — `Get`, `List`, `Discover` — selects the column, so the config a
  caller sees holds no secret, and no log, event or metric carries one
  (`readIDP` never selects `client_secret`, only `readSecret` does;
  `TestTheReadsNeverHoldTheSecret`). The column is written and read through the
  model path, so the database module's serializer seals and opens it, and `Start`
  proves the key is usable with a rolled-back round trip that ends the startup
  with `ErrNoEncryptionKey` without one (`register.roundTrip`,
  `TestStartRefusesWithoutARootKey`).
- **A verified domain is globally unique; unverified claims are not.** The unique
  index `authn_idp_domains_verified` on the verified rows is the arbiter, not an
  application-level read-then-write, so two providers verifying one domain at once
  have exactly one winner and the rest get `ErrDomainClaimed` (`markVerified`,
  `TestTwoProvidersVerifyingOneDomainHaveOneWinner`). A claim another provider has
  verified is refused up front too (`TestClaimRefusedOnceAnotherProviderVerifiedIt`).
- **The config is validated on write and an unknown key refused.** A wrong shape,
  a missing required key, an OIDC issuer that is not a concrete https one, an
  `algs` set holding `none` or an `HS*`, a SAML provider missing one of its three
  keys, and `transient` (or an `emailAddress` with no subject attribute) are each
  `ErrInvalidConfig` naming the key (`validateConfig`,
  `TestPutRefusesAConfigItCannotRunOn`). `provisioning` has no default, and an
  update may not move a provider to another tenant.
- **The username strategy is a combination the write refuses incomplete.** A
  `jit` provider names `username_strategy`, `claim` or `user-confirm`; a `claim`
  strategy names `username_claim`, `preferred_username` or `email`; no other
  strategy carries a claim; a `preprovisioned` provider needs none. The
  application holds the closed sets rather than a CHECK on the added columns,
  and a stored `jit` row with no usable strategy is refused at login
  (`checkUsernameStrategy`, `TestPutHoldsTheUsernameStrategyToItsCombinations`).
- **The failure direction is closed.** A database that cannot be read is
  `ErrUnavailable` on every method; a domain verification that finds no record
  changes nothing; a re-verification whose lookup fails keeps the verification and
  returns the failure (`TestReverifyKeepsAPageableLookupFailure`).
- **The TXT lookup has one path and one comparison.** `VerifyDomain` and
  `ReverifyDomains` go through `p.lookup`; an unset `authn.sso.dns-resolver`
  leaves `net.DefaultResolver` in place, and a set one replaces only the resolver
  the lookup dials (`systemLookup`, `resolverLookup`, `lookupTXT`,
  `TestAnUnsetResolverUsesTheSystemResolver`,
  `TestAConfiguredResolverCarriesTheTXTLookup`). The TXT name, the
  `speed-authn=<token>` comparison and the failure direction do not change.
  `authn.sso.oidc-ca-file` is declared here because this module owns the
  namespace; the enterprise OIDC method reads it and appends its certificates to
  the system roots (`trustRoots`). Both items take the primary configuration
  source alone and default to the system resolver and the system roots, so a
  deployment that configures neither behaves as before.
- **The sweep is bounded and each table has its own period.** Unverified claims
  go after seven days (hourly), assertion identifiers as soon as their condition
  passes (every ten minutes), a batch at a time until one comes back short, and
  `Stop` waits at most five seconds for the goroutine (`startSweep`, `Sweep`,
  `TestSweepRemovesStaleUnverifiedClaims`, `TestSweepRemovesExpiredAssertions`).
  Every time is read through `internal/clock` and is UTC, so the text SQLite
  compares is UTC on both sides under any local zone
  (`TestTheSweepsHoldUnderAnotherLocalZone`).
- **The SAML method records consumed assertion identifiers through this
  register.** The register answers `RememberAssertion` beyond the `SSOProviders`
  method set: the interface's method set is fixed, so `authn.sso.saml` reaches it
  through its own narrow interface and refuses to build without it. The write is
  a single `INSERT` on `authn_saml_assertions` whose primary key makes a second
  consumption `authn.ErrAssertionReplay`, and the condition is the earlier of the
  assertion's and its bearer confirmation's (`RememberAssertion`,
  `rememberAssertion`, `TestRememberAssertionRecordsOnceAndRefusesAReplay`).
- **The management surface reads domain claims through an extension shape.**
  The register answers `Domains` and `DomainsMany` beyond the `SSOProviders`
  method set, because the published method set is fixed and the provider views
  need every claim with its verification state. Each answer is
  `authn.SSOProviderDomain`, sorted by domain; the batch read gives every
  provider named an entry, empty when it has none. `authn.admin` asserts the
  delivered register to this shape and mounts no SSO provider group without it
  (`Domains`, `DomainsMany`, `TestDomainsReportsEachClaimAndItsState`).
- **The module knows no tenant and imports no protocol library.** No file here
  imports `pkg/tenant`; the tables embed no tenant model, because the discovery
  and the callback route have no tenant context. `authn.sso` imports neither
  gosaml2 nor go-oidc — those are the leaf modules' alone (`Module`).
- **The discovery leaks nothing about an account.** It takes only an email's
  domain, never its local part, and answers the same shape — an empty list when
  nothing matches — so a request cannot be used as an account-existence oracle
  (`handleDiscover`, `TestTheDiscoveryOfAnUnknownDomainIsTheSameEmptyShape`). It
  is registered public. A domain that cannot be reduced is a 400 `bad-request`;
  a body the capability's decoder refuses is a 400 `bad-request` and a body of
  another media type a 415 `unsupported-media-type`, both before any lookup.
- **The change event is a hint, never the authority.** `Put` and `Delete` publish
  `authn.idp.changed` after the commit, and a domain verification, withdrawal or
  removal publishes its own change; a publication that fails is a `Warn` and never
  fails the write (`publishChange`, `TestPutAndDeletePublishTheChange`).
- **A setting moved toward more lenient is visible after the fact, never
  blocked.** The subscription to `settings.changed` writes a `Warn` and a
  best-effort `authn.settings.relaxed` audit row naming the key and the
  direction and no value; the one setting it watches is `assertion-max-age`,
  relaxing in the `increase` direction (`relax.go`,
  `TestARelaxingChangeWritesAWarnAndAnAuditRow`).

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

CI runs the segments of `make check` as parallel jobs. On this module alone:

```
go -C pkg build ./authn/sso/...
go -C pkg vet ./authn/sso/...
go -C pkg test ./authn/sso/...
go -C pkg test -race ./authn/sso/...
env -C pkg golangci-lint run ./authn/sso/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages.

The PostgreSQL leg of a dialect case carries the `integration` tag and needs the
repository's compose stack; a default run compiles none of it, and an integration
run fails without it. The SQLite leg asks for nothing. `helpers_test.go` builds the product by hand over a
real database handle (`pkg/internal/dbtest`) with a root key configured and every
collaborator a case drives — the bus, the logger, the clock and a fake DNS
resolver — each a stub whose answers the case chooses. `assembly_test.go` drives
a real registry through the descriptor with stand-in modules for the database and
the mounter, which is what holds each callback's stage and the startup checks.
The route cases run on the real `mount.Wrap` through the shared `mounttest`
stand-in.
