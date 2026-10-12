# pkg/authn/login/internal/wechat

The thin WeChat client the two WeChat login entries share. It is not a module
and registers nothing: it is the one place the token exchange, the error
sanitizing, the flow state and the subject rules live, so the website-application
entry (`authn/login/wechatweb`) and the official-account entry
(`authn/login/wechatmp`) each hold only their own credentials, scope and
authorization endpoint.

It binds no WeChat SDK. The reasons it is written here rather than bridged are in
the WeChat ADR; they are specific to this one provider and are not a precedent
for another.

## Where the authority is

- What a WeChat login is and why it is shaped this way:
  [`docs/design/modules/design-authn-login.md`](../../../../../docs/design/modules/design-authn-login.md),
  the WeChat section, and the login flow and its callback.
- The decision to write the client and split the two entries:
  [`docs/adr/adr-authn-wechat-2026-10-10.md`](../../../../../docs/adr/adr-authn-wechat-2026-10-10.md).
- The subject and alias rules — a union id, or the open-id key, and the upgrade
  of a binding — [`docs/adr/adr-authn-external-binding-2026-10-10.md`](../../../../../docs/adr/adr-authn-external-binding-2026-10-10.md).
- The restricted outbound client it sends through:
  `pkg/authn/internal/egress`.
- The API itself: the godoc in this package.

This file states only what neither of those states: the boundaries a change here
must not cross, and how to run it.

## Boundaries

- **A URL carrying the secret never leaves this package.** WeChat's token
  exchange puts the application secret in the query string, so the request URL is
  a credential. Every error this package returns drops the URL before it is
  handed back, and the `*url.Error` net/http returns — whose text carries the
  whole URL — is unwrapped rather than passed on. The client also wraps its
  transport in no other, so no span carries the URL either. A new outbound path
  holds the same invariant and its tests assert it for every failure it can
  produce.
- **The outbound client is the restricted one.** [New] builds it from
  `internal/egress`, which refuses a destination that is not a public https
  address; a test names its own client to reach a loopback server. There is no
  other transport.
- **A non-zero errcode is a refusal; anything else that is unreadable is an
  outage.** The token endpoint answers HTTP 200 with a non-zero `errcode` for a
  bad code or bad credentials, and that is a *RefusalError whose class the caller
  tells apart with `errors.Is`. A status other than 200, a body that is not a
  token response, a body that names no open id, and a transport failure are
  `ErrProviderFailed`, which the caller answers an outage rather than a refusal.
  Neither carries the provider's own words about anything the client sent.
- **The caller holds the state, this package only shapes it.** [State] draws a
  64-character value and keeps the digest as the private flow state; the value
  goes to the provider and comes back, and the callback compares it in constant
  time. This package has no store; the flow record is the login engine's.
- **The subject rules are here and nowhere else.** An identity with a union id is
  bound under it with the open-id key offered as an alias; one without is bound
  under the per-application open-id key. No email is synthesized, and the name is
  fixed, because this client calls no user-info endpoint.
- **It reads no configuration and holds no credential of its own.** The two
  entries build it from their own decoded configuration and register the secret
  as a literal redaction rule before any logger is bound.

## How to run it

From the module directory:

```
env -C pkg go build ./authn/login/internal/wechat/...
env -C pkg go test ./authn/login/internal/wechat/...
env -C pkg go test -race ./authn/login/internal/wechat/...
env -C pkg golangci-lint run ./authn/login/internal/wechat/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it loads no package and still exits 0.

A case drives the client against a loopback TLS server — `httptest` started with
`StartTLS`, reached through `egress.New` with `AllowLoopback` and the server's own
certificate in `RootCAs` — and asserts the request shape, each failure the token
endpoint can produce, and that no failure carries the secret. A transport failure
is driven by injecting a client whose transport returns a `*url.Error`, so the
URL-stripping is asserted without a network.
