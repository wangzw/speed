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

| Mode | What it is | `Required()` | Proof check |
|---|---|---|---|
| `off` (default) | the gate is open | false | accepts every proof |
| `pow` | the self-hosted proof of work | true | `pkg/authn/humanverify/pow` |
| `turnstile` | Cloudflare Turnstile | true | not part of this build |

The `off` gate accepts every proof, an empty one included, and reports an empty
site key. It is the mode an assembly runs when it takes the module in and leaves
the item out. It needs no key-value store, no settings store and no route
mounter.

The `turnstile` provider is constructed and reports its name, but its proof
check is not part of this build. It refuses a missing proof with
`authn.ErrHumanVerificationRequired` — that half of the contract does not depend
on the mechanism — and answers a presented proof with `authn.ErrUnavailable`.
The direction is closed on purpose: an enabled deployment sends nothing rather
than accepting a token nobody examined. It has no site key in this build.

The `pow` mode needs the key-value store its challenges live in and the route
mounter that serves the challenge route. A command that names the mode without
either fails the startup with `authn.ErrInvalidConfig` naming what is absent, in
`New` for the store and in `Init` for the mounter.

## The proof of work

The provider issues a challenge on `POST /api/v1/auth/human/challenge`, which is
mounted, publicly, only when the mode is `pow`. The answer carries the provider
name (`pow`), the hash (`sha256`), the challenge, the difficulty and the
lifetime in whole seconds.

A challenge is 16 random bytes, base64url without padding. The client finds a
nonce such that SHA-256 of the decoded challenge followed by the nonce in
eight-byte big-endian carries at least `difficulty` leading zero bits, and
presents `challenge.nonce` as the proof. `Verify` reads the work from the
challenge's stored difficulty, then spends the challenge with a
compare-and-swap, so two concurrent presentations of one proof cannot both pass
and a proof accepted once is refused afterwards.

The challenge is stored under `authn:human:pow:<sha256(challenge) hex>`, with
the difficulty as its value and the challenge's lifetime as its expiry. A
missing, expired, spent or unparsable entry is `authn.ErrHumanVerificationRequired`.

A per-source issuance counter at `authn:human:pow:issue:<sha256(address) hex>`
bounds how many challenges one address holds in a rolling minute — ten — so an
anonymous caller cannot fill the store. The route answers 429 when the bound is
reached. A store that cannot answer is `authn.ErrUnavailable`, answered 503.

A proof longer than 512 bytes is refused with `authn.ErrInvalidArgument` (400):
a valid proof is 22 base64url characters, a dot and a nonce of at most twenty
decimal digits.

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

One input item, under the module's own namespace:

- `mode` — `off`, `pow` or `turnstile`; `off` when absent. A section that omits
  it runs the gate off; a section that misspells it stops the startup.

The proof-of-work mode reads two settings under `authn.humanverify.pow`:

- `difficulty` — leading zero bits a proof must reach; default 18, domain 16..22.
- `challenge-ttl` — how long a challenge stays valid; default 5m, domain 1m..15m.

A settings store that is absent, a read that fails and a value outside the
domain all read as the declared default, so a gate that is on does not stop
issuing challenges over a settings fault.

## Boundaries

- **The module judges the proof, not the request.** Whether a message may be
  sent, and which entry points ask for a proof, belong to the senders. This
  module answers one question about one proof.
- **The gate adds no budget of its own.** It is checked before the send
  budgets, so a refusal costs none of them, and it consumes no counter. The
  per-source issuance counter bounds challenge issuance, not sends.
- **A deployment that wants another mechanism supplies the seam itself.** The
  module claims the seam exclusively, so its own implementation and a bundled
  one cannot both stand; the host leaves this module out of the command.
- **Registration is not gated.** Sending is what the requirement covers, and a
  registration path that sends no message asks this module nothing.
- **The challenge fragment is static.** The merged contract carries the route
  whether or not a run mounts it, which is the drift the platform accepts
  between a fragment and the routes a configured run serves.

## Running it

From the repository root:

```
go -C pkg build ./authn/humanverify/...
go -C pkg vet ./authn/humanverify/...
go -C pkg test ./authn/humanverify/...
go -C pkg test -race ./authn/humanverify/...
env -C pkg golangci-lint run ./authn/humanverify/...
```

The fragment is a build product:

```
env UPDATE_FRAGMENTS=1 go -C pkg test ./authn/humanverify \
  -run TestTheFragmentIsGeneratedFromTheDeclarations
make api-docs
```

`make check` from the repository root is what CI runs. Run `golangci-lint` from
inside `pkg`: handed an absolute path from outside the module it prints zero
issues without loading the packages.
