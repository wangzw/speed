# pkg/authn/emailcode

The optional email channel of the authentication capability, registered as
`authn.emailcode`. It adapts the root package's `EmailCodeSender` and
`EmailResetCodeSender` to the top-level `pkg/mail` capability: it renders a
verification code into the `authn.email.verify` messages or a password reset
code into the `authn.email.reset` messages, and hands the finished message to a
mail carrier. It delivers `authn.EmailCodeSender` exclusively, and
`authn.EmailResetCodeSender` when the compiled-in catalog carries the reset
messages, so a command tree names this module or leaves the email faces
unavailable; the host may instead implement the seams itself and carry no mail
capability at all.

The module declares no configuration item. Everything it needs to send is the
mail carrier's own section (`mail.smtp` in this repository's carrier), and its
messages are compiled in as an `i18n.Catalog` resource with zh-CN and en-US.

The API is the package's godoc. Read `SendCode`'s comment before changing what
the seam does.

## Boundaries

- **It follows the mail carrier selection.** `Prepare` reads `mail.carrier`. A
  `none` selection states `StateDisabled` with that reason rather than reporting
  the missing `mail.Sender` as a startup fault, so a deployment that deliberately
  sends no mail loses this sender along with the rest of the capability.
- **The reset channel is offered exactly when the catalog carries it.** `Module`
  reads the embedded catalog at registration time; the three
  `authn.email.reset.subject`, `body_text` and `body_html` leaves must be present
  and non-empty in every locale it carries for `EmailResetCodeSender` to enter
  `Provides`. A build whose catalog lacks one offers only the verifying channel
  and starts normally (`provisions`, `catalogCarriesReset`,
  `TestTheResetSeamIsProvidedExactlyWhenTheCatalogCarriesIt`). `New` checks the
  reset messages too when the channel is offered, so a localizer that does not
  carry the catalog fails the startup rather than the first reset send.
- **The code is drawn elsewhere and only passes through here.** The login
  module draws it, keeps the pending record and compares the answer; this
  package turns one `SendCode` or `SendResetCode` call into one `mail.Message`.
  It holds no budget, no record, no attempt count and no retry, because those
  belong to the consumer that owns the code.
- **The code, the recipient and the bodies reach no log, no error text and no
  metric.** Each send logs one outcome line with the carrier's receipt and its
  duration; the line carries neither the code nor the address nor the message.
  The error a caller receives is the carrier's and carries no code. A test pins
  the two paths that could leak it and the two outcome lines.
- **A carrier error is passed through as classified, and uncertainty is named.**
  `mail.ErrThrottled` and `mail.ErrRejected` reach the caller unchanged; a
  `mail.ErrUncertain` reaches it wrapped in `authn.ErrEmailUncertain`, with the
  mail sentinel still inside. The caller collapses all of them into one 503 so a
  client cannot tell whether the message left. `CertifiesNotSent` publishes the
  other half of that classification: it reports true only for the two definite
  failures, and the caller reads it to decide whether a failed send gives back the
  budget its attempt reserved. A sender that does not publish it is treated as
  uncertain.
- **The messages are checked at startup, not at the first request.** `New`
  renders every message in every locale the embedded catalog carries — the
  verifying messages always, the reset messages when that channel is offered —
  and fails with the missing key and locale when one is absent, so a catalog
  edited badly stops the process instead of sending a message that is only its
  key.
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
