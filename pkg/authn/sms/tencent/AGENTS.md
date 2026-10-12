# pkg/authn/sms/tencent

The Tencent Cloud SMS carrier, registered as `authn.sms.tencent`. It is one
`authn.SMSSender`: it maps this side's (purpose, language) pair to an approved
template number, builds one `SendSms` request, calls the carrier once and
classifies the answer into the sending sentinels. A host writes this module's
name into a command; it delivers the sending seam exclusively, so a command
names exactly one carrier and two at once fail in resolution.

The whole leaf package binds
`github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111` and can
be dropped without touching any other module.

## Where the authority is

- What this carrier is and why it is shaped this way:
  [`docs/design/modules/design-authn-sms.md`](../../../../docs/design/modules/design-authn-sms.md),
  the SMSSender seam and the vendor-adapter section, and
  [`docs/adr/adr-authn-sms-2026-10-10.md`](../../../../docs/adr/adr-authn-sms-2026-10-10.md).
- Why the SDK binding lives in a leaf package:
  [`adr-authn-packaging-2026-10-10`](../../../../docs/adr/adr-authn-packaging-2026-10-10.md).
- The input items this module declares:
  [`design-authn-config`](../../../../docs/design/modules/design-authn-config.md),
  the `authn.sms.tencent` rows.
- The seam itself, `authn.SMS`, `authn.SendReceipt` and the sending sentinels:
  the godoc in `pkg/authn`.

This file states only what neither of those states: the boundaries a change here
must not cross, and how to run it.

## Boundaries

- **The classification table is the design's, and the two verified codes are the
  only ones that leave the uncertain sentinel.** `LimitExceeded.*` is
  `ErrSMSThrottled`, `FailedOperation.TemplateParamSetNotMatchApprovedTemplate`
  is `ErrSMSRejected`, and every other code — a per-number status and a top-level
  error alike — is `ErrSMSUncertain`, along with a timeout, a network error and a
  call whose context ended. The uncertain direction is deliberate: it counts one
  more budget rather than treating an unconfirmed failure as a definite throttle
  or refusal, and it never resends. A code confirmed against the vendor's list is
  added to `classify.go` and its case gains a per-code assertion in the same
  change. The `LimitExceeded.*` prefix is assumed to be all rate-limit codes; the
  tests hold that assumption.
- **The carrier is called once, and nothing is retried.** The vendor's send call
  is not idempotent, so a second call would deliver the message twice and bill it
  twice. The vendor SDK's own network and rate-limit retries are switched off, and
  its region breaker with them, for the same reason.
- **One `DedupeID` reaches the carrier at most once.** The seam requires it, so
  the sender remembers the outcome of each identifier it has sent and answers a
  second call for one with that outcome instead of calling again. A resend builds
  a new challenge and so a new identifier, which is never held back. The memory is
  bounded, lives in this process alone, and is shared with the other carrier
  through `pkg/authn/sms/internal/dedupe`.
- **A (purpose, locale) pair the constructor did not cover never reaches the
  carrier.** The two sets are closed and the constructor checks the template map
  covers every combination and names no other pair, so `Send` with a pair outside
  them is refused before a request is built and is not an uncertain case — no
  message was composed. An empty locale reads as `zh-CN`.
- **The credentials and the message code reach no log, no error text and no
  metric.** The secret only signs the call and is registered, with the credential
  identifier, as a literal redaction rule with the logging capability; the module
  echoes neither the carrier's own message text nor a template parameter into what
  it returns.
- **No vendor type appears on the package's surface.** The package exports its
  module name, its config namespace and its module descriptor; the vendor client
  is held in an unexported field, so a host that imports this package to register
  it never has to name the SDK.

## How to run it

From the module directory:

```
env -C pkg go build ./authn/sms/tencent/...
env -C pkg go test ./authn/sms/tencent/...
env -C pkg go test -race ./authn/sms/tencent/...
env -C pkg golangci-lint run ./authn/sms/tencent/...
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
