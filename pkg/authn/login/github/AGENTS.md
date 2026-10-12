# pkg/authn/login/github

The GitHub login method, registered as `authn.login.github`. It is one
`authn.LoginMethod` among the first factors: it runs an OAuth 2.0
authorization-code flow, exchanges the code for an access token, reads the
external identity and answers the engine a `Step`. It delivers the login-method
capability non-exclusively, so it stands beside every other method, and takes no
first-factor capability — a host writes this module's name into a command beside
the login engine and a gateway.

GitHub is not an OpenID provider and issues no ID token, so the identity is read
from the token: the numeric account number from `/user` is the subject, and an
address from `/user/emails` is taken only when one entry is primary and
verified. The address `/user` carries has no verified marker and is never used.
The access token is used once and discarded.

The method touches no HTTP server, session, token or flow record and writes no
log line; it reads the anti-forgery state and the PKCE verifier from the flow
record's method state, checks each exactly once on the callback, and says what is
next. The engine, not this package, logs the cause of an answer.

The API is the package's godoc, and the `LoginMethod`, `Start`, `FlowState`,
`StepInput`, `Step` and `Proof` shapes in `pkg/authn`.

## Boundaries

- **The state is the code's, and only its digest is kept.** `Begin` generates 32
  random bytes and uses their base64url as the state; the flow record's method
  state carries only the SHA-256 digest and the PKCE verifier. The callback's
  state is hashed and compared to the digest in constant time. The state is
  never the request's, and the challenge is always S256.
- **A callback the provider refuses ends before anything else is read.** A
  callback carrying the provider's `error` parameter is refused before any other
  parameter is touched, and a state that does not match is refused before the
  exchange, so neither reaches the token endpoint.
- **An exchange or a read the provider refuses is a refusal; an unreachable
  provider is an outage.** An exchange answered with an OAuth error and a read
  answered with a status that is not 200 are `StepRefused`; a transport failure
  and a body that does not decode are `StepUnavailable`. The two are counted
  apart, and the cause names neither the code, the token, the state nor the
  verifier.
- **The address is a clue and needs both markers.** Only an entry that is both
  primary and verified produces an address; an account with no such entry is
  proven with an empty address and `EmailVerified` false, and that is not an
  error. The address `/user` carries is never read, and no address is synthesized.
- **The access token is used once and dropped.** It travels in the reads'
  `Authorization` header, is never stored, never refreshed, and reaches no
  answer, no error text and no log. The client secret is registered as a literal
  redaction rule.
- **Every outbound request goes through `internal/egress`.** The restricted
  client is injected into the exchange with `oauth2.HTTPClient` and used for the
  two reads besides, so no request reaches a non-public address. The module's
  `New` builds no other client for these requests.
- **The callback address is built from the public base, never from the
  request.** `readConfig` reads `authn.gate`'s `mount.public-url` and
  `mount.base-path` and refuses an empty one, so the `redirect_uri` the provider
  is told is fixed at startup and never taken from a `Host` header.
- **The method serves `login`, `link` and `step-up`.** An `enroll` registers a
  factor and a provider's identity is not one, so a flow of that purpose is
  refused before the method reads or generates anything. An answer that is not a
  callback and a step the method never named are refused, so a flow record this
  method did not write cannot be walked into a proof.

## How to run it

From the module directory:

```
env -C pkg go build ./authn/login/github/...
env -C pkg go test ./authn/login/github/...
env -C pkg go test -race ./authn/login/github/...
env -C pkg golangci-lint run ./authn/login/github/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it loads no package and still exits 0.

The cases drive the method over a `githubMock`, a TLS server on the loopback
interface standing in for the token endpoint and the API endpoints, through the
restricted client pointed at a development address. `github_test.go` drives
`Begin` and `Answer` directly and reads the answer's kind; `config_test.go`
drives the real configuration loader over a body, with a stand-in gateway module
declaring the keys this module reads from `authn.gate`; `assembly_test.go` drives
a real registry through the descriptor.
