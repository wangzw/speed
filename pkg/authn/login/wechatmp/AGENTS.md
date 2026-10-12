# pkg/authn/login/wechatmp

The WeChat official-account login method, registered as `authn.login.wechat-mp`
and reporting the name `wechat.mp`. It is one `authn.LoginMethod` among the first
factors: it sends the user agent to WeChat's web-authorization endpoint and
proves a WeChat identity from the code the provider hands back. It delivers the
login-method capability non-exclusively, so it stands beside every other method,
and a host writes this module's name into a command alongside the login engine,
providing this module's two configuration items.

It shares the thin client — the token exchange, the error sanitizing, the flow
state and the subject rules — with the website-application method under
`login/internal/wechat`. It is a separate module with its own credentials because
WeChat issues the website application and the official account their own. Its
authorization scope is `snsapi_userinfo` and its authorization address ends with
the `#wechat_redirect` fragment WeChat's web authorization requires.

The API is the package's godoc, and the `LoginMethod`, `Start`, `FlowState`,
`StepInput`, `Step` and `Proof` shapes in `pkg/authn`.

## Boundaries

- **The two configuration items are required and the module is not optional.**
  `app-id` and `app-secret` are required in the schema, so a command that names
  the module without them fails at startup with `ErrInvalidConfig`. Prepare and
  New both read and validate the module's section and the gateway's public
  address through one function, so the two see one configuration; an empty
  public address is `ErrInvalidConfig` too. There is no joint check with the
  website-application module: the two are independent, and a host that uses one
  does not configure the other.
- **The authorization endpoint is the web-authorization one and the address ends
  with `#wechat_redirect`.** The scope is `snsapi_userinfo`. Both facts are
  carried by this package's own constants and the shared client's authorize-URL
  builder; the other entry uses its own endpoint and scope.
- **The callback address is built from the gateway's public address and base
  path, never from the request.** `redirect_uri` is
  `public-url + base-path + /callback/wechat.mp`, and the provider is told to
  return there; the callback route carries the method's own name, which the
  engine records as the flow's provider.
- **The method touches no HTTP, session, token or flow record.** It answers a
  `Step`; the engine writes the flow record, keeps the private state, and moves
  the record to `processing` before the callback is answered, so the code is
  traded at most once. The method reads no cookie.
- **`Begin` reads nothing from the start request.** It draws a fresh state and
  answers a redirect; the state is the CSRF defence of a protocol that has no
  PKCE and no nonce, and the flow binding is the engine's second defence.
- **The callback is refused on a wrong or missing state, a wrong or missing
  code, or a cancelled authorization; a provider that cannot be reached is an
  outage.** A refusal is `StepRefused`, whatever the provider's own cause is,
  distinguished only in the log (a rejected code from rejected credentials); a
  status other than 200, an unreadable body and a transport failure are
  `StepUnavailable`. No cause carries the request URL or the secret.
- **The secret is registered as a literal redaction rule in `New`, before any
  logger this module would take.** An assembly with no logging capability
  registers nothing.
- **The method serves `login`, `link` and `step-up`, and refuses everything
  else.** A WeChat identity is a first factor, so it serves a login, a link that
  binds it to an account already logged in, and a step-up that falls back to a
  fresh first factor. An enrollment registers a second factor, which a redirect
  to a provider is not.

## How to run it

From the module directory:

```
env -C pkg go build ./authn/login/wechatmp/...
env -C pkg go test ./authn/login/wechatmp/...
env -C pkg go test -race ./authn/login/wechatmp/...
env -C pkg golangci-lint run ./authn/login/wechatmp/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it loads no package and still exits 0.

The cases drive `Begin` and `Answer` directly against a loopback TLS server the
client reaches through `egress.New` with `AllowLoopback` — the authorization
address and its `#wechat_redirect` fragment, the callback's proof, a wrong or
missing state, an `errcode`, a non-200 status, and the secret staying out of
every cause. `assembly_test.go` drives a real registry through the descriptor
with stand-ins for the configuration reader and the logging capability, and
asserts that the method is delivered, the callback address is built, and the
secret is registered as a redaction rule.
