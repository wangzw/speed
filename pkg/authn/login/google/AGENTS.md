# pkg/authn/login/google

The Google login method, registered as `authn.login.google`. It is one
`authn.LoginMethod` among the first factors: it proves an external identity by
an OpenID Connect authorization-code flow against `https://accounts.google.com`
and answers the engine a `Step`. It delivers the login-method capability
non-exclusively, so it stands beside every other method, and requires no
capability of another module — the provider is reached over the restricted
egress client this module builds itself.

The method is this package's whole surface. In `Begin` it draws the state, the
PKCE verifier and the nonce, keeps them in the flow's private state and answers
the authorization URL; in `Answer` it checks the redirect, exchanges the code
and verifies the identity token. It touches no HTTP route, session, token or flow
record; the engine logs the cause of an answer, and the outbound requests are
recorded by the logging transport the client carries (see the egress boundary
below).

The API is the package's godoc, and the `LoginMethod`, `Start`, `FlowState`,
`StepInput`, `Step` and `Proof` shapes in `pkg/authn`.

## Boundaries

- **The libraries stay in this package.** `go-oidc` and `x/oauth2` are named in
  the non-test files here and nowhere on the root package's surface: the engine
  gets a `Step` and a `Proof`, never a library type. `oauth2.Config`,
  `oidc.Provider` and `oidc.IDToken` do not leave this package.
- **The state, the PKCE verifier and the nonce are the method's own.** `Begin`
  draws all three, keeps the verifier and the nonce in the flow's private state
  and stores the state only as its SHA-256; the redirect compares the state's
  digest in constant time. The flow record's read permission is a credential
  here, because the record holds the verifier and the nonce.
- **The verification the library does not do is done here.** go-oidc verifies the
  signature, the issuer, the audience and the expiry and handles Google's
  scheme-less issuer; the nonce, the authorized party when the token carries more
  than one audience, and the hosted domain are checked here, and no userinfo
  request is made. A check that the library is relied on to make is a defect:
  the negative vectors in `method_test.go` are the record of which side holds
  which.
- **Every outbound request goes through `internal/egress`, injected through
  `oidc.ClientContext`.** Discovery, the key set and the token endpoint all use
  the same restricted client, so each is https-only, at a public address, and
  bounded by the deadline of the context the engine hands in. A call that does
  not inject the client would use the library's own client and is a defect; the
  test serves a loopback provider and the production client refuses it, so an
  uninjected call fails the suite.
- **The client carries the trace and logging transports.** `New` wraps the
  restricted client's transport in `observability.Transport` and, inside it,
  `log.Transport`, so every discovery, key-set and token request carries the
  call's span across the hop and leaves one record, success or failure, naming
  the method, host, path, status, outcome and duration. The record carries
  neither the query nor a body: the query is where an authorization code, a
  token and a state travel. `outbound_log_test.go` drives a request through the
  client the product assembles and holds both what the record names and what it
  must not.
- **Discovery is lazy, cached and fails open into an outage, not into a
  refusal.** The provider is discovered on the first call that needs it and
  cached; a discovery that fails ends that call with `StepUnavailable` and is not
  cached, so the next call retries and the method answers an outage until the
  provider is reachable. A startup that cannot discover the provider is not a
  failure.
- **An outage is an outage and a refusal is a refusal.** A transport failure, a
  context that ended, a token endpoint's server error and a token response with
  no identity token are `StepUnavailable`; a state that does not match, a token
  that does not verify, a nonce that does not match and a provider that reported
  the request was refused are `StepRefused`. The methods do not become a
  `StepRefused` under an outage. No cause, in the log as on the wire, names an
  authorization code, a token, a state, a nonce or a client secret. The one place
  the two cannot be told apart is the library's key-set fetch: it runs under the
  provider's own context, not the call's, and its failure is flattened into the
  same error as a signature failure, so a key set that could not be reached is
  answered as a refusal. Discovery and the token exchange are reached with the
  call's own context and are where a provider outage is told apart; the test that
  points the key set at an unreachable address records the refusal.
- **The method serves login, link and step-up.** A Google identity proves which
  external identity a caller holds: a login resolves an account from it, a link
  binds it to the flow's account, and a step-up re-proves an account whose first
  factor is this one. An enrollment registers a factor, which an external
  identity does not prove, so it is refused before the method draws anything. An
  answer that is not a redirect back, and a step the method did not name, are
  refused, so a flow record this method did not write cannot be walked into a
  proof.
- **The client secret is registered as a log redaction rule.** The module takes
  the logging capability as an optional requirement and registers the secret's
  value as a literal rule in `New`, before any logger binds an attribute, so the
  secret is masked wherever it turns up in a log line. Marking the item
  `Sensitive` does not do this; it keeps the value out of error texts and help
  output. An assembly with no logging has nothing to register it with and
  registers nothing.
- **The redirect target is built from the gateway's configuration.** It is the
  gateway's `mount.public-url`, the `mount.base-path` (the declared default when
  the gateway set none) and this provider's callback path, which is the method's
  name. The method reads those two keys in `Prepare` and fails the start when the
  public address is empty, so a deployment that configured no address is told at
  startup rather than at the first login.

## How to run it

From the module directory:

```
env -C pkg go build ./authn/login/google/...
env -C pkg go test ./authn/login/google/...
env -C pkg go test -race ./authn/login/google/...
env -C pkg golangci-lint run ./authn/login/google/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it loads no package and still exits 0.

`harness_test.go` serves a mock OpenID Connect provider over TLS on loopback —
discovery, an authorization endpoint that insists on a PKCE challenge, a token
endpoint that checks the verifier and a key set — and wires the method to it
through the same restricted client the product builds, with loopback allowed.
`method_test.go` drives `Begin` and `Answer` against it: the positive flow, and
each negative vector (a missing or changed state, a mismatched issuer, a missing
code, a refused verifier, a foreign audience, an expired token, a missing or
mismatched nonce, a mismatched authorized party, `alg=none` and HS256, a
mismatched hosted domain, an unverified address, and the outages).
`assembly_test.go` drives a real registry through the descriptor.
