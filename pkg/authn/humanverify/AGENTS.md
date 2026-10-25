# pkg/authn/humanverify

The human-verification gate of the authentication capability, registered as
`authn.humanverify`. It delivers `authn.HumanVerifier` exclusively, so a command
names this module or supplies a verifier of its own; two exclusive providers in
one run fail the startup. The gate is off by default, and a deployment chooses
the mechanism with the `mode` item.

A send asks the seam before it spends a send budget, so a request the proof
refuses costs no budget, and a message is sent only after the proof has been
accepted. The module holds no send state, sends nothing itself and knows nothing
about which entry point called it.

The API is the package's godoc. Read `authn.HumanVerifier`'s comment before
changing what the seam promises.

## Modes

`mode` is a closed set, and a value outside it stops the startup in `Prepare`
with `authn.ErrInvalidConfig` naming `authn.humanverify.mode`:

| Mode | What it is | `Required()` |
|---|---|---|
| `off` (default) | the gate is open | false |
| `pow` | the self-hosted proof of work | true |
| `turnstile` | Cloudflare Turnstile | true |

The `off` gate accepts every proof, an empty one included, and reports an empty
site key. It is the mode an assembly runs when it takes the module in and leaves
the item out.

The `pow` and `turnstile` providers are constructed and report their names, but
neither proof check is part of this build. They refuse a missing proof with
`authn.ErrHumanVerificationRequired` — that half of the contract does not depend
on the mechanism — and answer a presented proof with `authn.ErrUnavailable`. The
direction is closed on purpose: an enabled deployment sends nothing rather than
accepting a token nobody examined. Neither provider has a site key in this
build.

## The seam's contract

- **A proof is single-use.** A token accepted once is refused when it is
  presented again, whether the second presentation follows at once or later.
  The provider behind the seam owns the consumed record.
- **An empty token is a caller that presented nothing.** A gate that requires a
  proof refuses it with `authn.ErrHumanVerificationRequired`; a gate that
  requires none accepts it.
- **The three failures answer differently.** A missing, invalid, expired or
  already used proof is `authn.ErrHumanVerificationRequired` (403 with a problem
  code that names human verification); a proof longer than the provider admits
  is `authn.ErrInvalidArgument` (400); a provider, or the store that keeps its
  tokens, that cannot be reached is `authn.ErrUnavailable` (503). No send happens
  in any of the three.
- **The token is a credential.** It reaches no log line, no error text, no event
  and no metric, on any path.

## Configuration

One item, under the module's own namespace:

- `mode` — `off`, `pow` or `turnstile`; `off` when absent. A section that omits
  it runs the gate off; a section that misspells it stops the startup.

There is no setting: the mode decides what the run requires, and a deployment
that changes it restarts.

## Boundaries

- **The module judges the proof, not the request.** Whether a message may be
  sent, and which entry points ask for a proof, belong to the senders. This
  module answers one question about one proof.
- **The gate adds no budget of its own.** It is checked before the send
  budgets, so a refusal costs none of them, and it consumes no counter.
- **A deployment that wants another mechanism supplies the seam itself.** The
  module claims the seam exclusively, so its own implementation and a bundled
  one cannot both stand; the host leaves this module out of the command.
- **Registration is not gated.** Sending is what the requirement covers, and a
  registration path that sends no message asks this module nothing.

## Running it

From the repository root:

```
go -C pkg build ./authn/humanverify/...
go -C pkg vet ./authn/humanverify/...
go -C pkg test ./authn/humanverify/...
go -C pkg test -race ./authn/humanverify/...
env -C pkg golangci-lint run ./authn/humanverify/...
```

`make check` from the repository root is what CI runs. Run `golangci-lint` from
inside `pkg`: handed an absolute path from outside the module it prints zero
issues without loading the packages.
