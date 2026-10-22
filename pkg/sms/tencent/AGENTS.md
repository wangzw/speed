# pkg/sms/tencent

The Tencent Cloud SMS carrier, registered as `sms.tencent`. It delivers both
seams over one vendor client: `sms.Sender`, which builds one `SendSms` request
from a resolved message, calls the carrier once and classifies the answer into
the sending sentinels; and `sms.Provider`, the same vendor's template operations
— submit a template for review, read a template's review state, delete a
template. A host writes this module's name into a command; it declares both seams
exclusively, so a command names exactly one carrier and two at once fail in
resolution.

The whole leaf package binds
`github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111` and can
be dropped without touching any other module.

The API is the package's godoc, and the `sms.Resolved`, `sms.Receipt`, the
sending sentinels and the `sms.Provider` seam in `pkg/sms`.

## Boundaries

- **The model declaration is the deployment's shape.** `Model` answers
  `Content{TemplateCode}`, `Review{PerTemplate}` and one `sign-name` identity
  whose name and value are the configured signature. It declares no originator
  choice, no attribute keys, no body bound and no asynchronous receipt. What the
  vendor can carry is stated here and held by `TestTheCarrierDeclaresItsModel`,
  which changes with it.
- **The carrier holds no template set.** Which template a message goes out under
  is decided by the template registry, and this package is handed the vendor
  number and the already-ordered values in an `sms.Resolved`. Its whole part in
  the decision is laying those values out the way the vendor wants them.
- **The parameters are positional.** `Send` lays `Resolved.Ordered` into
  `TemplateParamSet` in the order the approved template expects, sets `TemplateId`
  from `Resolved.Code` and `PhoneNumberSet` from `Resolved.To`.
- **A submission's named placeholders are rewritten to positions, because this
  vendor's reviewed bodies are positional.** `Offer` turns each `${name}` in
  `Submission.Content` into `{1}`, `{2}`, … numbered by `Submission.Params` — the
  reason the seam is named. A parameter declared twice and a placeholder naming an
  undeclared parameter are both `sms.ErrRejected` before the carrier is reached; a
  declared parameter the body never uses is allowed. `Kind` maps to `SmsType`: 1
  marketing, 2 notification, 3 verification.
- **The classification table is this module's, and two codes are the only ones
  that leave the uncertain sentinel.** `LimitExceeded.*` is `sms.ErrThrottled`,
  `FailedOperation.TemplateParamSetNotMatchApprovedTemplate` is `sms.ErrRejected`,
  and every other code — on a send and on a management call alike — is
  `sms.ErrUncertain`, along with a timeout, a network error and a call whose
  context ended. The uncertain direction is deliberate: it counts one more budget
  rather than treating an unconfirmed failure as a definite throttle or refusal,
  and it never resends. A code confirmed against the vendor's list is added to
  `classify.go` and its case gains a per-code assertion in the same change. The
  `LimitExceeded.*` prefix is assumed to be all rate-limit codes; the tests hold
  that assumption.
- **The review state is read one number at a time, and a status of 2 is not an
  approval.** `Approval` queries a single id: an empty `TemplateIdSet` makes the
  vendor page through every template instead of answering that it holds none. The
  vendor's `StatusCode` maps 0 to approved, 1 and 2 to pending — 2 is approved but
  not yet in force, and the vendor's own note says only 0 may be used to send, so
  a send under it must wait — and -1 to rejected with the reviewer's reply as the
  reason. A status the list does not name is `sms.ErrUncertain`. A response that
  names the id nowhere means the vendor no longer holds the template, which reads
  as withdrawn.
- **The carrier is called once, and nothing is retried.** The vendor's calls are
  not idempotent, so a second call would deliver a message twice and bill it
  twice, or file a second copy of a template. The vendor SDK's own network and
  rate-limit retries are switched off, and its region breaker with them, for the
  same reason.
- **One `DedupeID` reaches the carrier at most once.** The seam requires it, so
  the sender remembers the outcome of each identifier it has sent and answers a
  second call for one with that outcome instead of calling again. A resend builds
  a new challenge and so a new identifier, which is never held back. The memory is
  bounded, lives in this process alone, and is shared with the other carrier
  through `pkg/internal/dedupe`.
- **The credentials and the message code reach no log, no error text and no
  metric.** The secret and the credential identifier are registered as literal
  redaction rules by the factory, from the `Secrets` hook, through
  `pkg/internal/literal`, before any logger is bound; the module echoes neither
  the carrier's own message text nor a template parameter into what it returns,
  on a send or a management call.
- **No vendor type appears on the package's surface.** The package exports its
  module name, its config namespace and its module descriptor; the vendor client
  is held in an unexported field, so a host that imports this package to register
  it never has to name the SDK.

## How to run it

From the module directory:

```
env -C pkg go build ./sms/tencent/...
env -C pkg go test ./sms/tencent/...
env -C pkg go test -race ./sms/tencent/...
env -C pkg golangci-lint run ./sms/tencent/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it loads no package and still exits 0.

The cases need no real credentials: a case builds the vendor client against a
loopback `httptest` endpoint and drives the seam through it, so the request shape
(`PhoneNumberSet`, `TemplateId`, `SignName`, `SmsSdkAppId`, `TemplateParamSet`
on a send; `TemplateName`, `TemplateContent`, `SmsType` on an offer;
`TemplateIdSet` on a status read; `TemplateId` on a delete) and the classification
are held against the real SDK. `provider_test.go` holds the review-status mapping
per code and the placeholder rewrite, `config_test.go` drives the real
configuration loader for the required items, `module_test.go` drives a real
registry through the descriptor to `Init` and holds the redaction registration
and the two seams resolving to one product, and `surface_test.go` holds that no
vendor type reaches the exported surface.
