# pkg/authn/login/sms

The SMS login method, registered as `authn.login.sms`. It is one
`authn.LoginMethod` among the first factors: it sends a verification code to a
phone number and proves the ownership of the number when the code checks. It
delivers the login-method capability non-exclusively, so it stands beside every
other method, and takes an SMS module, an account module and a back-off — a host
writes this module's name into a command beside the login engine, a module that
delivers `authn.SMSCodes`, and an account module.

The method is this package's whole surface. It holds no challenge state: the
allowed-prefix table, the budgets, the challenge row and the check of a code are
the SMS module's, taken through `authn.SMSCodes`. It touches no HTTP, session,
token or flow record and writes no log line; the engine logs the cause of an
answer. It reads the account module's registration switch from the configuration
to decide whether an unknown number is sent a real code.

## Where the authority is

- What this method is and why it is shaped this way:
  [`docs/design/modules/design-authn-login.md`](../../../docs/design/modules/design-authn-login.md),
  the LoginMethod seam, the per-flow send budget and the enumeration guard, and
  the SMS section.
- The challenge state machine it drives:
  [`docs/design/modules/design-authn-sms.md`](../../../docs/design/modules/design-authn-sms.md)
  for `SMSCodes`, `SendCode` and the suppressed row, and
  [`docs/adr/adr-authn-sms-2026-10-10.md`](../../../docs/adr/adr-authn-sms-2026-10-10.md)
  for why the state is one module's and the send is pluggable.
- The flow state machine and the comparison-and-swap it is a step of:
  [`docs/adr/adr-authn-login-flow-2026-10-10.md`](../../../docs/adr/adr-authn-login-flow-2026-10-10.md).
- The back-off it counts against: `design-authn-account` and
  `design-authn-failure`, and the `Throttle` seam in `pkg/authn`.
- The API itself: the godoc in this package, and the `LoginMethod`, `Start`,
  `FlowState`, `StepInput`, `Step` and `Proof` shapes in `pkg/authn`.

This file states only what neither of those states: the boundaries a change here
must not cross, and how to run it.

## Boundaries

- **The method holds no challenge state.** The number's allowed prefix, the
  budgets, the challenge row, the verification code and its check are the SMS
  module's. The method answers a `Step`; it does not read or write a challenge,
  and it never sees the code the challenge carries.
- **Two steps, `phone` then `code`.** `Begin` reads the start request's own
  `phone` field and, when it is there, sends a code at once; when it is not, it
  answers `StepPrompt` for the phone. The answer to the phone step sends a code
  and answers `StepPrompt` for the code; the answer to the code step checks it.
  The number the code went to and the challenge that carries it are kept in the
  method's private bytes, so the answer step needs neither from the client and a
  flow record this method did not write cannot be walked into a proof.
- **One send path for every number.** The method never checks the
  allowed-prefix table itself: it reaches `SMSCodes.Send` for every number and
  relays what comes back. A prefix the deployment does not allow comes back as
  `ErrUnsupportedRegion`, which the method answers `StepRefused` carrying that
  sentinel (the engine answers it a 400 `unsupported-region`); a number in a
  suppressed send is answered exactly as one that was delivered.
- **Delivery follows registration and the registration switch.** A code is
  delivered to a number registered to an enabled account, and to any number while
  the account module's registration switch reads open, because a first login may
  create an account for it. Every other number — an unknown one with registration
  closed, one a disabled or deleted account holds — gets a suppressed send that
  is counted and answered as a real one, so the answer tells nobody whether a
  number is registered. The switch is read from `authn.account` through the
  configuration reader; a reader that is not there reads as closed.
- **The per-flow send budget is checked before every send.** A flow whose
  `SendsLeft` is not positive is answered `StepThrottled(0)` without a send. Each
  call makes at most one send and reports it with `Step.WithSends`, and a
  suppressed send counts as one, so the engine's `sends` advances along one path
  for registered and unknown numbers alike.
- **The back-off is read before the comparison, under `ScopePhone`.** The key is
  the SHA-256 of the E.164 number, the same key the account self-service phone
  routes use; the method checks it before `Verify`, counts a wrong code with
  `Fail` and clears a code that holds with `Succeed`. A number in back-off is
  answered `StepThrottled` without a comparison, and a suppressed challenge is
  counted and refused along the same path as a real one.
- **Every refusal of a code reads the same and names nothing.** A wrong code, an
  expired one, a used one and a suppressed challenge all answer `StepRefused`
  carrying `authn.ErrInvalidCredentials`. A dependency that cannot be reached is
  `StepUnavailable`, never a refusal. No cause or error text holds the phone
  number, the code or the challenge.
- **The method serves `login` and `step-up` only.** A `link` binds an external
  identity and an `enroll` registers a factor, and a code proves neither, so a
  flow of another purpose is refused before the method reads or sends anything.
  A purpose a code can prove is a first factor for a login and for a step-up
  that falls back to a fresh first factor.
- **The module requires the three capabilities it uses and nothing else.**
  `SMSCodes`, `Throttle` and `Accounts` are each delivered exclusively by
  another module, so a command that names this method without an SMS module and
  an account module fails before anything is built. The login-method capability
  is delivered without being claimed, which is what lets several methods stand
  side by side.

## How to run it

From the module directory:

```
env -C pkg go build ./authn/login/sms/...
env -C pkg go test ./authn/login/sms/...
env -C pkg go test -race ./authn/login/sms/...
env -C pkg golangci-lint run ./authn/login/sms/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it loads no package and still exits 0.

The cases need no HTTP and no store: they build the method over a fake
`SMSCodes`, a fake `Throttle` and a fake `Accounts` whose answers a case chooses
and reads back, and drive `Begin` and `Answer` directly. `assembly_test.go` drives
a real registry through the descriptor with stand-ins for the three exclusive
capabilities and the configuration reader.
