# pkg/mail/smtp

The generic SMTP carrier, registered as `mail.smtp`. It is one `mail.Sender`:
it turns a rendered message into one SMTP transaction — one connection, `MAIL
FROM`, `RCPT TO`, `DATA` — calls the relay once and classifies what comes back
into the sending sentinels. A host writes this module's name into a command; it
declares the sending seam exclusively, so a command names exactly one carrier and
two at once fail in resolution.

The whole leaf package binds `github.com/wneessen/go-mail` v0.8.1 and can be
dropped without touching any other module. A host deploys it against any relay
that speaks submission — Aliyun DirectMail, Tencent SES, or a private relay —
by setting the host and port, so no vendor SDK beyond this one library enters the
build.

The API is the package's godoc, and the `mail.Message`, `mail.Receipt` and
sending sentinels in `pkg/mail`.

## Boundaries

- **The three connection postures are `starttls` (default), `implicit` and
  `none`; a value outside the set is a startup failure.** `starttls` connects in
  the clear and requires the server to advertise `STARTTLS`: it is mandatory, not
  opportunistic, so a server that does not offer it fails the send rather than
  falling back to plaintext. `implicit` encrypts from the first byte (SMTPS).
  `none` runs the whole session in the clear. Certificate verification is on
  unless `tls-skip-verify` is set, which exists only for a private relay with a
  self-signed certificate.
- **One send is one connection.** Each `Send` opens its own connection through
  the library's dial-and-send call; nothing is pooled or reused across calls.
- **Nothing is retried.** The SMTP transaction is not idempotent — a second one
  delivers and bills a second message — so no retry is configured on the library
  and a message whose `DedupeID` was seen before is answered with the earlier
  call's outcome instead of being sent again. The memory is bounded, lives in
  this process alone, and is shared with the other carriers through
  `pkg/internal/dedupe`.
- **Every failure is classified by the server's reply code and its class.** A
  `4xx` (transient negative) is `mail.ErrThrottled`; a `5xx` (permanent negative)
  is `mail.ErrRejected`. A connection lost in the middle of the message, a
  timeout, a network error and an ended context are all `mail.ErrUncertain`,
  because the send is not idempotent and a call that did not complete may still
  have delivered the message. The table is built from the class, not an
  enumerated set: any `4xx` or `5xx` the server can answer is covered, and a code
  outside both classes falls to the uncertain default. Only the numeric code
  reaches the error text; the server's own message text never does, because it
  can quote the request and the request carries the message.
- **A message the package refuses and a sender outside the allowed set never
  reach the relay.** `Message.Validate` is stateless and the allowed-set check is
  the carrier's: the sender — the message's own `From`, or the configured default
  when the message names none — is compared on its addr-spec against `{from} ∪
  from-allowed`, and one outside the set is `mail.ErrRejected` before any
  connection.
- **The credential is registered as a literal redaction rule.** The `password`
  item is sensitive and its value goes out through `Secrets` to the factory,
  which registers it through `pkg/internal/literal` before this package's `Open`
  builds the client and before any logger is bound. The subject, the bodies, the
  addresses and the credential reach no log, no error text and no metric.
- **No library type appears on the package's surface.** The vendor client is
  held behind an unexported interface, so a host that imports this package to
  register it sees the `mail.Sender` seam and never has to name the SDK.

## The fake server

`smtp_test.go` drives the real client against a hand-written SMTP server in
`fake_test.go`, with no third-party dependency. It answers the greeting and the
transaction commands, presents each of the three connection shapes (plaintext
with AUTH, a `tls.Server` upgrade for STARTTLS, a `tls.NewListener` for implicit
TLS), signs a throwaway certificate in process, and can answer `RCPT` with `550`
or drop the connection mid-`DATA` on demand. It records the envelope and the
`DATA` bytes so a case can assert against the landed message with the standard
library (`net/mail.ReadMessage`, `mime.ParseMediaType`, `multipart.NewReader`) as
the judge. Its own tests hold it to the two DATA-framing rules that are easy to
get wrong: a body line beginning with a dot is un-stuffed back to the original,
and the `CRLF-dot-CRLF` terminator is found however the bytes arrive.

## How to run it

From the repository root:

```
env -C pkg go build ./mail/smtp/...
env -C pkg go test ./mail/smtp/...
env -C pkg go test -race ./mail/smtp/...
env -C pkg golangci-lint run ./mail/smtp/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it loads no package and still exits 0.

The cases need no real relay: a case points the client at the loopback fake
server. `config_test.go` drives the real configuration loader for the required,
container-leaf and enum items, `assembly_test.go` drives a real registry through
the descriptor to `Init`, and `secrecy_test.go` holds the password out of every
error text and into the redaction rules.
