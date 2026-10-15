# pkg/sms/aliyun

The Aliyun Dysmsapi carrier, registered as `sms.aliyun`. It delivers two seams
over one vendor client. The sending seam lays a resolved message's parameters out
as the JSON object Aliyun wants, builds one `SendSms` request, calls the carrier
once and classifies the answer into the sending sentinels. The management seam —
`sms.Provider` — offers a template for review (`CreateSmsTemplate`), reads a
template's review state (`GetSmsTemplate`) and retires a template
(`DeleteSmsTemplate`). A host writes this module's name into a command; it
declares both seams exclusively, so a command names exactly one carrier and two
at once fail in resolution.

The carrier holds no template set of its own: which template a message goes out
under is decided by the template registry, and `Send` is handed a `sms.Resolved`
that already carries the vendor code and the parameters. The management seam is
the only place a vendor's numbering is read or written.

The whole leaf package binds `github.com/alibabacloud-go/dysmsapi-20170525/v5`
and can be dropped without touching any other module.

The API is the package's godoc, the `sms.Resolved`, `sms.Receipt`, `sms.Submission`
and `sms.Approval` types and the sending sentinels in `pkg/sms`.

## Boundaries

- **`Send` lays out what it is handed, and decides nothing about the template.** A
  `sms.Resolved` carries the destination, the vendor code and the parameters; this
  carrier serializes `Params` into the JSON object Aliyun takes (the keyed form,
  so the declared order does not matter) and puts `Code` in `TemplateCode`. A
  message the registry would refuse never reaches here, so an uncovered template
  or an empty locale is not this package's to catch.
- **Every send status falls to the uncertain sentinel, and no code is verified yet
  to mean otherwise.** Aliyun's status codes for throttling and rejection have not
  been checked against the vendor's list, so the mapping in `verifiedSentinels` is
  empty and a status that is not `OK` — the empty one included — is
  `sms.ErrUncertain`. The direction is deliberate: it counts one more budget rather
  than treating an unconfirmed failure as a definite throttle or refusal, and it
  never resends. A code verified against the vendor's list is added to that table,
  and the per-code case gains it in the same change. A timeout, a network error, a
  response with no body and a call whose context ended are uncertain for the same
  reason.
- **The management seam maps the vendor's spellings and guesses none of them.** A
  submission's `Kind` maps to `TemplateType` — `KindVerification` to 0,
  `KindNotification` to 1, `KindMarketing` to 2 — which is what the SDK documents
  on `CreateSmsTemplateRequest.TemplateType`; a kind the map does not name is a
  caller defect, refused before the carrier is reached. A template's
  `TemplateStatus` maps to `sms.ApprovalState`: 0 pending, 1 approved, 2 rejected
  and 10 withdrawn. **This review mapping is an assumption**: the vendor's
  numbering comes from the SDK's doc comment on
  `GetSmsTemplateResponseBody.TemplateStatus` and has not been confirmed against
  live credentials, so `TestTheApprovalStatesFollowTheReviewCode` holds it to those
  vectors and a code verified online is added there and to the map together. A
  status the map does not name is uncertain rather than guessed. The refusal reason
  is the review's own reject text.
- **A submission's body is passed through verbatim.** `sms.Submission.Content` is
  written with `${name}` placeholders, and Aliyun's approved templates use that
  same syntax, so this carrier rewrites nothing; a carrier whose vendor took its
  placeholders positionally would be the one to render them here.
- **The vendor is called at most once per identifier, and nothing is retried.**
  The vendor interface is not idempotent, so a second call would deliver the
  message twice and bill it twice — or create a second template nobody approved. A
  message whose `DedupeID` was seen before is answered with the earlier call's
  outcome instead of calling again; the memory is bounded, lives in this process
  alone, and is shared with the other carrier through `pkg/internal/dedupe`.
  The vendor SDK's own retry is switched off for the same reason, on every call.
- **The call carries the caller's context, so a context that ends cancels the
  in-flight request.** The SDK's `*WithContext` methods take the context and cancel
  the HTTP call when it ends; a cancelled call is answered with the uncertain
  sentinel, the same direction as a timeout the carrier itself reports.
- **The credentials and the message code reach no log, no error text and no
  metric.** The access-key secret is registered as a literal redaction rule by the
  factory, from the `Secrets` hook, through `pkg/internal/literal`, before this
  package's `Open` builds the client and before any logger is bound; the module
  does not echo the carrier's own message text into what it returns, because that
  text can carry the request and the request carries the code.
- **No vendor type appears on the package's surface.** The package exports its
  module name, its config namespace and its module descriptor; the vendor client
  is held behind an unexported interface, so a host that imports this package to
  register it never has to name the SDK.

## How to run it

From the module directory:

```
env -C pkg go build ./sms/aliyun/...
env -C pkg go test ./sms/aliyun/...
env -C pkg go test -race ./sms/aliyun/...
env -C pkg golangci-lint run ./sms/aliyun/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it loads no package and still exits 0.

The cases need no real credentials: a case builds the vendor client against a
loopback `httptest` endpoint and drives `Send`, `Offer`, `Approval` and `Retire`
through it, so the request shapes and the classification are held against the real
SDK. `config_test.go` drives the real configuration loader for the required items,
and `assembly_test.go` drives a real registry through the descriptor to `Init`.
