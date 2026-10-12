# pkg/authn/sso/oidc

The enterprise OIDC login method, registered as `authn.sso.oidc`. It is one
`authn.LoginMethod` among the first factors: it proves an external identity by
an OpenID Connect authorization-code flow against an identity provider a tenant
registered, and answers the engine a `Step`. It delivers the login-method
capability non-exclusively, so it stands beside every other method, and it
requires the enterprise provider register (`authn.SSOProviders`) and the account
module (`authn.Accounts`).

The method is this package's whole surface. In `Begin` it reads the provider the
start input names, draws the state, the PKCE verifier and the nonce, keeps them
in the flow's private state and answers the authorization URL; in `Answer` it
checks the redirect, exchanges the authorization code and verifies the identity
token. It touches no HTTP route, session, token or flow record: account
resolution is the engine's.

The API is the package's godoc, and the `LoginMethod`, `Start`, `FlowState`,
`StepInput`, `Step`, `Proof`, `SSOProvider` and `SSOProviders` shapes in
`pkg/authn`.

## Boundaries

- **The libraries stay in this package.** `go-oidc` and `x/oauth2` are named in
  the non-test files here and nowhere on the root package's surface: the engine
  gets a `Step` and a `Proof`, never a library type. `oauth2.Config`,
  `oidc.Provider` and `oidc.IDToken` do not leave this package.
- **The provider is data, read from the register.** `Begin` reads the provider
  by the identifier in the start input's `provider` field, and its secret
  through `SSOProviders.Secret` alone. The method resolves no account and
  creates none; it reads no table of its own. The secret never enters a log, an
  error, an event or a metric.
- **The state, the PKCE verifier and the nonce are the method's own.** `Begin`
  draws all three, keeps the verifier and the nonce in the flow's private state
  and stores the state only as its SHA-256; the redirect compares the state's
  digest in constant time. The private state also carries the provider
  identifier, which is what `Answer` exchanges against, because `Answer` is
  handed the flow and not the callback path.
- **The verification the library does not do is done here.** go-oidc verifies
  the signature, the issuer, the audience and the expiry; the nonce, the
  authorized party when the token carries more than one audience, the
  non-empty subject and the redirect's RFC 9207 `iss` parameter are checked
  here, and no userinfo request is made. The signature algorithms the verifier
  admits are the provider's configured `algs` (defaulting to RS256 and ES256),
  so `alg=none` and an `HS*` algorithm are refused. A check the library is
  relied on to make is a defect: the negative vectors in `method_test.go` are
  the record of which side holds which.
- **Every outbound request goes through `internal/egress`, injected through
  `oidc.ClientContext`.** Discovery, the key set and the token endpoint all use
  the same restricted client, so each is https-only, at a public address, and
  bounded by the deadline of the context the engine hands in (and, per call, by
  `authn.sso.egress-timeout`). A call that does not inject the client would use
  the library's own client and is a defect. The client is built here, from the
  SSO module's `allow-private-egress` developer relaxation.
- **The provider is cached per identity provider and version.** The discovered
  provider and its OAuth client are held under the provider's identifier, keyed
  by its stored `version`, with the expiry of `authn.sso.provider-cache-ttl`;
  an `authn.idp.changed` event for that provider drops the entry. A discovery
  that fails is not cached, ends that provider's calls with `StepUnavailable`
  and touches no other provider.
- **An outage is an outage and a refusal is a refusal.** A provider row that is
  absent, disabled or of another protocol, a discovery failure, a token
  endpoint's server error and a token response with no identity token are
  `StepUnavailable`; a state that does not match, a token that does not verify,
  a nonce that does not match, an authorized party that is not this client, an
  empty subject and a token endpoint that refused the code are `StepRefused`.
  The one place the two cannot be told apart is the library's key-set fetch: it
  runs under the provider's own context, not the call's, and its failure is
  flattened into the same error as a signature failure, so a key set that could
  not be reached is answered as a refusal. Discovery and the token exchange are
  reached with the call's own context and are where a provider outage is told
  apart; the test that points the key set at an unreachable address records the
  refusal.
- **A refusal records an outcome.** Every refusal of an identity token's
  judgement increments `authn.sso.assertions` under `protocol=oidc` and one
  `outcome` of the closed set, and writes a best-effort `authn.sso.reject` audit
  row naming the provider. The library flattens a bad signature, a foreign
  audience, a foreign issuer and an expiry into one error, so `classifyVerify`
  reads the token's own unverified claims to label the metric; that read decides
  nothing. A provider that reported the request was refused (the user declined)
  is neither an assertion nor an outage and records no outcome.
- **The method serves login, link and step-up.** An enterprise identity proves
  which external identity a caller holds: a login resolves an account from it, a
  link binds it to the flow's account, and a step-up re-proves an account whose
  first factor is this one. An enrollment registers a factor, which an external
  identity does not prove. A start input that names no provider is refused
  before anything is drawn.
- **The redirect target is built from the gateway's configuration.** It is the
  gateway's `mount.public-url`, the `mount.base-path` (the declared default when
  the gateway set none) and the provider's callback path (`/callback/<id>`). The
  method reads those keys in `Prepare` and fails the start when the public
  address is empty.

## How to run it

From the module directory:

```
env -C pkg go build ./authn/sso/oidc/...
env -C pkg go test ./authn/sso/oidc/...
env -C pkg go test -race ./authn/sso/oidc/...
env -C pkg golangci-lint run ./authn/sso/oidc/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it loads no package and still exits 0.

`harness_test.go` serves a mock OpenID Connect provider over TLS on loopback —
discovery, an authorization endpoint that insists on a PKCE challenge, a token
endpoint that checks the verifier and a key set — and wires the method to it
through the same restricted client the product builds, with loopback allowed.
`method_test.go` drives `Begin` and `Answer` against it: the positive flow, and
each negative vector (a mismatched state, an `iss` parameter that is not the
configured issuer verbatim, a token issuer, an audience and a subject the
verifier refuses, an expired token, a missing or changed nonce, a missing client
id in the audience, a mismatched authorized party, `alg=none` and HS256, a
missing code, a refused verifier, a provider error, a missing or disabled
provider, and the outages, including one provider's discovery failure leaving
another unaffected). `cache_test.go` drives the provider cache: a hit, a version
change, an `idp.changed` drop and an expiry. `claims_test.go` holds the attribute
mapping and the refusal classifier. `assembly_test.go` drives a real registry
through the descriptor.
