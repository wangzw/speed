# starters/demo

A runnable, copyable skeleton of a speed product. Copy this directory, rename
it, and grow a business on it. It assembles this repository's modules into one
program — an HTTP entry point and its endpoints, the platform baseline below,
the logging and telemetry providers, the operations surface and the frontend
host — and writes the command tree that says what each run may be.

It is a starting point, not an evaluation host. The modules under `pkg/` deliver
the seams; this program shows one way to put them together. It ships no business
domain: `server/modules/` is the empty slot a product's own modules land in.
Because it seeds no business data, several surfaces open empty in the browser:
the object store, the console dashboard beyond the tenant counts it reads from
the platform's own roster, the notification inbox (the baseline runs no
notification type and dispatches nothing; the review build turns the fixture's
switch on, see "The review build's notification fixture" below), and the
preference matrix. An empty
surface is the baseline doing what it says it does, not a capability left
unfinished — the data is the product's and its users' to produce.

It is two trees under one directory. `server/` is the Go program — its own
module, its configuration samples and the frontend bundle it embeds; `web/` is
the frontend package, whose source and build tooling live there and whose build
output lands in `server/dist/`. The build output sits inside the Go tree because
`go:embed` reaches only files under the embedding package's own directory and a
pattern may not climb out with `..`.

## The platform baseline

The program names the platform baseline: every platform capability that starts
without a credential, an external account or a service outside the deployment
stack. What that leaves out is the capability that needs one of those, and a
product adds it once it holds the dependency:

| Left out | Why |
|---|---|
| `authn.login.google`, `authn.login.github`, `authn.login.wechat-web`, `authn.login.wechat-mp` | a social provider's client credentials |
| `authn.sso.oidc`, `authn.sso.saml` | an enterprise provider's client credentials and metadata |
| `sms.*`, `authn.login.sms`, `authn.mfa.sms`, `authn.sms` | an SMS vendor's account |
| `mail.smtp` | an SMTP account |
| `authn.emailcode` | a mail carrier to send through; the review build pairs it with `mail.log` |
| `objectstore.s3` | an S3 account and bucket |

The test of a doubtful module is the module's own contract: if its required
configuration items cannot be given a value that starts, it does not belong
here.

**The baseline carries WebAuthn (`authn.mfa.webauthn`), and it is the one
capability the criterion admits that a copy still has to be given
deliberately.** Its relying party is bound to the origin the deployment is
reached at — an item `testdata/` carries, not a credential or a service — so it
starts with nothing external and passes the test above. It is named here so the
skeleton can show what the platform's management surface is for: a step-up asks
for a factor kind the session has not already proved, and with TOTP as the only
kind a session that signed in with TOTP has nothing left to raise itself with, so
the step-up start is refused. TOTP beside WebAuthn gives the step-up a factor
kind to offer, and WebAuthn is the one the bundled deployment carries.

The bundled frontend carries the passkey step to completion: `@speed/auth-ui`,
`@speed/account-ui` and `@speed/console-ui` each drive the browser's WebAuthn
ceremony (`navigator.credentials.create`/`get`) from the step's own options and
report the credential it returns. Enrolling the passkey and completing the
step-up both work on the bundled UI (see "Re-authentication and impersonation on
a fresh copy" below).

**The baseline leaves out the static operator password table (`authn.static`),
though the criterion would admit it: it needs no external service, only the
table the configuration names.** It is the one backend that takes the Basic
scheme, and a browser opens its native password prompt for a Basic or Digest
challenge on any 401. This skeleton serves the frontend bundle to a browser, so
a 401 carrying Basic — a lapsed session or token on a guarded endpoint — would
put a native password box in front of a signed-in user instead of letting the
frontend end the session and return to its own login page. The session cookie
and the bearer token carry every caller the guards here reach, so the demo wires
no Basic backend and `testdata/` carries no password table; a copy that wants a
Basic-authenticated operator or probe adds `authn.static` and its
`authn.static.subjects` table back itself.

The roster is written in two places in the server package. `main.go` carries
`baselineServe` and `baselineWorker`, the platform core: the modules a run
always names because they start without a credential, an external account or a
service outside the deployment stack. `capabilities.go` carries
`capabilityModules`, the compile-time ceiling: every other capability the
program can run — the social and enterprise OIDC login methods, the SMS family,
the mail carriers and the verification-code sender, and the notification
fixture — compiled into every build and named by `serve`. A module in that list
is considered, not started: each states in `Prepare` whether its configuration
lets it run, so one binary serves both a bare run and a fully configured
deployment. `worker` names the background modules that need neither a router nor
a gateway — the roster, settings, the job queue and its scheduler, and the
outbox relay. The stack's engine, key-value backend, event backend and export
provider are named on the commands rather than on the stack node, so `migrate`
still runs the engine and the declarations alone.

### What a run names and what the imports reach

Naming a module in the command tree, importing its package, and its own
configuration answer three different questions:

- **The command tree decides what a process considers.** A module an `Include`
  list names takes its place in that run's participating set; a module nothing
  names is never assembled and never started.
- **The binary's imports decide what the contract document covers.** `gen` and
  `/openapi.json` merge the `http.Spec` fragments of every module the imports
  reach — the available set — whether or not a command names that module for a
  run.
- **A named module's own configuration decides whether it starts.** Every
  participating module states in `Prepare` whether it runs. An optional
  capability whose configuration is absent stands itself down with a reason in
  the startup diagnostics; a half-configured one fails the start naming the
  item.

So every platform module that declares an HTTP fragment is imported. `sms.admin`
is in `capabilityModules` and named by `serve`; it runs only when its
configuration enables the SMS family, which the review configuration does and
the baseline does not. `authn.sso.saml` is imported for the contract alone and
named by no command. Both fragments still describe the platform's API surface.
Leaving one unimported would drop its operations from the merged document with
no error — the document would simply be smaller than the platform contract.
`server/main.go` carries the blank import for `authn.sso.saml` beside a comment
saying why; the declarations of a module reached this way are collected by the
stack node's `IncludeAllDeclarations`, and no baseline `Include` names it.

### The review build

`go build -tags review` compiles the default build plus the review-only test
controls and the loopback guard (`server/review.go`, `review_sms_control.go`
and `notification_control_review.go`). Every capability the program can run —
the four social methods, the enterprise OIDC method (`authn.sso.oidc`), the SMS
family with the `sms.log` log carrier and the `sms.admin` template page,
name-and-password registration with `authn.emailcode` over the `mail.log` log
carrier, and the SMS second factor (`authn.mfa.sms`) — is compiled into every
build and considered by `serve` (`capabilities.go`); whether one starts is its
own configuration decision. `server/review_off.go` is the untagged counterpart
and names nothing, so a default binary carries neither the controls nor the
guard.

`mail.log` is a carrier built through `mail.NewModule`, the same factory
`mail.smtp` uses: it reaches no relay and writes each rendered message to the
process log. `authn.emailcode` renders `authn.email.verify` in the language the
request announced (falling back to English) and hands the message over. Register
the code for the walkthrough with `verification_code`; the value is not logged
under `code`, because that name is a registered sensitive key of the process and
the logging layer would mask it.

**The review build runs the anonymous password reset on both channels.** Its
configuration sets the `authn.login.password-reset` setting to `open`, and
`sms.registry.templates` declares the optional `authn.code.reset` template
beside the three required ones — declaring it is what makes the SMS module
publish the reset purpose and the carrier able to carry it. `authn.emailcode`
provides `authn.EmailResetCodeSender` from its compiled-in catalog, so the mail
carrier serves the email channel. The sign-in surface therefore carries the
forgot-password entry, `/password-reset` is the host's own page over
`@speed/auth-ui`'s `PasswordResetScreen`, and the reset code arrives through
`mail.log` or `sms.log` like any other verification code.

The inbox and number resend windows are held by the destination across purposes,
so a reset sent shortly after the account proved that same address or number is
suppressed: the surface still shows its sent notice, and its resend control —
which states the remaining interval — is what releases the message. The two
journeys read that control rather than sleeping.

`server/review/sqlite.yaml` is the configuration the build reads. It keeps
`authn.account.signup` and `authn.account.password-signup` open, widens the four
`authn.login.email-*` budgets and lowers the resend interval to its floor so a
person can walk the flow, points the database, key and object root under
`var/`, names the endpoints `sms.admin` mounts on, and lowers
`sms.registry.poll-interval` so a verdict the review control moves is reflected
on the next list read. Baseline `serve` names the same capability roster but its
configuration leaves the carriers unconfigured and registration closed, so those
capabilities stand themselves down and the baseline login page advertises no
registration entry.

The same file carries the `authn.sso` block the enterprise OIDC method reads.
It states the deployment defaults — `allow-private-egress: false` and both
outbound seams empty — so the TXT lookup of a domain-ownership proof takes the
system resolver and the method's egress trusts the system roots. A review that
drives the enterprise journey points the three at local fixtures the end-to-end
harness starts: the resolver address of a UDP stub that answers the test
domain's TXT name, the CA file that signed the local provider's certificate, and
the loopback relaxation its egress client needs. Those are fixtures, not
deployment values; a deployment leaves the block as it stands. The one item
that is not a seam is `authn.sso.oidc.enabled: true`, which turns the method on:
the method is compiled into every build and stands itself down when the switch
is absent, so a baseline or a deployment offers no enterprise entry and the
review assembly does.

`server/review_sms_control.go` is the review-only control over the review
carrier's per-template review. It mounts
`POST /api/v1/system/sms/review/verdicts`, moves one vendor code's verdict, and
declares the static feature `review.sms.control`. The console's SMS page renders
the host's verdict control only while the feature is served, so a baseline
deployment carries neither the route nor the control.

The walkthrough is: register a username, email and password; sign in with the
password through the ordinary flow; add the email under the account's own
identifiers; read the code from the `log mail:` line; submit it to
`POST /api/v1/auth/identifiers/verify`; watch `/api/v1/auth/me` report
`verification.state = verified`; then call the personal ensure. Registration
itself sends no mail, and `sms.log` verifies a phone the same way without
lifting the registration limit.

The build also carries a guard (`reviewGuardModule`) that refuses to start when
any configured listener is not loopback: the review build seeds the published
administrator credential and opens registration. The guard limits the listening
address only; a code written to the process log can still leave the machine
through log collection or export.

#### The review build's notification fixture

`server/notification_fixture.go` is compiled into every build and named in
`capabilityModules`, and gives the member-notification and SMS-template journeys
a running counterpart. It adds four things the baseline deliberately lacks.
Whether a run dispatches it is the fixture's own `Prepare` decision, stated by
the explicit switch `review.notification.enabled`: there is no state to infer it
from, because the type is present in every build and the tenancy roster is data.
With the switch absent or false the fixture states `StateDisabled` and takes no
part; with it true the worker, its schedule and the recipient wiring run. There
is no half-configured state: the switch is the whole of the fixture's own
configuration.

- **A type.** It declares `review.notice` — group `review`, default channels
  `in_app`, `email` and `sms`, unsubscribable — with its text in
  `server/review_notification_locales/en-US.json` and `zh-CN.json`. The
  catalog's `review.notice.sms.template` entry names the runtime SMS template
  the template-rollout journey registers (`review-notice`), and the type's
  parameter whitelist admits `to`, which is both the dispatch's destination and
  the template's only declared parameter. The declaration lives on the module
  rather than on the enabled run on purpose: `notification.preferences` and
  `notification.delivery` collect the type taxonomy with `core.Resources` over
  the *available* set, not the running set, so the type is part of the
  compile-time ceiling even in a run that stands the fixture down. Disabling the
  fixture leaves the type in the taxonomy and removes the worker, the schedule
  and the recipient wiring from the feature set.
  `server/review_notification_isolation_test.go` holds that boundary in the
  default build, and the review-tagged cases hold the other direction.

- **A recipient-aware dispatch.** The fixture resolves each recipient's proved
  phone and email address. A proved phone is the single destination and the sms
  channel; with no proved phone the dispatch falls back to the email address and
  the in-app/email channels. Because the capability carries one destination
  parameter for every external channel, the fixture prunes the channel the
  destination cannot address through the preference seam before it submits —
  monotonically, so a channel the recipient turned off is never re-enabled. An
  account with neither a proved phone nor an address is skipped rather than
  failed.

- **A worker-driven dispatch.** The module declares a `jobs.Schedule`, and the
  platform's scheduler enqueues one job per tenant per window. The module's
  handler turns each job into one dispatch per addressable active member. Each
  dispatch renders in the recipient's own language — the account's stored
  `i18n.locale` preference when it has one, and the platform's default locale
  when it does not — so one window addresses a member who chose Chinese in
  Chinese and a member with no preference in the default. The dispatch is the
  real one — `notification.Dispatcher` validates it against the collected
  taxonomy, the job queue carries it, and the delivery pipeline writes the
  in-app row and renders the external channel through `mail.log` or the
  review SMS carrier. `server/review/sqlite.yaml` lowers
  `jobs.scheduler.tick` to two seconds, so a tenant created during a walkthrough
  starts receiving notices promptly; the dispatch window itself is the fixture's
  own constant. The worker's window is the member-notification journey's
  dispatch, not the SMS-template rollout journey's proof.

- **The review-only tenant control.** The module mounts
  `POST /api/v1/notifications/review/notice` on the `api` endpoint and declares
  the static feature `review.notification.control`. A signed-in member of the
  tenant clicks the tenant home page's control, and the handler dispatches one
  notice to the caller through the same recipient-aware path. The route and the
  feature are compiled only under the review tag, so neither exists in a default
  binary; the home page renders the control only while the feature document
  carries the name. The control is the SMS-template rollout journey's business
  action.

- **The preference recipient wiring.** The preference routes read the recipient
  from the request context (`preferences.WithRecipient`); the capability holds
  no identity, so the host names it. The baseline does not, which is why its
  matrix is empty and a preference write is refused. The review module registers
  a layer on the `api` endpoint in the recording stage — inside the
  authentication gateway, so the identity is already on the context, and outside
  the authorization guard — that copies the authenticated caller's effective
  account (the impersonated one while impersonating) onto the context. A request
  with no identity is left untouched, so an anonymous caller is still refused
  before the preference handler runs.

`TestReviewNotificationJourneyIsReachable` is the worked check: a registered
member gets a tenant, the worker writes the type into the inbox and the email
renders from the catalog, the member turns the email channel off and the write
succeeds, an anonymous caller is still refused, and the following windows send
no email.

#### The review build's impersonation fixture

The impersonation journey needs a state a hand walkthrough can only reach by
waiting an hour: an operator that is not a member of the target tenant, holds two
two-step methods with at least one seasoned non-SMS method, and can raise a stale
session with a kind it did not prove at sign-in. The review build seeds that
state on `seed`, in the review build only, so the journey is walkable from a
fresh database. This is an explicit review-only exception; a baseline binary
reads none of it.

- **The two factors, written through the product's seams.** The TOTP factor is
  registered through the multi-factor register and its seed is written through
  the TOTP module's own encrypted seed register (`serializer:encrypted`), so the
  database module seals it exactly as a browser enrollment would. The SMS factor
  goes through the register too; that factor keeps no data of its own, and it
  binds the account's proven phone. The seeding helpers are behind
  `//go:build review` in the owning packages
  (`pkg/authn/mfa/review_seed.go`, `pkg/authn/mfa/totp/review_seed.go`), and
  `server/review_seed.go` is the review build's hook into the `seed` command.
  Nothing here writes a sealed column by hand.

- **The backdated fields.** `authn_factors.activated_at` — and the row's
  `created_at` — of the TOTP factor are set two hours in the past, far enough
  past `authn.mfa.sensitive-min-age` (one hour here) that the impersonation gate
  reads the factor as seasoned. The fixture chooses that instant: `Enroll`
  activates at the clock's now, so the fixture hands the register's own
  activation statement a backdated instant through
  `pkg/authn/mfa/review_seed.go`. Everything else about the row is the
  register's own `insertPending`; the SMS factor is activated at the current
  time.

- **The proven contacts.** The operator is seeded with an email address, and the
  fixture proves it (`email-code`) and adds and proves a phone number
  (`sms-code`) through the account module's `AddIdentifier`, the same write a
  completed code flow performs. Both codes have a live path in this build
  (`mail.log`, `sms.log`), so the fixture starts the journey from the state
  the flows reach rather than from a verification detour.

- **The assembly's additions.** `authn.mfa.sms` is in `capabilityModules` and
  named by `serve`; the review configuration gives it a live carrier, so a
  session signed in with TOTP has a disjoint kind to step up with, and
  `server/review/sqlite.yaml` lowers `authn.impersonation.max-auth-age` to its
  one-minute floor (the declared default is five minutes; the domain is one
  minute to thirty) and seeds the target member of `acme` through
  `seed.members`. The target is the username `member` with no email address, so
  the notification fixture's mailbox counts stay what they assert. The operator
  is deliberately not a member of `acme` and holds no binding in that domain:
  `acme`'s tenant list names `member` as its administrator, so the operator has
  neither a membership nor a tenant-admin binding there. It holds
  `platform-admin` in the system (platform) domain alone. The impersonation gate
  does not read the sender's permissions in the target tenant, so the journey is
  still reachable.

The walkthrough: `seed`, then `serve`, then sign in as `operator` (the username
the seed writes; the proved address is not selected as a login way) with
`operator-dev-secret` and the current code of the fixture's TOTP seed
(`JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP`, issuer `Speed Demo`, SHA-1, six digits, a
thirty-second period; import it into an authenticator, or run the
standard-library one-liner under "Exercising a write path"). The seed is a
fixture and not a credential, and it is written through the TOTP module's
encrypted register. Wait out the one-minute freshness window, open the console's
impersonation page, pick the `acme` tenant and the `member` target, and submit.
The start is refused for freshness and opens the step-up; complete it with the
SMS code from the `log sms:` log line, and the start is replayed. The step-up
offers SMS because the session already proved TOTP; that is the branch this
fixture exists to make reachable.

`TestReviewImpersonationTOTPFactorIsSeasonedAndSignsIn` is the guard on the
fixture being real: it seeds, reads the factor and its seed back through the
register and the encrypted serializer, asserts the register reports the factor
as seasoned, computes a code from the stored secret, and completes a
password-and-TOTP login with it. A renamed column, a changed status or a broken
serializer fails it. `TestReviewImpersonationStepUpOffersSMS` starts a step-up
after that sign-in and requires the SMS factor (and not TOTP) on the list.
`review_impersonation_isolation_test.go` holds the default build's half: the
SMS second factor is in the capability ceiling every build compiles in, while
`authn.impersonation.max-auth-age` still declares five minutes.

### The token signing key

`authn.token.signing-key-file` names `var/token-signing-key.pem`, a path relative
to the working directory. The program generates an ES256 key there on its first
run and keeps it afterwards: a key regenerated on every start would invalidate
every token already issued. The path is under `server/var/`, which `.gitignore`
keeps out of the tree, so a generated key is never committed. A deployment replaces the file
with its own key material — the token package's `GenerateKey("ES256")` mints one
to start from, and the token module itself neither generates nor rotates a key.

### The seed guard

`server/seed_guard.go` carries a module both the `serve` and `seed` rosters
name. Its `Prepare` refuses the run when the effective seed accounts come from
the defaults document while any configured listener is not loopback: the
compiled seed is the development operator the walkthrough publishes, and a
deployment that listens off the machine must not write it or serve it. Where the
account list came from is `config.EffectiveLayer` over `seed.accounts`, so the
check reads the layer and never the credential; when that query cannot answer
about the reader, it compares the effective account list with the compiled one.
The listener question is the classification the review build's own guard uses
(`server/listener_address.go`), shared outside either build tag. The refusal
names the seed, the listener and the two ways out: override the seed, or listen
on a loopback address. `server/testdata/` and the review configuration are
override documents with loopback listeners, and the Fly template replaces
`seed.accounts` and `seed.tenants`, so every shipped configuration carries on.

### Replace before serving real traffic

`server/defaults.go` holds the platform topology and nothing else: it carries
no credential and no value that differs between deployments, so a copy deploys
it unchanged. It is written in Go and encoded to JSON in its `Data` method, so
the document has no separate file to drift from and costs the host no parsing
dependency. The configuration files under `server/testdata/` are override
documents over it, and they ship the development values a copy replaces so it
starts as it stands. Every one of them is marked `DEV` in the file it appears
in; replace them all before a deployment serves real traffic. A sample's value
is that sample's own and does not stand for a product default: the defaults,
the site name in the authenticator and passkey prompts included, live in
`server/defaults.go` and the deployment template, and the samples keep whatever
value they were written with:

- **The initial operators** (`seed.accounts`) are the accounts the `seed`
  command writes. Replace every one of them before a deployment serves real
  traffic. The sample configurations give each a plaintext password, which is
  the one development value here written in the clear; a deployment that will
  not carry that gives a `password-hash` instead — a PHC entry, the form
  `pkg/authn/static`'s `Hash` helper writes. A seeded account signs in through
  the login flow (`authn.login.password`) and the session cookie it hands out,
  not through an Authorization scheme. An identifier without an `@` is a
  username, which needs no proof; the baseline `testdata/` files name one,
  because an email address the host writes is an administrator's assertion
  rather than the holder's proof, and the baseline ships no mail channel. A
  stack that can deliver a code (the review build, the Fly image) may name an
  email address instead, and such an entry names a `username` beside it: every
  account is created with a username, and the username is the name the password
  login accepts. The address is written unproved, so it is neither a login way
  nor a default contact until its holder proves it and selects it on the
  account page.

  **The review build proves its operator's address during the seed; the Fly
  image's operator starts unverified.** The review build's configuration
  (`server/review/sqlite.yaml`) names an email address for the operator, and the
  review seed proves it as part of the impersonation fixture (see "The review
  build's impersonation fixture"), so management writes answer from the first
  sign-in. The Fly image's rendered configuration (`deploy/fly.yaml.tmpl` fills
  `seed.accounts` from `DEMO_OPERATOR_EMAIL`) asserts an address rather than
  proving it: the account reads `verification.state = pending` and every
  management human write answers 403 `contact-required` until its holder proves
  it. The first sign-in after a deployment upgrade therefore has to prove the
  address: open the account page's verification section, send a code to the
  operator's address, read it from the mailbox the Fly image's SMTP relay
  delivers to, and submit it. Management actions are allowed from then on. This
  is the expected state of an administrator-provisioned email, not a fault: the
  rule treats a provisioned address, an imported one and a self-registered one
  alike. A deployment therefore has to point `DEMO_OPERATOR_EMAIL` at an address
  that can receive mail, since no other path proves it, and set
  `DEMO_OPERATOR_USERNAME` to the name the operator signs in with. Proving the
  address does not make it a login way: the operator signs in with its username
  in both builds, and the review build's proved address is likewise not a login
  way until its holder selects it.
- **The database encryption key** (`db.<engine>.encryption-key`) seals the
  columns a module encrypts at rest. Replace the `dev-only-change-me-…` value.
- **The lowered factor age** (`seed.settings`:
  `authn.mfa.sensitive-min-age: 1h`) exists only so the impersonation path can be
  exercised on a local copy; the declared default is 24h and this is the
  setting's lower bound. A deployment removes that seed entry or restores the
  declared default before serving real traffic.

## Layout

| Path | What it is |
|---|---|
| `Makefile` | The product's own build entry point: `make help` lists it, `make check` runs it. What a copy uses; inside the repository the root Makefile is the entry point |
| `server/main.go` | The command tree, the deployment-stack table, the baseline inclusion lists, the `gen` command, the placeholder command group, and `run()` |
| `server/host.go` | The environment prefix, the host's own module (the `config.HostIdentity` and defaults-document resources), and the endpoint constants |
| `server/capabilities.go` | The compile-time capability ceiling: every other capability compiled into every build and named by `serve` |
| `server/defaults.go` | The platform defaults document, written in Go and encoded to JSON, layered below every configuration file |
| `server/defaults_seed.go` | The seed section of that document |
| `server/seed_guard.go` | The startup guard that refuses the compiled demonstration seed on a non-loopback listener |
| `server/listener_address.go` | The loopback classification both startup guards use |
| `server/signingkey.go` | The token signing key's path, and the first-run generation that puts a key there |
| `server/modules/` | The slot for this product's business modules |
| `server/testdata/` | One override document per deployment stack: the deltas from `server/defaults.go` |
| `server/dist/` | The frontend build output, embedded by the Go program |
| `web/` | The frontend application's source and build; it writes into `server/dist` |
| `compose.yaml` | The four external backends the non-memory stacks run on |
| `Dockerfile` | The container image: the frontend workspace built first, then the Go program that embeds it. Built from the repository root, not this directory |
| `fly.toml` | The Fly.io deployment: one Machine, the volume, the two checks |
| `deploy/` | The container entrypoint and the configuration template it renders at start |
| `.gitignore` | Keeps the runtime state (`server/var/`) and the build output (`server/dist/`) out of the tree |

## Running it

The binary is one program on six stacks. The stack is a level of the command
tree, and everything below a stack runs on it, so every command is written
`demo <stack> <command>`.

A run has to be pointed at a configuration. The locator is a URI, and its scheme
selects the transport, so a local file is written `file:///path/to/config.yaml`
— the scheme is not optional. It is given on the command line with `--config`,
after the command or before it, or through the environment variable
`DEMO_CONFIG`.

Some of the configuration's values are paths relative to the working directory —
the token signing key, the object store's root, the sqlite stack's database — so
run the program from its module directory, `starters/demo/server`. The `sqlite`
stack needs nothing installed and keeps what it is given, so it is the one to
start with: seed the operator it ships with, then serve.

```
# from the repository root; -C runs the program with its module directory as the working directory
go -C starters/demo/server run . --config file:testdata/sqlite.yaml sqlite seed
go -C starters/demo/server run . --config file:testdata/sqlite.yaml sqlite serve
```

Or build once and run the binary from inside `starters/demo/server`, so the same
relative paths resolve:

```
go -C starters/demo/server build -o ./demo .
# then, with starters/demo/server as the working directory:
./demo --config file:testdata/sqlite.yaml sqlite seed
./demo --config file:testdata/sqlite.yaml sqlite serve
```

`migrate` runs the stack's database engine alone and applies every module's
declarations; `serve` runs the whole lifecycle and accepts traffic. The program
writes a `ready` record on standard output once every module has started and a
`stopped` record as it leaves, so a supervisor (or a test) that needs to know the
process is up waits for the record and not for a listening socket. Stop a run
with SIGINT or SIGTERM; it drains in-flight requests first.

`seed` writes the initial operators `seed.accounts` names, always registers the
system tenant, registers the business tenants `seed.tenants` names with their
administrators, writes the memberships `seed.members` names, and writes the
runtime settings `seed.settings` names. At least one account must carry
`platform-admin: true`: the command refuses the account list otherwise and
names `seed.accounts`, because a console with no platform administrator has no
path back. An account marked `platform-admin` becomes a member of the system
tenant and holds `platform-admin` in the platform domain (the system tenant's
domain) plus `tenant-admin` in the system domain; a business tenant never holds
`platform-admin`, which exists only in the platform domain. A tenant entry gives
`name` and `admins`, and may also give `settings`; every named administrator
gets a membership and the tenant-admin role in that tenant's domain, and the
first entry is written first.
The system tenant must not be listed under `seed.tenants`: the command
registers it itself and refuses an entry naming it. A `personal-` prefixed name
is refused too: the list names the deployment's business tenants, and personal
tenants are built from account creation. A member entry names a
tenant under `seed.tenants` and an account under `seed.accounts`; it is an
active membership unless it carries `suspended: true`. The two membership rules
differ: a `seed.members` row is written only while the run creates its account,
so an operator's add, suspend, resume or remove survives a later run; an
administrator named under a tenant entry (and a platform administrator's system
membership) is reconciled on every run — a missing membership is written and a
missing tenant-admin binding is added — because the configuration names that
account an administrator and the binding has no effect without the membership.
A setting under `seed.settings` is written into the global layer, and one under
a tenant entry into that tenant's layer, only while that layer holds no explicit
row for its key, so a value an operator changed through the settings surface
survives a later seed run. It carries the migrations with it, so it is
the whole of what a fresh copy does to a database before serving from it; a run
with nothing to reconcile writes nothing and says so. A permission a module declares
after an earlier run is added on the next run through the host's seeding
surface, so each administration role keeps everything the process declares while
the management surface stays unable to change it. The command names the personal
tenant module too, so its account-creation hook builds every seeded account's
personal tenant at seed time when `workspace.personal.enabled` is on; with the
switch off the hook builds nothing. The run has no HTTP entry point: the
personal module skips its backfill route and the audit journal skips its
request-level layer, while the account lifecycle hooks and the statement-level
audit rows keep working. Routes belong to the serve command. The tenant-admin role the seed writes in a
business tenant is builtin; the personal tenant's tenant-admin is not, because
the host adds its grants with the seeding surface and revokes the ones outside
the allowlist with the manager.

`seed` writes no second factor in a baseline build, and none in a review build either unless the deployment sets `seed.impersonation`: that switch seeds the impersonation journey's two judged methods on the platform administrator, one aged past `authn.mfa.sensitive-min-age`, and it changes how that administrator signs in afterwards. The public `pkg/authn`
surface hands a host a registration step it does not let that host read, so a
baseline seed cannot finish an enrollment; the console's step-up gate therefore
refuses every write until an operator adds a factor in account settings and
signs in again, completing that factor at login, and `seed` says so on its way
out. The alternative — writing the factor's sealed row directly — would bind
this skeleton to the factor module's internals. The review build is the
exception, and writes its factors through the modules' own registers instead
(see "The review build's impersonation fixture"): the note is absent there.

### Exercising a write path

Register the factor through the UI first: sign in, open the account page, start
two-step-verification enrolment, and confirm it with a code. Then sign out and
sign in again, completing the second factor at login. Only that second session
admits a write: enrolment does not raise the assurance of the session it
happened under, so a write attempted right after enrolling is still refused —
in a session that is no longer fresh, even the enrolment itself is refused by
the step-up gate. The factor is proven at the next login, and that login's
local multi-factor is what the write path accepts. A step-up cannot substitute
for that second login: it accepts only a factor enabled before the session's
authentication time, so the factor just enrolled under the current session is
not offered to it. The design settles the order the same way: a second factor
is satisfied at login.

The wizard renders the `otpauth://` URI as text; take its `secret` parameter
and compute the current code with a standard-library one-liner:

```
python3 -c "import base64,hmac,hashlib,struct,time; s=base64.b32decode('SECRET'); c=int(time.time())//30; h=hmac.new(s,struct.pack('>Q',c),hashlib.sha1).digest(); o=h[-1]&15; print(str((struct.unpack('>I',h[o:o+4])[0]&0x7fffffff)%1000000).zfill(6))"
```

The parameters are the design's: SHA-1, six digits, thirty-second period.

Two timing notes. A code cannot be replayed inside its window — the counter
must advance — so wait for the next window before a second sign-in that
completes the factor. And once a sign-in completes the second factor, the
session counts as freshly elevated for the configured recent window (ten
minutes by default), so a batch of writes can be exercised on one sign-in.

Impersonation needs more than the factor. Start with two accounts, write the
target into the tenant on the invitations-and-members page, and wait until the
factor has been active for `authn.mfa.sensitive-min-age` (one hour here, twenty
four by default) — a step-up cannot shorten that, because it is the factor's
age rather than the session's assurance that is refused.

Seeding is a step of its own rather than something `serve` does on its way up,
because a deployment nobody may administer until somebody says so is the posture
this skeleton ships. `serve` requires the system tenant the seed registers, so
the seed must run first on every stack. On `sqlite-memory` the step reaches a
later serve only if both commands point at the same database file: that stack's
`:memory:` locator belongs to the process that opens it, and seed and serve are
two processes. The suite therefore gives the stack a file locator; the stack to
sign in on is `sqlite`.

`version` answers with the program's version and assembles nothing:

```
go -C starters/demo/server run . version
```

`gen` writes the merged API document. Like every command under a stack it reads
that stack's configuration, so it is pointed at one too; it binds no port and
constructs no module, so the configuration it reads is never connected to:

```
go -C starters/demo/server run . --config file:testdata/sqlite-memory.yaml \
    sqlite-memory gen --title demo-api --version 1.2.3 -o /tmp/openapi.json \
    --features /tmp/features.json
```

The `serve` command's configuration is the sample under `server/testdata/`. The
four `postgres` stacks differ from the two `sqlite` ones in the backend section
alone, and `compose.yaml` brings those backends up:

```
docker compose -f starters/demo/compose.yaml up -d
go -C starters/demo/server run . --config file:testdata/postgres.yaml postgres migrate
go -C starters/demo/server run . --config file:testdata/postgres.yaml postgres serve
```

### Deploying to Fly.io

`Dockerfile`, `fly.toml` and `deploy/` are one deployment: the `sqlite` stack on
a single Machine, with a volume at `/data`. The stack reaches no network for its
backends, so the image, the Machine and the volume are the whole topology. The
volume is what keeps the database, the token signing key and the object store
across deploys and machine stops.

The image is built from the repository root, because it compiles the frontend
workspace under `web/` and the Go workspace `go.work` declares; neither is
inside this directory. `fly.toml`'s `[build] dockerfile` is relative to that
file. The root `Makefile`'s `fly` target passes the root as the working
directory.

**A volume written before the contact-reservation migration is not upgraded.**
The account module's `0003_contact_claim.sql` adds the global unique index on
`authn_pending_contacts` and performs no data migration, so a database whose
existing rows break that index cannot apply it. Recreate the deployment with an
empty volume (`flyctl volumes destroy`, then create it again) and let `seed`
write the accounts and settings once more; the token signing key and
the object store live on the same volume and are recreated with it.

**One-time setup.** The names below are the file's; a fork changes `app` and the
volume's `source` together.

```
flyctl apps create vislake-speed-demo
flyctl volumes create speed_demo_data --app vislake-speed-demo --region sin --size 1
flyctl secrets set --app vislake-speed-demo \
  DEMO_OPERATOR_USERNAME=operator \
  DEMO_OPERATOR_EMAIL=operator@example.com \
  DEMO_OPERATOR_PASSWORD='...' \
  DEMO_DB_ENCRYPTION_KEY='...' \
  DEMO_SMTP_HOST=smtp.example.com \
  DEMO_SMTP_FROM='Demo <no-reply@example.com>' \
  DEMO_SMTP_USERNAME='...' \
  DEMO_SMTP_PASSWORD='...' \
  DEMO_GOOGLE_CLIENT_ID='...' \
  DEMO_GOOGLE_CLIENT_SECRET='...' \
  DEMO_GITHUB_CLIENT_ID='...' \
  DEMO_GITHUB_CLIENT_SECRET='...'
# optional: DEMO_SMTP_PORT (default 587), DEMO_SMTP_TLS (starttls, implicit, none)

# the repository secret the deploy workflow reads
gh secret set FLY_API_TOKEN --body "$(flyctl tokens create deploy --app vislake-speed-demo)"
```

The deployment carries no development credential. The entrypoint refuses to
start without the operator's username, address and password and the database
encryption key, so the DEV values the sample configurations ship cannot reach a
Machine. `DEMO_OPERATOR_USERNAME` is the name the password login accepts, held
to the account module's username rules: a reserved name or a value outside the
allowed character set fails the seed. `DEMO_OPERATOR_PASSWORD` is held to the
account store's password policy: the declared minimum is the domain's floor of eight
characters, so the operator credential only has to meet that. A deployment
that raises `authn.account.password-min-length` through `seed.settings` must
also raise the credential; the seed writes the setting before it creates the
account, so the policy binds the run that writes the credential. A value
outside the policy fails the seed on the first start. `DEMO_DB_ENCRYPTION_KEY`
is at least 14 bytes and must stay the same for the life of the volume: it
seals columns at rest, and a different key on an existing database makes them
unreadable.

**Registration is open on this deployment.** The image is the default build,
which compiles every capability in and lets the rendered configuration decide
which ones start; `seed.settings` opens
`authn.account.password-signup` and `authn.account.signup`; and
`workspace.personal.enabled` is on, so an account that proves its email address
gets a personal tenant. The email loop needs a real relay: the entrypoint
requires `DEMO_SMTP_HOST` and `DEMO_SMTP_FROM`, and treats
`DEMO_SMTP_USERNAME` and `DEMO_SMTP_PASSWORD` as optional (empty means no
AUTH). `DEMO_SMTP_PORT` defaults to 587 and `DEMO_SMTP_TLS` to `starttls`. The
entrypoint refuses a value carrying a double quote, a backslash, a line break
or a control character, because the value is rendered inside a YAML scalar.
Both registration routes are open on a public deployment, so the two per-source
budgets keep the platform defaults rather than the demo seeding its own: a source
address may open ten accounts a minute (`authn.account.signup-ip-limit`) and
send sixty verification messages a minute (`authn.login.ip-limit`). The mail
budgets are the declared defaults too: a walk-through may need to wait out the
per-address window. The baseline testdata and the review build name their own
capabilities in their own configuration, so their runs keep their own feature
sets.

**The anonymous password reset is open on this deployment too.**
`seed.settings` sets `authn.login.password-reset` to `open`, and the image's
`authn.emailcode` provides the reset seam over the same SMTP carrier, so the
sign-in surface offers the forgot-password entry and `/password-reset` offers
the email channel. The image assembles no SMS family, so the reset page offers
email alone; a deployment that adds the SMS modules and declares the optional
`authn.code.reset` template gets the SMS channel beside it.

**Google and GitHub sign-in are on the same deployment.** Each application's
identifier and secret come from its own Fly secret; the entrypoint requires all
four, so a deployment that leaves one out refuses to start rather than serving a
method with no credential. A first social login opens an account that carries
only the provider's display name: the address the provider reports is not
written as an identifier, a login way or a contact. Both providers hold a
redirect URI on the deployment, and it is derived from the origin rather than
configured:

```
https://<app>.fly.dev/api/v1/auth/callback/google
https://<app>.fly.dev/api/v1/auth/callback/github
```

A custom domain (`DEMO_PUBLIC_URL`) changes both, so register the pair on the
domain the deployment is reached at. An account that already exists under a
password signs in socially only after it binds the provider in its account
settings: a social login whose provider-proved address matches an existing
account answers `link-required` rather than taking the account over, and a
first social sign-in opens an account that does not carry the provider's
address.

The origin is derived from `FLY_APP_NAME` (`https://<app>.fly.dev`). A custom
domain sets `DEMO_PUBLIC_URL` to its https origin, which is what the WebAuthn
relying party and the gateway's trusted origins are bound to.

Every request reaches the Machine through Fly's proxy, which is the one peer
whose forwarded headers the deployment believes: the template names
`172.16.0.0/16` as the trusted network and reads the client address from
`Fly-Client-IP`. "Behind a reverse proxy" below states the same two settings for
other deployments.

**What a start does.** `deploy/entrypoint.sh` renders
`deploy/fly.yaml.tmpl` into `/data/config.yaml`, replacing the public origin,
its domain and the operator's username, address and password; it takes
ownership of the volume and drops to an unprivileged user; it runs `sqlite
seed` and then hands the process
to `sqlite serve`. Seeding on every start is safe by the command's own contract:
it writes only what is absent, so a restart neither duplicates nor overwrites an
operator's change. `serve` carries the platform baseline's background modules,
so no second process runs.

The template states the endpoint claim table because this deployment keeps the
operations surface on a listener of its own: the compiled defaults put every
endpoint, the ops probes included, on the backend listener, and `fly.toml`'s
readiness check probes `GET /ready` on port 8083. A template that named the
listeners alone would leave that listener with no endpoint claiming it, so the
check would have nothing to reach. The table is written whole because
`http.endpoints` is a container leaf.

The seed keys an account on the login name its entry configures, and an entry
that names both an email address and a username answers to either: the address
is looked up first, then the username beside it. A holder who removes the
seeded address from the account page leaves the account under its username, so a
later start adopts that account as skipped instead of creating a second one, and
the removed address is not written back — the holder's change stands. Only the
account entry has that second name: a `tenants` administrator entry and a
`members` entry name their account by the login name their own entry writes, so
an entry that writes the address names the account by the address.

**Checks.** A rolling deploy gates on `GET /` on the backend listener, which
answers with the embedded bundle's entry document. The readiness signal is the
operations endpoint's `GET /ready` on port 8083, declared as a top-level check
because the operations listener is not a service; top-level checks report
Machine health and gate no routing.

**Deploying.** `.github/workflows/deploy.yml` runs `make fly` when the
`check` workflow has completed successfully on `main`, and on request. It
installs the pinned flyctl and builds the image on Fly's remote builder. The
repository secret `FLY_API_TOKEN` is the account credential; the one-time setup
above scopes it to the app.

## Endpoints and guards

The product declares five endpoints, and what this program says about them is
which class of traffic belongs where:

| Endpoint | Traffic |
|---|---|
| `api` | the product's own API, scoped to a tenant |
| `account` | a user who has not chosen a tenant yet |
| `admin` | the platform's operators |
| `ops` | probes and documents; `GET /health`, `GET /ready`, `GET /openapi.json` |
| `web` | the frontend bundle; its catch-all route covers the endpoint's whole path space and it carries no other route |

### What each endpoint claims

The dispatch selects an endpoint by the path prefixes the configuration gives
it under `http.endpoints.<name>.prefixes`; the registered routes take no part in
the choice. `server/defaults.go`, compiled into the binary, declares the
table:

| Endpoint | Claims |
|---|---|
| `api` | `/api` |
| `account` | `/api/v1/auth`, `/api/v1/features`, `/api/v1/workspace` |
| `admin` | `/api/v1/system/audit`, `/api/v1/system/auth`, `/api/v1/system/permissions`, `/api/v1/system/settings`, `/api/v1/system/sms`, `/api/v1/system/tenants` |
| `ops` | `/health`, `/ready`, `/openapi.json` (declared explicitly: the endpoint shares the backend listener, whose root the `web` endpoint claims) |
| `web` | `/` |

The table is the product's fixed namespace map, not a description of the
current run: a capability dropped from `baselineServe`, or removed from the
imports altogether, keeps its namespace claimed. `sms.admin` is the example —
the binary imports it for the contract, the baseline runs it in no command (the
review build's `serve` runs it), so `admin` claims `/api/v1/system/sms` even
though the baseline answers no route there. A path under a claim reaches
that endpoint's chain even when no route matches: with no credentials the
gateway refuses it 401, and an admitted caller gets the endpoint's own 404. A
frontend page path must stay outside every non-root claim, or a page navigation
would receive that endpoint's answer instead of the entry document.

Two tests hold the table. `server/claims_test.go` holds every operation of the
served `/openapi.json` and of the committed `contracts/api.json` to it, so an
operation whose namespace lost its claim is reported; it reads the table through
the same loader the binary uses, so it checks the effective configuration —
defaults document and file together — rather than a file's own copy.
`server/differential_test.go` removes routing modules from `baselineServe` one
group at a time and compares every anonymous answer of the variant with the
baseline's — byte for byte apart from `Date` — on both deployment topologies.

### The defaults document and the override documents

`server/defaults.go` is the platform topology: the endpoint table, the endpoint
each capability mounts on, the listener addresses a bare run binds, the
development seed, and the request list the journal records. It is written in Go
and encoded to JSON in its `Data` method, and registered as a `config.Defaults`
resource on the host module, so the loader layers it below the configuration
file a run is pointed at and above each module's prototype defaults. A bare run
needs no configuration file at all: `demo sqlite serve` with no `--config` and
no `DEMO_CONFIG` starts on this document alone. What belongs in it is what every
deployment of this binary shares; a credential, an address, an origin, a path on
disk or a seed is a deployment value and lives in the configuration file.

The configurations under `server/testdata/` (and `server/review/sqlite.yaml`)
are therefore override documents: each carries the deltas from `defaults.go`,
the values their deployment chooses, and the backend section at the bottom — not
a second copy of the whole platform table. A copy of this skeleton changes the
delta, not the table.

One rule shapes how far a file can shrink: a **required** input item is
satisfied by the defaults document, and a prototype's field value is not. The
document is the host naming a value on purpose, the same act as writing it in
the configuration file; a prototype value always holds something, so it could
not tell "nobody gave a value" from the zero one. The mounting table therefore
lives in `defaults.go` alone, and no configuration file repeats it.

What a file must still state is the values the document must not carry:

- `http.cors.endpoints`, because the CORS module refuses an empty table when it
  resolves rather than marking the item required in its declaration, and each
  policy needs at least one allow-origin because the library refuses one that
  disables every origin. Its contents name the origins this deployment is
  served from, which differ between deployments.
- `objectstore.fs.root`, which is required and is a path on disk: where a
  deployment keeps its bytes differs per deployment.
- `observability.otlp.endpoint`, which is required and names the collector the
  deployment exports to. `defaults.go` carries no `observability.otlp`
  section: only the `postgres-redis-nats` stack exports through OTLP, and the
  stacks that export to stdout must not start pointed at a collector that is
  not there.
- every credential: a seeded account's password or `password-hash`, the
  database encryption key, a provider's client secret. A credential never
  belongs in a document compiled into the binary.

The required items the document carries — `webui.endpoint`,
`feature.board.endpoint`, `tenant.resolve.endpoints`,
`tenant.resolve.extractor`, `tenant.admission.endpoints`,
`tenant.admin.endpoints`, `authz.guard.endpoints`, `authz.admin.endpoints`,
`authz.self.endpoints`, `audit.admin.endpoints` and
`settings.admin.endpoints` — need not appear in a file. A key in the document
is overridden by the same key in the file, and a container leaf such as
`http.endpoints` is replaced whole rather than merged: the split topology the
differential case exercises writes the full table to move `web` onto a listener
of its own.

Every endpoint hangs on one listener, which each configuration file
declares: `api`, `account`, `admin`, `content`, `web` and the operations
probes share the `backend` listener. A listener is one socket, so the frontend
bundle and the API are same-origin and a browser reaches the API from
the page without CORS. The `http.cors` policy the sample carries is therefore
inert in this stack; it is kept as the starting point for a deployment that
serves the frontend from an origin of its own. A deployment may split the
listener again; the differential case's split topology does, moving `web` and
`ops` onto listeners of their own.

Sharing a listener with the `web` catch-all decides what a path under no
endpoint's static prefix is answered with. On the `backend` listener the `web`
endpoint is the fallback, so such a path is served the frontend bundle. A path
that falls under an endpoint's prefix is not left to the bundle: that endpoint's
chain answers it even when no route registered the path. A mistyped path under
the api endpoint's prefix — `/api/v1`, which its routes establish — therefore
reaches the api endpoint, not the bundle: with no credentials the gateway
refuses it 401, and once admitted the api endpoint answers its own 404 (or its
405 where the path exists but the method is not allowed).

The baseline now ships the guards each endpoint carries; `server/defaults.go`
names the fixed part of that wiring, and no configuration file repeats it:

- The gateway (`authn.gate`) guards `api`, `account` and `admin`. Its ordinary
  routes — login, session, registration, profile — mount on the `account`
  endpoint under `authn.gate.mount.bases.default` (`/api/v1/auth` by default).
  Its platform-management routes — the account-management routes, the enterprise
  provider register and the impersonation records — mount on `admin` under
  `authn.gate.mount.endpoints.system` and `mount.bases.system` (`/api/v1/system`
  by default), because they are decided in the platform domain and the endpoint
  is outside both tenant layers. An impersonation record's path carries the
  tenant (`/api/v1/system/tenants/:tenant/impersonations`), but the tenant is
  the data the record is confined to, not the policy domain. The
  tenant-management routes — membership, invitations and identity providers —
  mount on `api`, under `authn.gate.mount.endpoints.tenants` and
  `mount.bases.tenants` (`/api/v1/tenants` by default), because they are decided
  in that tenant's policy domain and have to meet `tenant.resolve` and
  `tenant.admission`; the endpoint's `/api` claim covers the namespace. The
  login flow is delivered as a
  session cookie: the frontend's login flow
  requests `delivery: 'session'` (and `@speed/auth-core` forces it), so a
  completed login answers `{state: 'authenticated', session: {id, expires_at}}`
  with a `Set-Cookie` and no token pair, and a page reload stays signed in by
  re-reading `GET /api/v1/auth/session`. The delivery set is a runtime setting
  (`authn.login.delivery-modes`, default `["session","token"]`), not a
  configuration item, so it is narrowed through the settings surface rather
  than `testdata/`; this host's frontend already requests session for every
  login.
- Tenant resolution (`tenant.resolve`) and admission (`tenant.admission`) guard
  `api`; the authorization guard (`authz.guard`) enforces `api`; the rate
  limiter's quota layer (`ratelimit`) limits `api`, and its address shield
  (`ratelimit.address`) guards `account` -- a coarse per-source request cap
  before identity is settled, not a per-recipient send limit. Admission's list
  is a subset of resolution's. Resolution reads
  the tenant from the request's `X-Tenant` header (the header extractor
  `tenant.resolve.extractor` names; the header name itself is the module's own
  default), so an `api` call without that header is refused 404 -- the tenant is
  not a claim of the access token, it is a request-time routing fact the caller
  must announce. The frontend does exactly that: the client built in
  `bootstrapSpeedApp` announces the host's current tenant as `X-Tenant` (its
  `tenantProvider`, wired from `starters/demo/web`), read per attempt.
- The platform's management surfaces mount on `admin`: the roster
  (`tenant.admin`), the authorization catalog (`authz.admin`), the settings
  surface (`settings.admin`) and the journal's query routes (`audit.admin`).
- The tenant-management route set decides each action in its handler through
  `authn.Decider`, so `server/management.go` (`management.access`) registers it
  open on the authorization guard's declaration surface: `api` carries the
  guard, whose closed default would otherwise refuse every one of those routes
  403 before a handler ran. `claims_test.go` holds the list to the committed
  contract in both directions.
- The caller's own permission read (`authz.self`) mounts on `api`: every
  authenticated caller may read the permissions it holds in the platform domain
  and in the request's tenant, and the frontend attaches both sets to the
  session from that answer.
- The feature document (`feature.board`) mounts `GET /api/v1/features` on the endpoint
  `feature.board.endpoint` names — `account` in `defaults.go`, which
  the gateway guards, which resolves no tenant, and whose listener claims
  `/api/v1/features`. It publishes only to authenticated callers: a process with no
  authentication gateway fails to start rather than expose the document, and an
  anonymous request receives the gateway's own 401. The document names what this
  process serves, so `sms.admin` — imported for the contract and run by no
  baseline command (the review build's `serve` runs it) — is absent from the
  baseline's document.
- The notification surface (`notification.inbox`, `notification.stream`,
  `notification.preferences`) mounts on `api`. The capability declares no
  permission point of its own, so its routes carry no gate and the authorization
  guard, whose default is closed, would refuse each one 403. The host closes that
  gap itself: `notification.access` (`server/notification.go`, named in
  `baselineServe`) registers the notification routes open on the guard's
  declaration surface, so the gateway still authenticates, the guard admits, and
  each handler answers for the caller. They are open rather than on a permission
  because they are scoped by the caller's identity — the inbox and the stream
  read the caller's own account — and not by anyone's permission. The preference
  routes read a recipient the host names on the request context
  (`preferences.WithRecipient`); the baseline declares no notification type and
  names no recipient, so the matrix is empty and that step does not arise. The
  review build is the worked example of both: it declares a type and names the
  caller as the recipient (see "The review build's notification fixture").
- **The personal tenant** is the host's own policy, not a platform capability
  (`workspace.personal`, `server/personal.go` and `server/personal_lifecycle.go`,
  named in `baselineServe`). Its switch is `workspace.personal.enabled`, off in
  every `testdata/` file and on in the review assembly
  (`server/review/sqlite.yaml`). With it on, the module implements
  `authn.AccountLifecycle.AccountCreated`: the account is committed and then its
  personal tenant, membership and tenant-admin binding are built, so no account
  exists without a tenant. With the switch off the hook builds nothing, no route
  is mounted, the feature is absent from the document, and the claimed
  `/api/v1/workspace` prefix answers the account endpoint's 404. A service
  account has no personal tenant, and a deleted account (or an existing tenant
  tombstone) is no work. The status and deletion hooks stay in force whether or
  not the route is mounted, because they wind up a tenant an account already
  has.

  The route `POST /api/v1/workspace/personal` on the `account` endpoint is the
  backfill: a caller whose own personal tenant is missing from its membership
  list asks for one. A membership in another tenant is not a refusal — every
  account owns its personal tenant — so the backfill creates it beside them; an
  existing personal membership is read back without rewriting it. The route
  requires an authenticated caller and settles it for the effective account: it
  is registered `Impersonable`, so an impersonating identity reaches the handler,
  which builds or reads back the impersonated account's personal tenant rather
  than answering the gateway's default 401. It refuses a service account or a
  static-credential principal 403.
  Registration verification is not an eligibility fact: an unverified account
  gets its personal tenant, and the verification gate limits what it may do
  inside the workspace. The idempotent pieces are the tenant row (identifier and
  alias both `personal-<account-id>`), the account's membership, a non-builtin
  `tenant-admin` role, that role's grants converged to the allowlist the host
  declares (the host's business-resource set less membership, invitation and
  identity-provider management — the set is empty here because neither the
  notification surface nor the object store declares a permission point), and
  the account's binding. It never overwrites an operator's decision: an existing
  membership is left alone, and a tenant row whose status is not enabled answers
  409 `tenant-unavailable` without writing anything. An alias collision or a
  derived identifier outside the roster domain answers 409
  `identifier-unavailable`. A dependency failure is a 503 with no part of the
  result. The two 409s carry `urn:speed:workspace:<code>` types, so a frontend
  that must render them by code can distinguish them. `personal.go` demonstrates
  that an application module's refusal a product frontend must render by code
  needs that shape, since `@speed/api-client` degrades every other problem type
  to `client.http.<status>`. The frontend calls the backfill from the tenant
  frame when an authenticated, non-impersonating viewer's own personal tenant is
  absent from its settled membership list and the feature is served, regardless
  of the account's verification state or its other memberships. The
  「完成账户设置」 notice is driven by the account's current profile showing
  setup incomplete, not by whether the backfill created anything. A registration
  completed through SMS lands on `/` (the tenant home), and without the feature
  it lands on `/account/setup`; a password registration lands on
  `/account?verify=1` in either case, and the verification notice is rendered by
  the account surface independently of the backfill.
- **The tenancy policy seams** are the host's answers to what the tenant and
  authorization capabilities leave open (`server/tenancy.go`, named in
  `baselineServe` and in the `seed` command). The module provides
  `authz.MembershipChecker` (whether an account is an active member of a tenant,
  read from `authn.Memberships`; a subject of the static credential table is the
  exception — it is an operator the deployment configured, holds no membership
  row and answers true), `tenant.WriteGuard` (the system tenant and every
  personal tenant reject a disable or a delete), `authn.MembershipGuard` (a
  personal tenant refuses every membership removal, and an ordinary tenant
  refuses the removal that would leave it with no tenant-admin binding; its
  `Lock` takes the host's `authz.TenantLock` — an in-process per-tenant
  semaphore on SQLite, a session-level advisory lock on PostgreSQL — so the
  check and the removal's commit are one serialised step),
  `authz.TenantLock` (the same object the policy module takes as its binding
  delete's lock, so `authz.Manager.DeleteBinding` and both guards exclude each
  other; `MembershipLock` answers this product so the account module takes the
  one object once for a removal), and `tenant.Provisioner` (a tenant opens with
  its creator as its first tenant-admin: the tenant-admin role, every declared
  permission and the creator's binding are written before the roster row, and a
  refused roster row removes them).
  `tenant.admin` requires the guard and the provisioner, and the policy module
  consults the checker before binding `platform-admin` in the platform domain,
  so a serve assembly names this module or does not start. The membership guard
  stands beside the policy module's own: the account module asks every guard and
  any refusal refuses the removal. Account deletion does not go through that
  call, so it consults neither guard and can leave an ordinary tenant with no
  tenant-admin binding until a new one is written. The personal domain is
  recognised by the identifier prefix the host derives (`personal-`), and that
  prefix is this host's policy: the platform passes plain tenant identifiers and
  defines no tenant kind.
- **The policy write guard** (`authz.WriteGuard`) refuses every role, grant and
  binding write in a domain whose identifier starts with `personal-`. It is a
  module of its own (`host.tenancy.policy-write`, named in `baselineServe`)
  because `tenant.WriteGuard` and `authz.WriteGuard` both declare a `Denied`
  method, which one Go type cannot carry twice. The personal tenant's own
  tenant-admin holds no membership-management permission, so that space cannot
  grow a second member through either the membership route or the policy one.
- The journal (`audit.journal`) records the requests its `requests` list names
  on the endpoints its `endpoints` list names. The sample files name no request
  route (`requests: []`): the object store's routes are gone, and the writes a
  copy would otherwise sample are recorded at their own seams. A read the frontend polls on its own
  (the profile, the session, the impersonation status) would write a row per
  poll and bury the changes worth finding, and a route whose action the
  capability already records at the seam (login, impersonation, the
  account-management actions) would record one fact twice.
- `ops`, `web` and `content` carry no gateway: the operations surface serves
  probes and the document, the frontend bundle is static, and the content
  endpoint serves token-bearing byte addresses, where the token itself is the
  credential.

Which guards each endpoint carries is configuration, and where a module mounts
its routes — the two endpoint lists on the tenant layers, the guard's list, the
journal's requests — is a decision to revisit as a product's own routes grow.
`pkg/http` is where the endpoints and their chains are declared and configured;
the operations surface is `pkg/ops`, which mounts its three routes on the
endpoint `ops.endpoint` names and expects no guard there.

### Behind a reverse proxy

A deployment reached through a reverse proxy reads the client address from a
forwarded header, and does so only when the peer is a network the configuration
names. `http.trusted-proxies` and `http.remote-ip-headers` are module-level
defaults: every endpoint inherits both, and an endpoint overrides either by
naming its own under `http.endpoints.<name>`, an explicit empty list included:

```
http:
  trusted-proxies: ["127.0.0.1/32", "::1/128"]
  remote-ip-headers: ["X-Forwarded-For", "X-Real-IP"]
```

Both are empty by default, which trusts nobody: the client address is then the
connected peer, so no client can choose its own reported address with a header.
The setting decides which addresses are believed; it is not an authentication
boundary.

The shape is the same for every proxy, and only the values differ:

- **A proxy on the same host**, nginx included: trust `127.0.0.1/32` and
  `::1/128`. `remote-ip-headers` may be left out: the default order, which the
  snippet above writes explicitly, reads `X-Forwarded-For` and then
  `X-Real-IP`, the two headers nginx writes
  (`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;` or
  `proxy_set_header X-Real-IP $remote_addr;`).
- **An AWS ALB**: trust the CIDRs of the subnets the load balancer's nodes sit
  in, never the whole VPC — every host in a trusted CIDR may write the header.
  Read `X-Forwarded-For`, which the load balancer appends the client address
  to. An entry is skipped from the right while it is a trusted address, so the
  first untrusted one from the right is the client.
- **Cloudflare**: trust the ranges Cloudflare currently publishes — take them
  from Cloudflare's own list, which changes — and read `CF-Connecting-IP`. The
  origin must also restrict ingress to those ranges, or the trusted list only
  reaches as far as the host's firewall.
- **A container**: trust the network the proxy container is on. A default
  bridge shared with unrelated containers is a network those containers occupy
  too.

Trusting a range another workload also occupies hands the header to that
workload: each host in the range can claim any client address. List only
networks the proxy alone occupies, and take the narrowest one that covers it.

### Re-authentication and impersonation on a fresh copy

The baseline names two factor kinds, TOTP and WebAuthn, and the second one is
here for the step-up. A step-up (`purpose: 'step-up'`) asks for a factor whose
authentication methods are disjoint from the session's, so an operator who
signed in with password and TOTP carries a session that proved `otp` and cannot
raise itself with another TOTP. WebAuthn's method (`webauthn`) is disjoint from
it, so the step-up start answers a `factor` step offering the passkey, and its
completion elevates the session (`Sessions.Elevate`), re-running the step-up-gated
write the operator tried -- an account-status or factor write, or an impersonation
start.

Both halves of the WebAuthn ceremony are in the bundled frontend. `@speed/auth-ui`,
`@speed/account-ui` and `@speed/console-ui` each drive the browser's WebAuthn
ceremony (`navigator.credentials.create` for a registration, `navigator.credentials.get`
for an assertion) from the step's own public-key options and report the credential
it returns: the enrollment wizard registers a passkey, and the step-up dialog
presents an assertion, both against whatever platform or roaming authenticator the
browser has. The second factor kind -- WebAuthn beside TOTP -- is what makes the
step-up start answer a factor step rather than refuse for want of one.

Two things order a hand-run of the sequence. The passkey has to be enrolled
*before* the session that raises itself: a step-up accepts only a factor enabled
before the session's authentication time, so a passkey enrolled under a session
is not offered to that same session. And the write has to be attempted with a
session that is no longer fresh: the gate is a fresh local multi-factor, its
window is the setting `authn.mfa.recent-window` (ten minutes by default), and a
just-completed multi-factor sign-in satisfies it directly, so the dialog appears
only once the window has passed.

Impersonation carries one gate of its own: its actor must hold an active
non-SMS factor activated at least `authn.mfa.sensitive-min-age` before now. The
declared default is 24 hours; the sample configurations lower it to the
setting's domain minimum of 1h for local demonstration, and production keeps
the declared default. A factor enrolled minutes ago still cannot start an
impersonation — it has to mature for an hour under the samples — so a baseline
copy cannot demonstrate a started impersonation on the day it is seeded, and a
step-up does not shorten that wait. The review build is the one exception: its
impersonation fixture seeds the operator's factors and backdates the TOTP one
past the minimum, so the journey is walkable from a fresh review database (see
"The review build's impersonation fixture").

## The stacks

A stack names a database engine, a key-value backend, an event backend and a
telemetry export backend. They are built by one function (`addStack`), so what
tells two of them apart is the table in `main.go` and nothing else:

| Stack | Engine | Key-value | Event | Export |
|---|---|---|---|---|
| `sqlite` | `db.sqlite` | `kv.memory` | `event.memory` | `observability.stdout` |
| `sqlite-memory` | `db.sqlite` | `kv.memory` | `event.memory` | `observability.stdout` |
| `postgres` | `db.postgres` | `kv.postgres` | `event.postgres` | `observability.stdout` |
| `postgres-redis-nats` | `db.postgres` | `kv.redis` | `event.nats` | `observability.otlp` |
| `postgres-nats-redis` | `db.postgres` | `kv.nats` | `event.redis` | `observability.stdout` |
| `postgres-memcached-nats` | `db.postgres` | `kv.memcached` | `event.nats` | `observability.stdout` |

`sqlite` and `sqlite-memory` name the same modules and are still two stacks: the
database the first's configuration points at is a file under `var/`, and the
second's is the process's own, gone with the run that opened it. The table does
not show that difference and the configuration does — it is the one pair of rows
here whose distinction lives in `testdata/` alone. Sign in on `sqlite`; use
`sqlite-memory` to exercise the assembly and the probes, where nothing written is
meant to outlive the process.

The set is closed: a stack a product adds is a row in the `stacks` table, and a
configuration under `server/testdata/` to go with it.

The engine is named on the stack node, so every command under the stack runs it.
The key-value, event and export backends are named on the individual commands
instead, because `migrate` runs the engine alone: a backend named on the stack
node would run in `migrate` too, putting an input item and a reachability the
pre-deployment job has no use for.

## The commands

Under every stack:

- **`serve`** runs the whole lifecycle and accepts traffic: the entry point, the
  platform baseline (`baselineServe` in `main.go`), the stack's two backends and
  the telemetry providers.
- **`worker`** runs the background capabilities — the roster, settings, the
  audit journal, the job queue and its scheduler, and the outbox relay, beside
  the two backends and the telemetry providers — with no HTTP entry point
  (`baselineWorker` in `main.go`). The audit journal is part of the worker
  baseline because background paths write audit rows: the account-deletion
  cleanup and the personal tenant's create and delete in the event compensation.
  A run with no entry point serves no request, so request capture is skipped
  while statement- and record-level capture continue; an endpoint name the
  entry point does not declare is still a startup failure where the entry point
  is present. A module whose routes or request lists are read wherever it runs
  belongs to `serve`, which stands the entry point up.
- **`migrate`** runs the engine alone and reaches every module's declarations to
  build every table.
- **`seed`** writes the accounts under `seed.accounts`, each with the credential
  it is given — a plaintext `password` or a `password-hash` — always registers
  the system tenant, registers the business tenants under `seed.tenants` with
  their administrators, writes the memberships under `seed.members`, and writes
  the runtime settings under `seed.settings`. Each tenant entry gives `name`,
  `admins` and optionally `settings`; every named administrator gets a membership
  and the tenant-admin role in that tenant's domain, with the first entry written
  first, and each `settings` pair is written into that tenant's layer through the
  same store, so its key and value are held to the declaration as the global
  list's are. An account marked
  `platform-admin` becomes a member of the system tenant and holds
  `platform-admin` in the platform domain plus `tenant-admin` in the system
  domain; a business tenant never receives `platform-admin`, which exists only in
  the platform domain. A member names a tenant under `seed.tenants` and an
  account under `seed.accounts`, and it is active unless it carries
  `suspended: true`; a tenant or an account the configuration does not declare,
  a tenant with no administrator, and one pair declared twice fail the command
  before it writes anything, with the entry named. The system tenant is
  registered in the module's `Init`, before the policy module's `Start` checks
  the roster for it; it must not be listed under `seed.tenants`, because the
  command writes that row itself and an entry naming it would claim an
  administrator for a row the command already owns. The administration roles are created,
  their grants are written through the seeding surface (`authz.Seeder`), and they
  are then marked builtin in every domain they exist in: the management surface
  refuses to rewrite them, delete them or change their grants, because deleting
  one would revoke every permission it carries with no command to put it back.
  The seeding surface only adds: a permission a module declares after the mark is
  added to the role on the next run, and an existing grant is never removed or
  rewritten. The memberships are written through `authn.Memberships.Put`, so an
  account that is missing or deleted is refused by the seam. A membership is
  written only while the run is creating its account, or when it is the
  membership that carries a declared administrator's binding; an account that
  already exists keeps whatever memberships the operator gave it, so an
  addition, a suspension, a resume and a removal all survive every restart and
  are never written back to the declared state. An existing membership is
  reported as skipped and its row — its state, its source and its SSO exemption —
  is left untouched. It is idempotent: an account, a tenant, a membership, a
  role, a grant or a binding already there is left as it stands and reported as
  skipped, so a re-run writes nothing — and a re-run after the process gained a
  permission writes exactly that grant. The one existing role it corrects is the
  administration roles' `builtin` mark: a role an earlier seed wrote without it
  is marked once its grants are in place, because that mark is what keeps the
  role off the console's delete control. Nothing else is touched, and an account,
  a tenant, an administrator or a membership the configuration no longer names
  is neither disabled, demoted nor deleted. It drives to `StageStart`, past the
  migrations, so a database that was never migrated is one it can seed in a
  single command. A role and a binding come from here because no migration writes
  one: who a deployment's administrators are is the host's answer, and this
  command is where this host gives it.
- **`gen`** merges the OpenAPI fragments the modules declare into one document
  and writes it out, either to the file `-o` names or to standard output; the
  document's title and version come from `--title` and `--version`, defaulting to
  the program's name and version, and its description and servers come from
  `--description` and the repeatable `--servers`, defaulting to this host's own
  declaration of itself. With `--features PATH` it also writes the
  feature vocabulary — every `feature.Spec` the binary's imports reach, sorted by
  name, each with its name, declaring module and whether it is conditional — as
  `contracts/features.json`. It drives to `StagePrepare`, so it binds no port and
  constructs no module: it reads the same static declarations the operations
  endpoint serves, through the same pure function (`pkg/ops/openapi`), and the
  vocabulary from the same declaring set.
- The **command groups** are where a product's one-shot operations hang. The
  skeleton ships one placeholder group (`example`) with two leaves, one that
  stops at `Init` (a read) and one that goes to `Start` (a write); a product
  replaces the leaves' actions and words with its own.

## Where to change what

- **A new business module**: write it under `server/modules/` the way every
  module in this repository is written — a descriptor registered from an `init` —
  and name it on the commands in `server/main.go` that run it. The import is the
  wiring.
- **The platform roster**: `baselineServe` and `baselineWorker` in
  `server/main.go` are the core a product grows from, and `capabilityModules` in
  `server/capabilities.go` is the compile-time ceiling named by `serve`. A
  module dropped from the ceiling is a capability no run can carry; a module
  added is one every build compiles in, which starts wherever its configuration
  lets it. Shared topology goes in `server/defaults.go`, a deployment value in
  `server/testdata/`.
- **A new endpoint, guard or backend on an endpoint**: the endpoint is a
  constant in `server/host.go` and an entry under `http.endpoints` in the
  configuration; which modules guard it is the `Include` list of the command in
  `server/main.go`.
- **A new stack**: a row in the `stacks` table and a configuration under
  `server/testdata/`.
- **The frontend**: `web/`. Its build writes the bundle into `server/dist`, which
  the Go side embeds and serves through `pkg/webui`; the Go side changes only if
  the bundle's shape changes.

## Boundaries

- **The common pieces are taken from `pkg/`, not copied back in.** The
  operations surface is `pkg/ops` and its merge is `pkg/ops/openapi`; the
  frontend host is `pkg/webui`; a module that needs a logger names it through
  `pkg/log.NamedFor` rather than reaching for the process default. This program
  writes none of those and changes none of their contracts.
- **No business domain.** The skeleton carries no users, tenants, orders or any
  other domain type. A product's domain lives in `server/modules/` and on the
  command groups' leaves.
- **The command tree is this program's alone.** No module declares a command or
  learns which one runs it; adding one is an edit to `server/main.go`.
- **`server/main.go` writes the assembly, not the module contracts.** It calls
  `config.App`, `core.Module` and `core.Registry` and changes nothing about
  them; a module's own `AGENTS.md` is its contract.

## Running the checks

This directory carries its own `Makefile` — `make help` lists what it runs, and
`make check` runs all of it, the frontend first and the Go program second
because the Go build embeds the bundle. That file is the product's entry point:
a tree lifted out of the repository has nothing above it, so its build, test and
lint commands live there.

Two of its legs take an input a copy supplies and this repository does not:
`web-install` installs the frontend's dependencies where the frontend package
sits (inside the repository that package is a member of the workspace under
`web/`, whose single lockfile the repository's install goes through instead),
and `lint` reads the nearest `.golangci.yml` upward from the module directory
(this repository keeps one at its root).

**Inside this repository the entry point is the root Makefile.** `make check`
from the repository root covers this module through `go.work` like every other
module, and the frontend legs run as part of `make web-check`. A single leg
against this module alone is `make -C starters/demo <target>`.

The repository's own build leg adds a `GOWORK=off` build beside the workspace
one. That is the standalone build: a copy dropped outside this repository
resolves `pkg` from its released version, so its `go.mod` and `go.sum` have to
carry every dependency the imports reach.

The tests build the program once and run it as a real child process: they run
`seed` and then `serve` on the `sqlite-memory` stack, with a file locator so the
two processes share one database, read the probes and the merged document, and
read two of the identity system's own routes to hold that the baseline is
assembled and not only the infrastructure it began with. The `sqlite-memory`
stack needs nothing installed; the other four need the backends `compose.yaml`
brings up and are not exercised by the suite.
