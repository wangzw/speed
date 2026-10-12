# pkg/authn/sms/aliyun

The Aliyun Dysmsapi carrier, registered as `authn.sms.aliyun`. It is one
`authn.SMSSender`: it maps this side's (purpose, language) pair to an approved
template code, builds one `SendSms` request, calls the carrier once and
classifies the answer into the sending sentinels. A host writes this module's
name into a command; it delivers the sending seam exclusively, so a command
names exactly one carrier and two at once fail in resolution.

The whole leaf package binds `github.com/alibabacloud-go/dysmsapi-20170525/v5`
and can be dropped without touching any other module.

The API is the package's godoc, and the `authn.SMS`, `authn.SendReceipt` and
sending sentinels in `pkg/authn`.

## Boundaries

- **Every status falls to the uncertain sentinel, and no code is verified yet to
  mean otherwise.** Aliyun's status codes for throttling and rejection have not
  been checked against the vendor's list, so the mapping in `verifiedSentinels`
  is empty and a status that is not `OK` — the empty one included — is
  `ErrSMSUncertain`. The direction is deliberate: it counts one more budget
  rather than treating an unconfirmed failure as a definite throttle or refusal,
  and it never resends. A code verified against the vendor's list is added to
  that table, and the per-code case gains it in the same change. A timeout, a
  network error, a response with no body and a call whose context ended are
  uncertain for the same reason.
- **The carrier is called at most once per identifier, and nothing is retried.**
  The vendor interface is not idempotent, so a second call would deliver the
  message twice and bill it twice. A message whose `DedupeID` was seen before is
  answered with the earlier call's outcome instead of calling again; the memory
  is bounded, lives in this process alone, and is shared with the other carrier
  through `pkg/authn/sms/internal/dedupe`. The vendor SDK's own retry is switched
  off for the same reason.
- **The call carries the caller's context, so a context that ends cancels the
  in-flight request.** The SDK's `SendSmsWithContext` takes the context and
  cancels the HTTP call when it ends; a cancelled call is answered with the
  uncertain sentinel, the same direction as a timeout the carrier itself reports.
  Nothing is retried: the vendor interface is not idempotent, so the SDK's own
  retry is switched off.
- **A (purpose, locale) pair the constructor did not cover never reaches the
  carrier.** The two sets are closed and the constructor checks the template map
  covers every combination, so `Send` with a pair outside them is refused before
  a request is built and is not an uncertain case — no message was composed. An
  empty locale reads as `zh-CN`.
- **The credentials and the message code reach no log, no error text and no
  metric.** The access-key secret only signs the call and is registered as a
  literal redaction rule with the logging capability; the module does not echo
  the carrier's own message text into what it returns, because that text can
  carry the request and the request carries the code.
- **No vendor type appears on the package's surface.** The package exports its
  module name, its config namespace and its module descriptor; the vendor client
  is held behind an unexported interface, so a host that imports this package to
  register it never has to name the SDK.

## How to run it

From the module directory:

```
env -C pkg go build ./authn/sms/aliyun/...
env -C pkg go test ./authn/sms/aliyun/...
env -C pkg go test -race ./authn/sms/aliyun/...
env -C pkg golangci-lint run ./authn/sms/aliyun/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it loads no package and still exits 0.

The cases need no real credentials: a case builds the vendor client against a
loopback `httptest` endpoint and drives `Send` through it, so the request shape
(`SignName`, `TemplateCode`, `TemplateParam`) and the classification are held
against the real SDK. `config_test.go` drives the real configuration loader for
the required and container-leaf items, and `assembly_test.go` drives a real
registry through the descriptor to `Init`.
