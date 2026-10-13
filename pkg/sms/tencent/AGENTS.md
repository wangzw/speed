# pkg/sms/tencent

The Tencent Cloud SMS carrier, registered as `sms.tencent`. It is one `sms.Sender`:
it maps a message's template and locale to an approved template number, builds one
`SendSms` request, calls the carrier once and classifies the answer into the
sending sentinels. A host writes this module's name into a command; it declares
the sending seam exclusively, so a command names exactly one carrier and two at
once fail in resolution.

The whole leaf package binds
`github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111` and can
be dropped without touching any other module.

The API is the package's godoc, and the `sms.Message`, `sms.Receipt` and sending
sentinels in `pkg/sms`.

## Boundaries

- **The templates are a first-class declaration, and the parameters are
  positional.** The config's `templates` is a `map[string]sms.TemplateSpec`: each
  template names its ordered parameters and its language-to-code map. The
  constructor freezes it into an `sms.Declaration` and `Send` resolves a
  `sms.Message` into the vendor code and the parameters in the declared order,
  which it lays into `TemplateParamSet`. A template map the config does not shape
  this way is a startup failure.
- **The classification table is this module's, and the two verified codes are
  the only ones that leave the uncertain sentinel.** `LimitExceeded.*` is
  `sms.ErrThrottled`, `FailedOperation.TemplateParamSetNotMatchApprovedTemplate`
  is `sms.ErrRejected`, and every other code — a per-number status and a top-level
  error alike — is `sms.ErrUncertain`, along with a timeout, a network error and
  a call whose context ended. The uncertain direction is deliberate: it counts
  one more budget rather than treating an unconfirmed failure as a definite
  throttle or refusal, and it never resends. A code confirmed against the
  vendor's list is added to `classify.go` and its case gains a per-code assertion
  in the same change. The `LimitExceeded.*` prefix is assumed to be all rate-limit
  codes; the tests hold that assumption.
- **A message the declaration does not cover never reaches the carrier.** An
  unlisted template, a locale with no code, or a missing or extra parameter is a
  caller defect: `Resolve` answers `ErrRejected` and no request is built.
- **The carrier is called once, and nothing is retried.** The vendor's send call
  is not idempotent, so a second call would deliver the message twice and bill it
  twice. The vendor SDK's own network and rate-limit retries are switched off, and
  its region breaker with them, for the same reason.
- **One `DedupeID` reaches the carrier at most once.** The seam requires it, so
  the sender remembers the outcome of each identifier it has sent and answers a
  second call for one with that outcome instead of calling again. A resend builds
  a new challenge and so a new identifier, which is never held back. The memory is
  bounded, lives in this process alone, and is shared with the other carrier
  through `pkg/sms/internal/dedupe`.
- **The credentials and the message code reach no log, no error text and no
  metric.** The secret and the credential identifier are registered as literal
  redaction rules by the factory, from the `Secrets` hook, through
  `pkg/internal/literal`, before any logger is bound; the module echoes neither
  the carrier's own message text nor a template parameter into what it returns.
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
loopback `httptest` endpoint and drives `Send` through it, so the request shape
(`PhoneNumberSet`, `TemplateId`, `SignName`, `SmsSdkAppId`, `TemplateParamSet`)
and the classification are held against the real SDK. `config_test.go` drives the
real configuration loader for the required and container-leaf items,
`module_test.go` drives a real registry through the descriptor to `Init` and holds
the redaction registration, and `surface_test.go` holds that no vendor type
reaches the exported surface.
