# pkg/sms/twilio

The Twilio carrier, registered as `sms.twilio`. It delivers two seams over one
vendor account. The sending seam lays a resolved message out the way the
Messages API wants it — the destination, the resolved content template, the
ordered parameter values as the template's positional content variables, and
the identity the message goes out from — calls the carrier once and classifies
the answer into the sending sentinels. The management seam — `sms.Provider` —
creates a `twilio/text` content template over the Content API, answers that this
carrier has no per-template review, and removes content. A host writes this
module's name into a command; it declares both seams exclusively, so a command
names exactly one carrier and two at once fail in resolution.

## Selection

`sms.carrier: twilio` selects this carrier; another name, or `none`, stands it
down with a reason, and a name no compiled carrier answers to is an error. With
no selection, `account-sid`, `auth-token`, `identities` and `default-identity`
all empty or absent is unconfigured and it stands down; some present and some
missing is half-configured and `Prepare` fails naming the missing items; all
present is configured and it runs. The two base URLs have defaults and are not
identity items. Two configured carriers with no selection is an exclusive
violation naming both modules. The log carrier is the fallback and runs only
when this carrier, or any other configured carrier, did not.

The carrier holds no template set of its own: which template a message goes out
under is decided by the template registry, and `Send` is handed a `sms.Resolved`
that already carries the content SID and the ordered values. The management seam
is the only place a vendor content SID is written or removed.

The whole leaf package binds `github.com/twilio/twilio-go` and can be dropped
without touching any other module.

The API is the package's godoc, the `sms.Resolved`, `sms.Receipt`,
`sms.Submission`, `sms.Approval` and `sms.CarrierModel` types and the sending
sentinels in `pkg/sms`.

## Boundaries

- **`Send` lays out what it is handed, and decides nothing about the template.**
  A `sms.Resolved` carries the destination, the content SID and the ordered
  values; this carrier puts `Code` in `ContentSid`, numbers `Ordered` into
  `ContentVariables` as `"1"`, `"2"`, … and sends no `Body` beside the content
  template. A non-zero `ValidFor` becomes `ValidityPeriod` as whole seconds,
  raised to one below a second and clamped to the vendor's 36000-second bound,
  the same direction the AWS carrier takes for `TimeToLive`, so one request means
  one lifetime on either deployment. A resolved message with no code is `sms.ErrRejected`: this carrier
  declares `Content{TemplateCode}` and cannot send a body-only template, and the
  registry refuses such a template for this carrier before a message is built. A
  message the registry would refuse never reaches here, so an uncovered template
  or an empty locale is not this package's to catch.
- **The send identity is chosen in `Send`.** An empty `Resolved.Originator` takes
  the configured `default-identity`; a name the deployment does not declare is
  `sms.ErrRejected` and is refused before the vendor is called. A
  `messaging-service` identity sends its value as `MessagingServiceSid`; any
  other identity sends its value as `From`. An identity's `"from"` attribute
  wins over both — beside a messaging service it pins one sender of the pool.
  `Open` refuses a configuration whose `default-identity` names no declared
  identity, whose identity names repeat, whose kind is outside the shared
  vocabulary, or whose value is empty.
- **The declared countries are carried, not compared.** `Identity.Countries`
  reaches the carrier declaration (`Model`) unchanged, but `Send` does not refuse
  a destination on it: this codebase fixes no form for a country value, and
  comparing an opaque string against an E.164 number would refuse messages for a
  reason an operator cannot predict. `sms.ErrUnroutable` is therefore reserved
  here for a vendor answer, and this carrier maps no vendor code to it yet.
- **The management seam creates content, not reviewed templates.** `Offer`
  rewrites the submission's `${name}` placeholders into the Content API's
  double-brace positional ones (`{{1}}`, `{{2}}`, …) numbered by the declared
  parameter order, and files the content under the locale's primary tag,
  lower-cased, which must be two lowercase ASCII letters: `zh-CN` becomes `zh`,
  and `fil` is `sms.ErrInvalidConfig` before any vendor call. A parameter
  declared twice, a placeholder naming an undeclared parameter and an unclosed
  placeholder are `sms.ErrRejected`, refused before the vendor is reached.
  `Approval` answers `sms.ApprovalNotRequired` and makes no call: there is no
  per-template review. `Retire` deletes the content and treats a 404 as already
  gone. `Offer` answers the HX content SID.
- **The error table is the HTTP status.** 429 is `sms.ErrThrottled`, another
  synchronous 4xx is `sms.ErrRejected`, and 5xx, a timeout, a network error and
  an ended context are `sms.ErrUncertain`. Only the status and the vendor's
  numeric code reach the error text; the vendor's message text does not, because
  it can quote the request. A `2xx` answer has taken the message: a delivery
  status or an error code it carries describes the delivery that follows and is
  never read as a synchronous refusal.
- **The vendor is called at most once per identifier, and nothing is retried.**
  The vendor interface is not idempotent, so a second call would deliver the
  message twice and bill it twice — or create a second content nobody asked for.
  A message whose `DedupeID` was seen before is answered with the earlier call's
  outcome instead of calling again; the memory is bounded, lives in this process
  alone, and is shared with the other carriers through `pkg/internal/dedupe`.
- **The caller's context is checked before a call, and cannot cancel one in
  flight.** The SDK's request path takes no context: an ended context is
  `sms.ErrUncertain` without reaching the vendor, while a context that ends
  during a call leaves it running to the SDK's own timeout. That timeout is the
  default ten seconds and its failure is classified as uncertain like any other.
  The SDK's own retry is not configured at all: its generated services make one
  request per call, and the client does not follow redirects.
- **The two access points are configurable, and the SDK offers no setter.** The
  generated services hard-code `https://api.twilio.com` and
  `https://content.twilio.com`, so `api-base-url` and `content-base-url` are
  applied by a wrapper around the SDK's base client: it rewrites the host of a
  request aimed at one of the two public endpoints and delegates credentials,
  form encoding and error decoding to the SDK's own client. It is what points
  the carrier at a gateway, and what lets the cases drive both APIs over loopback
  servers.
- **The auth token, the rendered body and the parameter values reach no log, no
  error text and no metric.** The token is registered as a literal redaction
  rule by the factory, from the `Secrets` hook, through `pkg/internal/literal`,
  before this package's `Open` builds the client and before any logger is bound.
  The seams do not echo the vendor's own message text into what they return.
  The SDK's client refuses a credential holding a character outside
  `[A-Za-z0-9]` by answering its own HTTP-400 error, which this module classifies
  as `sms.ErrRejected`; Twilio's account SIDs and auth tokens are alphanumeric.
- **Registration and delivery receipts are out of scope.** The identities and
  the countries they serve are declarations; US 10DLC brands and campaigns,
  toll-free verification and short codes are managed in the vendor's portal, and
  this package neither reads nor tracks their state. The final delivery status
  arrives out of band (`AsyncReceipt`): the synchronous answer carries only the
  message SID, and acceptance is not delivery.
- **No vendor type appears on the package's surface.** The package exports its
  module name, its config namespace and its module descriptor; the SDK's
  services and error types stay behind unexported fields and functions, so a
  host that imports this package to register it never has to name the SDK.

## How to run it

From the module directory:

```
env -C pkg go build ./sms/twilio/...
env -C pkg go test ./sms/twilio/...
env -C pkg go test -race ./sms/twilio/...
env -C pkg golangci-lint run ./sms/twilio/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it loads no package and still exits 0.

The cases need no real credentials: a case builds the sender with both access
points on loopback `httptest` servers and drives `Send`, `Offer`, `Approval` and
`Retire` through them, so the request shapes, the rewriting, the language
derivation and the classification are held against the real SDK.
`config_test.go` drives the real configuration loader for the identity items and their unconfigured or half-configured stance,
and `assembly_test.go` drives a real registry through the descriptor to `Init`.
