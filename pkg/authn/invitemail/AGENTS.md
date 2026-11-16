# pkg/authn/invitemail

The optional email channel of the invitation capability, registered as
`authn.invitemail`. It adapts the root package's `InviteNotifier` to the
top-level `pkg/mail` capability: it renders one invitation notice — the tenant
the invitation grants, the inviter's display name, the acceptance link and the
invitation's deadline — into the `authn.invitemail` messages, and hands the
finished message to a mail carrier. The tenant the notice names is the tenant's
name, which the invitation module resolves before it calls the seam; this
package renders `InviteNotice.Tenant` as given and never derives a name of its
own. It delivers
`authn.InviteNotifier` exclusively, so a command tree names this module or
supplies its own notifier; two providers in one run fail in resolution.

The API is the package's godoc. Read `Notify`'s comment and the `InviteNotice`
and `InviteDelivery` comments in `pkg/authn/invite_notice.go` before changing
what the seam does.

## Boundaries

- **The invitation module owns the invitation; this package owns the notice.**
  The invitee's token is drawn, stored as a digest and handed out once by
  `authn.invite`. This package holds no invitation row, no state machine and no
  retry: one `Notify` call becomes one `mail.Message`, and a failed delivery
  does not undo the invitation. The caller logs the failure and still answers
  the issue with the one-time link.
- **`authn.invitemail.accept-base-url` is the origin the acceptance landing is
  served at.** The notice's link is `<accept-base-url>/invitations/<token>`: the
  public landing the demo host serves the invitation page on
  (`/invitations/:token`), not the API route. The module appends that path and
  escapes the token as one segment, so no configuration can change the route's
  shape. The item has to be an absolute http or https URL with a host and no
  query or fragment; an invalid value stops the startup in `Prepare`, naming
  the item and never repeating the value. A trailing slash is normalized away,
  so the join cannot write a doubled slash.
- **An empty value is a legal, not-configured deployment.** The module stays
  enabled, reaches no carrier and reports `InviteNotConfigured` with a nil
  error. A deployment that sends no mail should leave the item empty rather than
  omit the module: the invitation module treats an absent notifier as the same
  outcome.
- **The log carrier is not a delivery channel for an invitation.** When the
  mail capability's sender answers `mail.log`'s carrier name, `Notify` reports
  `InviteNotConfigured` without rendering or handing over a message. The log
  carrier writes to the process log and reaches no recipient, so treating it as
  a send would withhold the acceptance link behind a line only the deployment
  can read. The invitation answer must instead carry the token for the issuer to
  pass on. Verification codes and every other message keep the log carrier's
  own behaviour; the rule lives here, in the invitation channel, alone.
- **A run that switches the mail capability off stands this module down.**
  `Prepare` reads `mail.carrier` through `mail.OffBySelection` before it reads
  this module's own section: with `mail.carrier: none` no sending provider
  exists, so the module states `StateDisabled` with the selection's reason
  instead of failing the assembly over an unmet requirement. The base URL is
  not judged on a run that sends nothing.
- **The notice names the inviter when it has one.** `InviteNotice.Inviter` is
  the display name the invitation module read from the issuing account, never
  the account identifier. A notice that carries one renders the body variant
  interpolating `{{inviter}}`; an invitation a host command issued without an
  account renders the impersonal variant, so the message never says " invited
  you" with nobody in front of it.
- **The HTML body interpolates escaped values.** The tenant name, the inviter's
  display name and the acceptance link reach the HTML body and the HTML expiry
  line HTML-escaped, so free text a person typed cannot introduce markup. The
  plain-text body and the subject carry the same values as written, because
  neither is parsed as HTML.
- **The deadline is a readable UTC date and time.** `InviteNotice.ExpiresAt` is
  the one deadline the invitation has. The message's own time segment renders it
  with a layout that ends in `UTC`, and `Localizer.Translate` formats with no
  time zone, which reads as UTC: the recipient may have no account and no time
  zone, and the request's time zone is never consulted. The date goes in a
  separate expiry line so the zero time means the caller does not know it and the
  sentence is omitted rather than an invented instant rendered.
- **The language is the request's, and a language the catalog lacks falls
  back.** The requested tag is tried as written, then by primary subtag
  (`zh-HK` to zh-CN, `en-GB` to en-US), then English, so a recipient in a
  language this deployment does not translate still receives a usable link.
- **The token, the recipient and the bodies reach no log, no error text and no
  metric.** The token is the whole credential of acceptance and the link embeds
  it. Each send logs one outcome line with the carrier's receipt and its
  duration; the line carries no token, address, tenant or body. The error a
  caller receives is the carrier's and carries no token.
- **A carrier error is passed through as classified.** `mail.ErrThrottled`,
  `mail.ErrRejected` and `mail.ErrUncertain` reach the caller unchanged, so the
  invitation module decides how to record the outcome. This package adds no
  classification of its own.
- **`InviteNotice.Tenant` is the name, never the internal identifier.** The
  subject and both bodies interpolate `{{tenant}}` from that field, so the
  recipient reads the same name a request surface addresses the tenant by. The
  internal identifier is not a name this package knows or can produce: a notice
  that arrives with one is a defect at the caller, and the seam's contract says
  the field carries the name.
- **The messages are checked at startup, not at the first request.** `New`
  renders every message in every locale the embedded catalog carries and fails
  with the missing key and locale when one is absent, so a catalog edited badly
  stops the process instead of sending a message that is only its key.
- **No message carries a `DedupeID`.** Each invitation issue draws a new token
  and a re-issue supersedes the old invitation, so a dedupe identifier would
  suppress a legitimate notice and add nothing: the carrier delivers each call
  once.

## Demo assembly

`starters/demo/server/capabilities.go` compiles this module into every build and
names it for `serve`. The review configuration sets
`authn.invitemail.accept-base-url` to `http://localhost:8080` and selects
`mail.carrier: log`. Because the log carrier is not a delivery channel for an
invitation, every issue in that assembly reports `not_configured` and the
console's one-time panel hands out the acceptance link; no notice is written to
the process log. `starters/demo/server/local.yaml.example` carries the same two
values for the local loop.

## Tests

`invitemail_test.go` holds the descriptor shape, the stand-down under
`mail.carrier: none`, the not-configured outcome, the origin/landing join, the
startup validation and normalization, both languages' rendered output, the
carrier failure, the locale fallback, the log's freedom from the token, the
recipient and the tenant, and the catalog's key parity.
