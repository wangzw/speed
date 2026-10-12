# pkg/authn/mfa/sms

The SMS second factor, registered as `authn.mfa.sms`. It is one `authn.Factor`
among the kinds: an account proves itself with a code sent to a phone it has
already verified. It delivers the factor capability without claiming it, one
module per kind, and takes two capabilities another module delivers
exclusively — the SMS code engine (`authn.SMSCodes`, from `authn.sms`) and the
account reads (`authn.Accounts`, from `authn.account`). A host writes this
module's name into a command beside the multi-factor module and those two.

The factor keeps no data of its own and no sub-table: it binds the account's
current verified phone identifier, so a phone that changes moves the factor with
it. It sends a code through the engine with the `mfa` purpose and checks the
answer against it. The register row, the back-off, the audit row and the event
belong to the multi-factor module, done around every call it makes here; this
package touches no HTTP, session, token or flow record and writes no log line.

The API is the package's godoc, and the `Factor`, `FactorRecord`, `FlowState`,
`StepInput`, `Step` and `Pass` shapes in `pkg/authn`.

## Boundaries

- **The factor binds a phone whose ownership the account proved, and refuses an
  account that has none.** `provenPhone` reads the account's identifiers and
  takes the first phone whose verification channel proves ownership (`sms-code`
  or `idp:<provider>`) and that carries a verification time. A phone only
  asserted — by an administrator or an import — is not one the factor can bind,
  because a second factor has to be a number the account proved it can receive a
  code at. An account with no such phone is `StepRefused` naming `B/phone` and
  `B/phone/verify`, the routes that prove one. A phone that cannot be read is
  `StepUnavailable`, never a refusal: an unreadable account must not read as one
  with no phone.
- **`Enroll` carries no step of its own.** A phone that is already proven
  registers at once: `Enroll` answers `StepEnrolled` on the first call and sends
  no code. It writes nothing — the register row is the multi-factor module's, and
  it is made active from that answer.
- **`Challenge` sends one code, and nothing without budget.** A flow whose
  `SendsLeft` is not positive is answered `StepThrottled(0)` and sends nothing.
  Otherwise the factor asks the engine to `Send` with purpose `mfa`, the account's
  proven phone, `Deliver` true and the flow's locale, and reports the one send it
  made with `WithSends(1)`, which the engine folds into the flow's budget. The
  challenge identifier the engine returned is kept in the answer's private bytes,
  which the engine keeps as they stand and hands back as `FlowState.Private` when
  the code is answered; it is the only state the factor keeps between the two
  calls.
- **`Challenge` maps the engine's refusals to answers of its own.** A `Send` that
  reads as `ErrThrottled` is `StepThrottled` carrying its `RetryAfter`; one that
  reads as `ErrUnsupportedRegion` is `StepRefused` (the factor's answers have no
  separate shape for it, and the engine merges a factor's outward failure into
  login-failed); any other failure, `ErrUnavailable` included, is
  `StepUnavailable`. A send that could not happen is never a refusal.
- **`Verify` checks the code and nothing else to prove.** It reads the challenge
  identifier from the flow's private bytes and the code from the submitted
  fields; a flow that carries no challenge this factor started, and an answer with
  no code, are both `StepRefused` before the engine is asked, so a malformed
  answer burns no challenge attempt. A code that holds is `StepPassed` carrying
  the `sms` method alone; `ErrInvalidCredentials` is `StepRefused`; any other
  failure is `StepUnavailable`. The code is passed to the engine and kept nowhere
  here — not in a log line, an error text or a metric.
- **`Remove` is a no-op.** The factor owns no data to erase: it points at the
  account's phone identifier, which the account module owns and the factor does
  not write.
- **The module requires the two capabilities it uses and nothing else.** The
  engine and the account reads are each delivered exclusively by another module,
  so a command that names this factor without them fails before anything is
  built. The factor capability is delivered without being claimed, which is what
  lets one module deliver each kind and several kinds stand side by side. The
  module declares no input item and states the enabled stance.

## How to run it

From the module directory:

```
env -C pkg go build ./authn/mfa/sms/...
env -C pkg go test ./authn/mfa/sms/...
env -C pkg go test -race ./authn/mfa/sms/...
env -C pkg golangci-lint run ./authn/mfa/sms/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it loads no package and still exits 0.

The cases need no HTTP, store or database: they build the factor over a fake
`SMSCodes` and a fake `Accounts` whose answers and calls a case chooses and reads
back, and drive `Enroll`, `Challenge` and `Verify` directly with a hand-built
`FlowState`, `StepInput` and `FactorRecord` (`sms_test.go`). `logcapture_test.go`
holds that no code and no phone reaches a log line or an answer, and
`assembly_test.go` drives a real registry through the descriptor with a stand-in
module for each exclusive capability.
