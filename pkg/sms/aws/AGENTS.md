# pkg/sms/aws

The AWS End User Messaging SMS carrier, registered as `sms.aws`. It delivers two
seams over one vendor client, through the Pinpoint SMS Voice v2 API. The sending
seam sends the body the template registry rendered — from one of the deployment's
declared identities — as one `SendTextMessage` request, and classifies the answer
into the sending sentinels. The management seam is a declaration and no vendor
work: AWS reviews no template, so `Offer` answers the empty code without calling
the vendor, `Approval` answers `sms.ApprovalNotRequired`, and `Retire` is a no-op.
A host writes this module's name into a command; it declares both seams
exclusively, so a command names exactly one carrier and two at once fail in
resolution.

## Selection

`sms.carrier: aws` selects this carrier; another name, or `none`, stands it down
with a reason, and a name no compiled carrier answers to is an error. With no
selection, `region`, `identities` and `default-identity` all empty or absent is
unconfigured and it stands down; some present and some missing is half-configured
and `Prepare` fails naming the missing items; all present is configured and it
runs. The two static credentials are optional — the SDK's own chain may provide
them — but a credential set without a region is half-configured. Two configured
carriers with no selection is an exclusive violation naming both modules. The
log carrier is the fallback and runs only when this carrier, or any other
configured carrier, did not.

The carrier holds no template set of its own: which template a message goes out
under is decided by the template registry, and `Send` is handed a `sms.Resolved`
whose `Body` the registry already rendered from the declaration. `Code` is not
used by this carrier.

The whole leaf package binds
`github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2` and can be dropped
without touching any other module.

The API is the package's godoc, the `sms.Resolved`, `sms.Receipt`,
`sms.Submission`, `sms.Approval` and `sms.CarrierModel` types and the sending
sentinels in `pkg/sms`.

## Boundaries

- **`Send` lays out what it is handed, and decides nothing about the template.**
  A `sms.Resolved` carries the destination, the kind, the rendered body, the
  parameters, the originator, the registration attributes and the lifetime; this
  carrier puts `Body` in `MessageBody`, the chosen identity's value in
  `OriginationIdentity`, the kind's vendor type in `MessageType` and the lifetime
  in `TimeToLive`. A message the registry would refuse never reaches here, so an
  uncovered template or an empty locale is not this package's to catch.
- **The sending identity is chosen and checked here, not by `Resolve`.**
  `Message.Originator` empty means the configured `default-identity`; a name the
  deployment does not declare is `sms.ErrRejected` and no vendor call is made. An
  identity that lists countries and does not cover the destination is
  `sms.ErrUnroutable`. The country is derived only for an Indian destination,
  the one country this carrier already has to understand for the DLT parameters;
  every other destination is left to the vendor. Registration — US 10DLC brand
  and campaign, India DLT, toll-free verification, short codes, per-country
  sender IDs — is **out of scope**: the model declares identities, their kinds,
  their values and their countries, and this process never manages, tracks or
  claims their registration state.
- **The message type follows the template's kind.** Verification and notification
  are `TRANSACTIONAL`; marketing is `PROMOTIONAL`; a declaration that names no
  kind gets notification upstream and is transactional here.
- **An Indian destination carries the DLT pair.** `DestinationCountryParameters`
  is set only for a destination in the `+91` range, with `IN_TEMPLATE_ID` from
  `Resolved.Attributes` and `IN_ENTITY_ID` from the chosen identity's attributes.
  A value the declaration did not give is left out rather than sent empty. The
  Indian calling code and country are the only country facts this carrier reads.
- **The capability declaration is the model.** `Content.TextBody` is set,
  `Content.TemplateCode` is not; `Review.PerTemplate` is false, so nothing waits
  on a vendor review, and `Review.Originator` is true, so a send may name any
  declared identity. `AttributeKeys` is `["IN_TEMPLATE_ID"]`; `MaxBodyRunes` is
  1600; `AsyncReceipt` is true, because the vendor's final delivery state arrives
  asynchronously and a synchronous answer is acceptance, not delivery.
  `Countries` is empty: the carrier restricts nothing beyond what each identity
  declares.
- **The management seam calls no vendor.** `Offer` returns `""` and the registry
  records the body locally, `Approval` answers `sms.ApprovalNotRequired` — not
  `sms.ApprovalUnknown`, which means a review exists and this process does not
  follow it — and `Retire` removes nothing. The submit body is the rendered body
  the registry holds; there is no vendor content resource to create or delete.
- **The exception table is the design's, and a failure it does not name is
  uncertain.** `ThrottlingException` is `sms.ErrThrottled`; `ValidationException`
  is `sms.ErrRejected`; `AccessDeniedException`, `ResourceNotFoundException`,
  `ConflictException` and `ServiceQuotaExceededException` are
  `sms.ErrUnroutable`; every other exception, a timeout, a network error and an
  ended context are `sms.ErrUncertain`. The direction is deliberate: it counts one
  more budget rather than treating an unconfirmed failure as a definite outcome,
  and it never resends. A vendor exception added to the list is added to
  `classify.go` and to `TestTheExceptionTableIsTheDesigns` in the same change.
- **The vendor is called at most once per identifier, and nothing is retried.**
  The vendor interface is not idempotent, so a second call would deliver the
  message twice and bill it twice. A message whose `DedupeID` was seen before is
  answered with the earlier call's outcome; the memory is bounded, lives in this
  process alone, and is shared with the other carriers through
  `pkg/internal/dedupe`. The SDK's own retry is forced to one attempt on the
  client, which is why `TestTheCarrierIsCalledExactlyOnceEvenWhenItFails` sees a
  single request for a server error.
- **The credentials, the body and the parameter values reach no log, no error
  text and no metric.** The secret access key is registered as a literal
  redaction rule by the factory, from the `Secrets` hook, through
  `pkg/internal/literal`, before this package's `Open` builds the client and
  before any logger is bound. A wrapped vendor failure carries the exception's
  name and never its message, because the message can quote the request and the
  request carries the body and the parameter values.
- **No vendor type appears on the package's surface.** The package exports its
  module name, its config namespace and its module descriptor; the vendor client
  is held behind an unexported interface, so a host that imports this package to
  register it never has to name the SDK.
- **Configuration is startup, not settings.** The region, the optional static
  credentials, the endpoint, the configuration set, the default identity and the
  identity list are configuration and are read once. With no access key the SDK's
  own default credential chain is used; one credential without the other is a
  startup failure. An endpoint written without a scheme takes `https`. The
  identity list is a list of structs and therefore takes the primary config
  source alone. Every consistency defect — no region, no identity, an identity
  without a name, a kind or a value, two identities under one name, a kind outside
  the shared vocabulary, or a default identity the section does not declare — is
  `sms.ErrInvalidConfig`, refused by `Open` before a client is built.

## How to run it

From the module directory:

```
env -C pkg go build ./sms/aws/...
env -C pkg go test ./sms/aws/...
env -C pkg go test -race ./sms/aws/...
env -C pkg golangci-lint run ./sms/aws/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it loads no package and still exits 0.

The cases need no real credentials: a case builds the vendor client against a
loopback `httptest` endpoint with static credentials, and drives `Send` through it
so the request shape, the message types, the lifetime, the Indian parameters and
the exception table are held against the real SDK. `config_test.go` drives the
real configuration loader for the identity items and the identity list, and
`assembly_test.go` drives a real registry through the descriptor to `Init`.
