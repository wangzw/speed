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
| `turnstile` | Cloudflare Turnstile | true | `pkg/authn/humanverify/turnstile` |

The `off` gate accepts every proof, an empty one included, and reports an empty
site key. It is the mode an assembly runs when it takes the module in and leaves
the item out. It needs no key-value store, no settings store and no route
mounter.

The `turnstile` mode needs a site key, a secret key and the expected hostname.
A command that names the mode without any of them fails the startup with
`authn.ErrInvalidConfig` naming the absent item. The provider posts the proof to
Cloudflare's siteverify endpoint and judges the answer; see below.

The `pow` mode needs the key-value store its challenges live in and the route
mounter that serves the challenge route. A command that names the mode without
either fails the startup with `authn.ErrInvalidConfig` naming what is absent, in
`New` for the store and in `Init` for the mounter.

## Cloudflare Turnstile

The provider posts `secret`, `response` and, when the send names one,
`remoteip` to the configured siteverify endpoint and reads the answer. It
accepts the proof only when `success` is true, the answer's `hostname` equals
the expected hostname (case-insensitively), and — when an action is configured —
the answer's `action` equals it.

The failures answer as the seam's three cases:

- `success` false — the duplicate and timeout codes included, so a replayed
  token is refused — is `authn.ErrHumanVerificationRequired` (403). The
  provider's `error-codes` are logged at Warn with the request's logger and
  never reach the client or the returned error.
- A non-2xx status, a transport failure, a body that is not the provider's
  response, or an answer accepted for another hostname or action is
  `authn.ErrUnavailable` (503): the proof was not judged, so nothing is sent.
- An empty token is `authn.ErrHumanVerificationRequired`; a token longer than
  4096 bytes is `authn.ErrInvalidArgument` (400). Neither reaches the network.

The whole call — connection, request and answer — is bounded by the provider's
timeout. The call goes through the restricted egress client
(`pkg/authn/internal/egress`), so it speaks https to a public address only. The
secret is registered with the logging capability as a literal redaction rule in
`New`; an assembly with no logging capability has nothing to register it with
and still starts.

The site key is public: `SiteKey()` reports it so a client-side widget can be
rendered. The secret and the token are credentials and never reach a log line,
an error text, an event or a metric.

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

Input items under the module's own namespace:

- `mode` — `off`, `pow` or `turnstile`; `off` when absent. A section that omits
  it runs the gate off; a section that misspells it stops the startup.

**Unconfigured and half-configured are different answers, and the mode decides
which.** A section that names no mode, or names `off` or `pow`, is a configured
deployment running that mechanism: Prepare states enabled and nothing is
required of the Turnstile items. Naming `turnstile` is a deployment asking for
that mechanism; if any of `site-key`, `secret-key` or `hostname` is absent, that
is a half-configured gate, and Prepare fails the start naming the item rather
than coming up with a gate that cannot check a proof and sending anyway. There
is no stand-down state here: the module's absence from a run is the host leaving
it out of the command, not a configuration its own section could express.

The `turnstile` mode reads these, and judges them only in that mode:

- `site-key` — the public widget key; required in this mode.
- `secret-key` — the secret that authenticates siteverify; required in this mode,
  sensitive, and registered as a literal redaction rule.
- `hostname` — the hostname the widget must have run on; required in this mode.
- `verify-url` — the siteverify endpoint; default
  `https://challenges.cloudflare.com/turnstile/v0/siteverify`, and an absolute
  https URL.
- `action` — the action the widget was rendered with; optional, empty accepts
  any.
- `timeout` — the whole time one verification may take; default 10s, domain 1s
  to 30s.

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
