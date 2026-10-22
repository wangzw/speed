# pkg/authn/emailcode

The optional email channel of the authentication capability, registered as
`authn.emailcode`. It adapts the root package's `EmailCodeSender` to the
top-level `pkg/mail` capability: it renders a verification code into the
`authn.email.verify` messages and hands the finished message to a mail carrier.
It delivers `authn.EmailCodeSender` exclusively, so a command tree names this
module or leaves the email self-service face unavailable; the host may instead
implement the seam itself and carry no mail capability at all.

The module declares no configuration item. Everything it needs to send is the
mail carrier's own section (`mail.smtp` in this repository's carrier), and its
messages are compiled in as an `i18n.Catalog` resource with zh-CN and en-US.

The API is the package's godoc. Read `SendCode`'s comment before changing what
the seam does.

## Boundaries

- **The code is drawn elsewhere and only passes through here.** The login
  module draws it, keeps the pending record and compares the answer; this
  package turns one `SendCode` call into one `mail.Message`. It holds no budget,
  no record, no attempt count and no retry, because those belong to the consumer
  that owns the code.
- **The code reaches no log, no error text and no metric.** Neither the message
  nor this package logs anything; the error a caller receives is the carrier's
  and carries no code. A test pins the two paths that could leak it.
- **A carrier error is passed through exactly as classified.** `mail.ErrThrottled`,
  `mail.ErrRejected` and `mail.ErrUncertain` reach the caller unchanged, and the
  caller collapses all of them into one 503 so a client cannot tell whether the
  message left. This package adds no sentinel of its own.
- **The messages are checked at startup, not at the first request.** `New`
  renders every message in every locale the embedded catalog carries and fails
  with the missing key and locale when one is absent, so a catalog edited badly
  stops the process instead of sending a message that is only its key.
- **A language the catalog does not carry falls back, it is not refused.** The
  requested tag is tried as written, then by primary subtag (`zh-HK` to zh-CN,
  `en-GB` to en-US), then English, so a recipient in a language this deployment
  does not translate still receives a usable code.
- **The module requires `mail.Sender` and resolves `i18n.Localizer` in `New`.**
  The mail capability's carrier owns the exclusive claim on `mail.Sender`, so a
  command names exactly one carrier; the localizer is resolved where a failure
  can name the message it cannot render. An assembly with no i18n engine fails to
  construct this module rather than sending keys.
- **No message carries a `DedupeID`.** Every send is a freshly drawn code, so a
  dedupe identifier would suppress a legitimate second message; the carrier
  delivers each call once and the login module's resend window is what bounds
  repeats.
