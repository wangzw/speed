# pkg/authn/humanverify

The human-verification gate of the authentication capability, registered as
`authn.humanverify`. It delivers `authn.HumanVerifier` exclusively, so a command
names this module or supplies a verifier of its own; two exclusive providers in
one run fail the startup.

The gate answers per surface. The **phone surface** guards the four sends that
address a phone number — SMS login, the SMS second factor, phone enrollment and
the anonymous reset's SMS channel — and runs a self-hosted captcha by default.
The **other surface** guards every send that does not address a phone number —
an email reset code and a self-service email addition — and runs a self-hosted
proof of work by default. A deployment chooses each surface's mechanism with
`phone-mode` and `mode`.

A send asks the seam before it spends a send budget, so a request the proof
refuses costs no budget, and a message is sent only after the proof has been
accepted. The module holds no send state, sends nothing itself and knows nothing
about which entry point called it.

The API is the package's godoc. Read `authn.HumanVerifier`'s comment before
changing what the seam promises.

## Modes

The modes form a closed set on each surface, and a value outside it stops the
startup in `Prepare` with `authn.ErrInvalidConfig` naming
`authn.humanverify.phone-mode` or `authn.humanverify.mode`:

| Mode | What it is | `Required(scope)` | Proof check |
|---|---|---|---|
| `off` | the surface is open | false | accepts every proof |
| `pow` | the self-hosted proof of work (default on the other surface) | true | `pkg/authn/humanverify/pow` |
| `captcha` | the self-hosted captcha (default on the phone surface) | true | `pkg/authn/humanverify/captcha` |
| `turnstile` | Cloudflare Turnstile | true | `pkg/authn/humanverify/turnstile` |

The `off` gate accepts every proof, an empty one included, and reports an empty
site key. A deployment states it to run no gate on a surface; that surface needs
no key-value store, no settings store and no route mounter.

The `turnstile` mode needs a site key, a secret key and the expected hostname.
A command that names the mode on either surface without any of them fails the
startup with `authn.ErrInvalidConfig` naming the absent item. The provider posts
the proof to Cloudflare's siteverify endpoint and judges the answer; see below.

The `pow` and `captcha` modes need the key-value store their challenges live in
and the route mounter that serves the challenge routes. A command that names
either mode without the store fails the startup in `New`; one without the
mounter fails it in `Init`; both name what is absent. The `pow` mode also reads
its parameters from the settings store when one is present.

## The self-hosted captcha

The provider draws three forms: an image of digits, a slider puzzle and an audio
reading of digits. The image and the audio use
[base64Captcha](https://github.com/mojocn/base64Captcha) (v1.3.8); the slider is
drawn with the standard library's image operations. An empty form is the image.

It issues a challenge on `POST /api/v1/auth/human/captcha/challenge`, which is
a declared operation of this module's fragment -- so the generated client calls
it -- and is mounted, publicly, only when a surface runs `captcha`. The answer
carries the provider name (`captcha`), the form, an instruction key, the
challenge id, the paths of the media that form fetches, and the lifetime in
whole seconds. The media routes are public, addressed by the unguessable id, and
are not declared: the client reads their paths from the challenge answer and
fetches them as images or audio, never through the API client.

- `GET /api/v1/auth/human/captcha/image/:id` — the digits picture.
- `GET /api/v1/auth/human/captcha/background/:id` — the slider background.
- `GET /api/v1/auth/human/captcha/piece/:id` — the slider piece.
- `GET /api/v1/auth/human/captcha/audio/:id` — the audio reading.

A challenge and its media stay in the key-value store for five minutes. The
record holds the answer as HMAC-SHA-256 digests keyed by a random per-challenge
salt rather than text; the salt is stored beside the digests, so a digest copied
out of the record without its salt cannot be enumerated over the five-digit
answer space. The slider records one digest per offset within its tolerance. The
proof is the challenge id and the answer joined by a dot, `id.answer`; the id is
base64url without padding and the answer is digits, or a decimal offset for the
slider.

A per-source issuance counter at `authn:human:captcha:issue:<sha256(address) hex>`
bounds how many challenges one address holds in a rolling minute — ten — so an
anonymous caller cannot fill the store. The route answers 429 when the bound is
reached. A store that cannot answer is `authn.ErrUnavailable`, answered 503.

`Verify` reads the record, compares the answer in constant time, spends the
record with a compare-and-swap so two presentations of one proof cannot both
pass, and then deletes the record and its media. A wrong answer spends the
record too: one challenge is one attempt. A missing, expired, spent or
unparsable challenge is `authn.ErrHumanVerificationRequired`. A media route for
an unknown, expired or spent id answers 404; a store that cannot answer is
`authn.ErrUnavailable`, answered 503.

The media are written before the record, so a challenge becomes usable only once
everything its client fetches is in place. A failed write removes what the call
already stored.

The audio form is the screen-reader alternative. The request chooses the
digit-reading language with the `lang` query item as a BCP 47 tag; the library
pronounces English, Japanese, Russian and Chinese only, a tag naming another
language is a 400, and an absent item uses the request's active locale, falling
back to English for a locale outside that set. That fallback is a residual
accessibility gap: the audio alternative still exists, but a viewer whose
language the library does not pronounce hears English digits. It is recorded
here rather than presented as a fully localised alternative. It has the same
single-use and lifetime semantics as the other two forms.

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

The site key is public: `SiteKey()` reports it for a surface that runs
Turnstile, so a client-side widget can be rendered. The secret and the token are
credentials and never reach a log line, an error text, an event or a metric.

## The proof of work

The provider issues a challenge on `POST /api/v1/auth/human/challenge`, which is
mounted, publicly, only when a surface runs `pow`. The answer carries the
provider name (`pow`), the hash (`sha256`), the challenge, the difficulty and
the lifetime in whole seconds.

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

- **Every answer is per surface.** `Required`, `Provider`, `SiteKey` and
  `Verify` take a `HumanScope` (`phone` or `other`) and answer for that surface
  alone. A deployment may run a different mechanism on each. An unknown scope
  answers as the phone surface, so a caller that names none cannot reach an off
  gate on the other surface.
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

- `phone-mode` — `off`, `pow`, `captcha` or `turnstile`; `captcha` when absent.
  It selects the mechanism the phone surface runs.
- `mode` — `off`, `pow`, `captcha` or `turnstile`; `pow` when absent. It selects
  the mechanism the other surface runs.
- `site-key`, `secret-key` and `hostname` — required when either surface runs
  `turnstile`.
- `verify-url` — the siteverify endpoint; default
  `https://challenges.cloudflare.com/turnstile/v0/siteverify`, and an absolute
  https URL.
- `action` — the action the widget was rendered with; optional, empty accepts
  any.
- `timeout` — the whole time one verification may take; default 10s, domain 1s
  to 30s.

**Unconfigured and half-configured are different answers, and the modes decide
which.** A section that names no mode runs the captcha on the phone surface and
the proof of work on the other, and nothing is required of the Turnstile items.
Naming `turnstile` on either surface is a deployment asking for that mechanism;
if any of `site-key`, `secret-key` or `hostname` is absent, that is a
half-configured gate, and Prepare fails the start naming the item rather than
coming up with a gate that cannot check a proof and sending anyway. There is no
stand-down state here: the module's absence from a run is the host leaving it
out of the command, not a configuration its own section could express.

The proof-of-work mode reads two settings under `authn.humanverify.pow`:

- `difficulty` — leading zero bits a proof must reach; default 18, domain 16..22.
- `challenge-ttl` — how long a challenge stays valid; default 5m, domain 1m..15m.

A settings store that is absent, a read that fails and a value outside the
domain all read as the declared default, so a gate that is on does not stop
issuing challenges over a settings fault.

## Boundaries

- **The module judges the proof, not the request.** Whether a message may be
  sent, and which entry points ask for a proof on which surface, belong to the
  senders. This module answers one question about one proof.
- **The gate adds no budget of its own.** It is checked before the send
  budgets, so a refusal costs none of them, and it consumes no counter. Each
  self-hosted mechanism's per-source issuance counter bounds challenge issuance,
  not sends.
- **A deployment that wants another mechanism supplies the seam itself.** The
  module claims the seam exclusively, so its own implementation and a bundled
  one cannot both stand; the host leaves this module out of the command.
- **Registration is not gated.** Sending is what the requirement covers, and a
  registration path that sends no message asks this module nothing.
- **The challenge fragments are static.** The merged contract carries the routes
  whether or not a run mounts them, which is the drift the platform accepts
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

The fragments are build products:

```
env UPDATE_FRAGMENTS=1 go -C pkg test ./authn/humanverify \
  -run TestTheFragmentIsGeneratedFromTheDeclarations
make api-docs
```

`make check` from the repository root is what CI runs. Run `golangci-lint` from
inside `pkg`: handed an absolute path from outside the module it prints zero
issues without loading the packages.
