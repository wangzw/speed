# pkg/mail/log

The log mail carrier, registered as `mail.log`. It delivers the `mail.Sender`
seam by writing each rendered message to the process log and answering success.
It reaches no relay, holds no credential and declares no configuration item of
its own.

It is the fallback carrier: it is compiled into every build, including a
production one, and it runs when no other mail carrier was explicitly enabled.
It also declares the `mail.carrier` selection item, because it is the carrier
that is always present; a binary that carries a vendor carrier without this one
accepts no selection and can only auto-select its sole configured carrier.

## Selection

- **`mail.carrier: log`** selects it explicitly. It runs and warns.
- **`mail.carrier: none`** switches the mail capability off. This carrier stands
  down too: `none` means no channel, not "fall back to the log".
- **Another compiled carrier's name** stands it down with a reason.
- **A name no compiled carrier answers to** is an error naming the value, the
  capability and the compiled names.
- **No selection** states `StateAuto`. Core's exclusive resolution leaves it
  running when no other carrier was explicitly enabled and stands it down when
  one was. Its own configuration is complete by definition, so it never has an
  unconfigured or half-configured state.

When it runs, `Open` writes a warning that every message, body included, goes to
the process log and that `mail.carrier` selects a relay instead. The warning is
the only sign that a deployment fell back because no real channel was
configured.

## Boundaries

- **The body reaches the log.** This is the carrier's purpose and the one place
  the capability's "the body reaches no log" promise is deliberately suspended;
  the warning says so. A deployment that routes this log off the machine accepts
  that the body and any verification code leave with it.
- **The verification code is reported under `verification_code`.** The gateway
  registers `code` as a sensitive key name for the process, so a value logged
  under it would be masked. `VerificationCode` reads the first exact six-digit
  run out of the rendered bodies, the plain text first; a longer run is not a
  code.
- **No message is queued, retried or tracked.** The carrier writes one record
  per `Send` and answers success; the rest belongs to the caller.

## How to run it

```
env -C pkg go build ./mail/log/...
env -C pkg go test ./mail/log/...
env -C pkg golangci-lint run ./mail/log/...
```
