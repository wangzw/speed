# pkg/sms/log

The log SMS carrier, registered as `sms.log`. It delivers one `sms.Product` —
the sending seam and the template-management seam — by writing each resolved
message to the process log. It reaches no vendor, holds no credential and
declares no configuration item of its own.

It is the fallback carrier: it is compiled into every build, including a
production one, and it runs when no other SMS carrier was explicitly enabled.
It also declares the `sms.carrier` selection item, because it is the carrier
that is always present; a binary that carries a vendor carrier without this one
accepts no selection and can only auto-select its sole configured carrier.

## Selection

- **`sms.carrier: log`** selects it explicitly. It runs and warns.
- **`sms.carrier: none`** switches the SMS capability off. This carrier stands
  down too: `none` means no channel, not "fall back to the log".
- **Another compiled carrier's name** stands it down with a reason; the vendor
  carriers, the registry and the management surface then run in its place.
- **A name no compiled carrier answers to** is an error naming the value, the
  capability and the compiled names.
- **No selection** states `StateAuto`. Core's exclusive resolution leaves it
  running when no other carrier was explicitly enabled and stands it down when
  one was. Its own configuration is complete by definition.

When it runs, `Open` writes a warning that every message, code included, goes to
the process log and that `sms.carrier` selects a vendor instead.

## Boundaries

- **The message reaches the log.** This is the carrier's purpose and the one
  place the capability's "the code reaches no log" promise is deliberately
  suspended; the warning says so.
- **The code is the SMS module's, not this package's.** The module generates it
  and hands it over as the message's declared `code` parameter. `Record` writes
  it under `verification_code` because the gateway registers `code` as a
  sensitive key name and the logging layer would mask the value. A notice
  carries no code and its record says a message was sent.
- **It answers a real per-template review.** `Model` declares
  `Review.PerTemplate`, an unknown code answers pending, and `Review` moves one
  code's verdict in memory. The review build's control drives it; the carrier
  itself is present in every build.
- **No message is queued, retried or tracked.** Every send goes through the
  real budgets, challenge row and verification in the SMS module.

## How to run it

```
env -C pkg go build ./sms/log/...
env -C pkg go test ./sms/log/...
env -C pkg golangci-lint run ./sms/log/...
```
