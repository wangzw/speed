# The end-to-end tier

`starters/demo/web/e2e` drives the **built demo binary** through Chromium and
proves the product's documented journeys on what the browser actually renders:
the real review assembly, a real sqlite file, real HTTP, real clicks and typing.
One journey also runs in WebKit (see [Browsers](#browsers)).

It is not a component test and not an API test. A journey passes only when a
person clicking through the running product would see the documented outcome.

## Run it

```sh
make e2e-install   # once: installs the Chromium and WebKit builds the tier drives
make e2e           # type-checks the tier, then runs every spec
```

A single journey, without the `make` wrapper:

```sh
GATE_JOBS=8 pnpm -C web --filter demo-web exec playwright test \
  --config e2e/playwright.config.ts -g first-registration
```

The global setup hands `GATE_JOBS` to its three Go builds as `-p` and
`GOMAXPROCS`, so it must be set: a run without it fails rather than choosing a
width of its own. `make e2e` passes it from the gate's CPU budget.

The Playwright runner transpiles TypeScript but does not check it, so the tier
has its own type-check:

```sh
pnpm -C web --filter demo-web run typecheck:e2e
```

The tier is deliberately **not** part of `make check`: it needs the browsers and a
built backend, and it is slower than the commit gate. `tools/check_e2e_honesty.py`
(the `repo-check` leg) holds both facts — that `e2e` exists and that `check`
does not invoke it, directly or through `playwright test`/`typecheck:e2e`.
`.github/workflows/e2e.yml` is the periodic leg that does run it (nightly and
on manual dispatch), so a committed journey is exercised beyond a person
remembering to run it.

## Browsers

The `chromium` project runs every spec. The `webkit` project runs
`journey-password-reset-email.spec.ts` alone, against the same server on
`http://localhost:<port>`. That journey registers an account, resets its
password and signs in, so the browser has to keep the flow, reset and session
cookies. WebKit refuses a `__Host-` or `__Secure-` cookie set over http even on
a loopback host, where Chromium and Firefox keep it, and WebKit on Linux refuses
any `Secure` cookie set over http, loopback host included. The journey passing
in WebKit is what holds the cookies of a loopback http deployment, unprefixed
(`authn-flow`, `authn-reset`, `authn-sid`) and without `Secure`, to what WebKit
keeps. One project is run on its own with `--project webkit`.

## What the harness does

- **Global setup** (`global-setup.ts`) runs once per invocation. An e2e-tagged
  Go build embeds `starters/demo/server/dist-e2e`, so it builds the front end
  first (`pnpm -C web --filter demo-web run build`) with `DEMO_WEB_OUT_DIR` pointed
  there: the harness passes the directory's absolute path, and a person
  reproducing the command by hand writes the web-root-relative
  `../server/dist-e2e`, because vite resolves a relative `outDir` against the
  web package. It then builds three Go targets. The review binary:
  `go -C starters/demo/server build -tags review,e2e -p=$GATE_JOBS -o
  <root>/harness/demo-e2e .`; the default binary:
  `go -C starters/demo/server build -tags e2e -p=$GATE_JOBS -o
  <root>/harness/demo-deploy .`; and the SAML signing helper:
  `go -C starters/demo/server build -p=$GATE_JOBS -o
  <root>/harness/saml-signer ./e2e/samlsign`. The default binary is the
  deployment assembly -- every capability compiled in and no review controls;
  run-time configuration decides which capabilities start. All three go
  under `<root>/harness`, and a manifest records the binaries and the tree they
  came from. The bundle stays in `starters/demo/server/dist-e2e`, outside the
  tree `make dev` builds into, so the run never touches
  `starters/demo/server/dist`. Every per-test server starts its binary from
  that build; a failed build fails the run with the command's whole output. A successful build still prints its
  standard error, and a warning in either stream fails the run: the harness
  does not swallow a bundler or compiler warning.
- **One server per spec** (`fixtures/server.ts`). The server fixture creates a
  temporary directory, seeds the database with the binary, then asks the
  kernel for free loopback ports and copies the review assembly's own
  `review/sqlite.yaml`, substituting every value that names a shared resource
  (both listener addresses, the sqlite DSN, the token signing key, the
  object-store root and the public origin). A needle that is missing or
  duplicated fails the fixture loudly — a half-substituted template would bind
  the published port or write into the source tree. It serves the binary with
  the temporary directory as the working directory, waits for the `msg=ready`
  record and for the operations probes (`GET /health`, `GET /ready`), and only
  then hands the server to the test. Ports are allocated after the seed (which
  does not listen), and every candidate must be free on both loopback address
  families: a port that is free on 127.0.0.1 but held on ::1 is discarded,
  because a browser resolving `localhost` prefers ::1 and would reach the
  process holding it. A machine with no IPv6 loopback is judged on 127.0.0.1
  alone. The readiness probes ask the literal 127.0.0.1 and ::1 addresses
  rather than resolving the hostname: on ::1 a refused connection or a missing
  address family passes, and an accepted connection must answer 200 on the
  same path or the port counts as taken. A bind failure — another process took
  the port in the short window before `serve` bound it — and a taken ::1 port
  are both retried with a fresh pair, bounded at three attempts. The fixture's
  own bounds (seed 30s, start 30s, probes 15s, stop 25s) sit inside
  Playwright's 180s test timeout, so a slow start reports the process log and
  standard error instead of being killed by the test
  timeout first. They are waits — each returns as soon as the thing it waits
  for happens — so they are generous, and the caller's `GATE_TIME_SCALE`
  multiplies them together with the test timeout and the 30s `expect.timeout`
  that every assertion without a bound of its own inherits. A loaded machine
  gets a larger scale; the tier never probes the machine to choose one.
- **Stopping is ordered.** Hooks registered with `beforeStop` run first — the
  demo fixture closes the browser context there, because the notification
  stream keeps a connection open and would otherwise hold shutdown open — then
  SIGTERM, then SIGKILL if the process has not left within the bound.
- **Failures keep their evidence.** A failed test attaches the server's whole
  process log and standard error and keeps the server's temporary directory; a
  passing test removes both. A run with any failed test keeps its own run
  directory too and prints its path at the end.
- **Accounts are driven through the UI** (`helpers/accounts.ts`): registration,
  the email verification section and password sign-in are filled and clicked by
  accessible label and role, never by calling the API.
- **A real identity provider backs the social journeys**
  (`fixtures/identity-provider.ts`). A spec that drives a social method opts its
  server in with `test.use({ socialIdentityProvider: true })`; only then does
  the server fixture start a local OpenID Connect provider: discovery, a key
  set, a token endpoint, a userinfo endpoint and a browser-facing authorization
  page for one fixed user. Every other spec runs without openssl and without
  the provider's two extra loopback ports. The server fixture points the review
  configuration's Google method at the provider through the method's `issuer`,
  `allow-loopback` and `root-ca-file` settings, and the provider's TLS
  certificate chains to a CA it mints with openssl into the server's temporary
  directory; the CA file's certificate is appended to the system roots the
  product trusts. The product's restricted egress client is the only thing that
  reaches the HTTPS half; the browser reaches the authorization page over plain
  http on a second loopback port and approves the request with a click, so the
  round trip is real without weakening Chromium's certificate checking for
  every journey. A spec never answers the provider's requests; it reads the
  page and clicks. The provider's own page copy is the fixture's constant, not
  a product locale string.
- **The enterprise journey backs its own fixtures**
  (`fixtures/identity-provider.ts`, `fixtures/dns-stub.ts`). A spec that drives
  that journey opts its server in with `test.use({ enterpriseSSO: true })`;
  only then does the server fixture start the enterprise provider and the UDP
  DNS stub. The login method itself is assembled for every review server, not
  only the opted-in one: `authn.sso.oidc` is part of the review build's module
  roster, so every review sign-in surface carries the enterprise entry even
  when the configuration names no provider. The opt-in starts the provider and
  DNS fixtures and points the seams at them; it does not add the method. The
  review configuration's `authn.sso` block is pointed at them:
  `dns-resolver` at the stub's loopback address, `oidc-ca-file` at the CA the
  provider minted, and `allow-private-egress` on so the restricted egress
  client can reach the provider on loopback. The stub answers TXT queries for
  the one record name on the test domain and NXDOMAINs every other name, type
  and class; nothing is forwarded. A spec publishes the record the product
  renders through the stub's `setTXT`, which is the operator's own act at the
  domain's DNS. The same opt-in adds a second tenant (`globex`) to the seed
  beside the template's `acme`: the journey's last step switches the employee
  from the organization they already belong to into the tenant the provider
  serves, and the seed writes the operator's platform-administrator role into
  both. The employee's memberships are still product writes through the
  admin's member page. Every other spec keeps the block's empty defaults —
  the system resolver and the system roots, which is what a deployment keeps —
  and the template's single seeded tenant. This opt-in is separate from
  `socialIdentityProvider`.

- **A SAML identity provider backs the SAML journey**
  (`fixtures/saml-identity-provider.ts`). A spec that drives it opts in with
  `test.use({ enterpriseSSO: true, samlIdentityProvider: true })`; the first
  supplies the tenant, the DNS stub and the domain proof, the second starts the
  provider. The review build runs the SAML method (`authn.sso.saml` is in its
  `reviewModules`), and an operator registers the provider through the admin's
  provider form with its entity identifier, https sign-on address, certificate
  and claim mapping. The provider receives the AuthnRequest on the redirect
  binding, shows a page for one fixed user and, once the person approves, serves
  a page that posts a signed response to the assertion consumer service. The
  signature comes from `starters/demo/server/e2e/samlsign`, a Go command the
  global setup builds beside the binaries; it signs with the library the product
  validates with. A second listener on plain http is the attacker's origin: it
  posts a response the fixture kept back for another browser's flow
  (`holdNextResponse`). Both origins are on 127.0.0.1 and the product is on
  localhost, so both are cross-site to it. The product accepts only an https
  single sign-on address, so the provider's certificate is self-minted and the
  spec sets `ignoreHTTPSErrors` for its own browser contexts; no other journey
  relaxes certificate checking. The spec reads the browser's own requests with
  `page.on('request')` to assert that the cross-site post reached the assertion
  consumer service without the flow cookie and that the re-post to the resume
  route was same-origin with it.

## The sanctioned non-UI reads

The review assembly's carriers reach no vendor or relay, so the process log is
the only place a person running the review reads a code or sees a message leave.
The tier allows exactly three reads from it:

- **A verification code.** The mail and SMS senders write each message's code to
  the process log as logfmt. `fixtures/log.ts` is the **only** module allowed to
  interpret those lines; it finds the latest code for a given kind
  (`sms`/`mail`) and destination address. The field is `verification_code`,
  never `code`: the gateway treats `code` as a sensitive key name and the
  logging layer masks its value.
- **A review carrier's accepted-message line.** The SMS carrier writes one line
  per message it accepts, naming the destination, template and language; the
  template rollout journey reads it. The mail carrier writes one line per
  message it sends, naming the destination and the rendered subject and body;
  the invitation journeys read it to show that the notice left the process and
  that its acceptance landing is the one the page handed out. The logging layer
  masks the `spd_inv_` token wherever it appears, so the mail body carries the
  landing with the token redacted. `waitForCarrierMessage` and
  `countCarrierMessages` are the server fixture's reads; the mail line is
  selected by the subject the request's language renders.
- **The delivery pipeline's external-send record line**, for the same journey
  only. The pipeline writes one record per message that leaves, and
  `waitForOutboundRecord` and `countOutboundRecords` are the server fixture's
  reads.

A spec or helper may import only `waitForVerificationCode` from `fixtures/log`;
the carrier and outbound reads are server-fixture methods because
`fixtures/log.ts` is the only module that parses logfmt. Every other test-side
read of the process log is forbidden.

**This tier does not read the delivery ledger.** `notification_sends` is a table
the design keeps off the product's interface, and the rollout spec does not claim
to cover it: an end-to-end spec asserts rendered behaviour, and a fixture may
read the ledger but may not be the basis of its assertion. The record that a
locally refused send is failed with a refusal reason is asserted by the
review-tagged Go case `TestReviewRefusedSMSTemplateIsRecordedFailedInTheLedger`
(`starters/demo/server/review_notification_ledger_test.go`), which queries the
table directly.

Everything else is an assertion on rendered behaviour: visible text, accessible
roles and names, focus, URL, disabled/selected state, banners and empty states.

## Fixture-type journeys

A journey whose subject is deployment machinery rather than a rendered surface
may be registered fixture-type, and `journey-deploy-and-operate` is the only
one. Its steps -- rendering the deployment template onto a volume, running the
binary's own `sqlite seed`, starting `sqlite serve`, and reading the listeners
the deployment publishes -- are the deployment's own work, and no browser can
perform or observe them. The counterpart of that allowance is that the spec's
header names the ids it covers off-browser, and only these may be so covered:

- the process and its commands: the ready record, and `seed`'s report of what
  it wrote (REQ-deployment-005, 007, 008, 012);
- what the binary serves: the entry document, the extension-less page
  fallback, and the 404 for a named asset (REQ-deployment-002 for the
  same-origin half, 003, 004);
- files the deployment writes: the first-run signing key and its permission
  (REQ-deployment-006);
- the operations listener, which has no browser client: the liveness and
  readiness probes and the merged API document (REQ-deployment-017, 018, 019);
- the backend origin's login discovery document, whose entry set is the
  requirement's own subject (REQ-signin-001, assembled entry set only).

Everything else in that journey -- sign-in, the account page, the admin, the
refused write -- is still driven in the browser with clicks and keys. The
allowance is not a licence to substitute a fixture for product behaviour: a
fixture may read what the deployment itself produces, and may not answer a
request or manufacture a state the product would otherwise have to reach.

## Honesty rules

These are the contract the tier is held to, enforced twice — by the eslint rule
`speed/no-e2e-bypass` over this directory and by `tools/check_e2e_honesty.py`
over the tree. The first two are mechanical; the last two are review rules the
gates cannot decide for a spec.

- **Inside the browser, interaction is mouse clicks and keyboard input only.**
  No `fetch`/`XMLHttpRequest`/`WebSocket`/axios/node-fetch/ws, no Node HTTP
  client (`node:http`, `node:https`, `node:http2`, `node:net`), no
  `page.evaluate`/`addInitScript`, no `page.route`/`context.route` or
  HAR/websocket replay, and no API request context (`page.request`, the
  `request` fixture). Those ways of touching the backend can pass while the
  page a person uses is broken. Detection follows bindings and destructured
  aliases (`{ page: p }`, `{ request: api }`, `const api = page.request`) and
  recognises a CDP session by its `newCDPSession(...)` initializer.
- **The one allowed CDP family is WebAuthn.** A spec may open a CDP session and
  send `WebAuthn.*` virtual-authenticator commands for a passkey ceremony;
  every other CDP domain, and a `send` whose command is not a literal, is a
  finding.
- **A spec or helper may not reach around the product.** Only
  `e2e/fixtures/**` (and the global setup) may spawn processes, write files,
  open socket or database clients, or interpret log lines; the eslint rule
  runs those files with `{ allowAssembly: true }` and the honesty gate enforces
  the same split by directory. The allowed log reads are the three named above:
  a spec or helper imports only `waitForVerificationCode`, and asks the server
  fixture for a review carrier's accepted message or the delivery pipeline's
  external-send record (`waitForCarrierMessage`, `waitForOutboundRecord` and
  their count forms). `fixtures/log.ts` is the only module that parses logfmt
  lines, and every other test-side log read is forbidden. No spec or fixture
  method reads the delivery ledger; the one case that asserts on it is the
  review-tagged Go case named above.
- **The backend is real**: the built review binary, a fresh sqlite file, real
  HTTP. Fixtures may assemble the environment (builds, temporary directories,
  random ports, seeding the operator, probes); they may not fake a response.
  The gate cannot prove a fixture is merely assembling, so this is also a
  review rule: a fixture that answers a journey's request is a defect.
- **Never inject data into the database** to manufacture a precondition. A
  journey whose precondition the UI cannot reach is registered `unreachable`;
  a journey the UI can reach but whose spec is not written is `pending`; a
  journey the product does not present yet is `unimplemented`. None of the
  three is a licence to bypass the product. The layer split above
  keeps a spec and a helper away from a database client. What the fixtures do
  seed goes through the binary's own `seed` command reading a configuration
  the fixture extends: the operator, and, only when a spec opts in, the
  account or membership that journey signs in as. That is assembly, not a
  manufactured product state: the seed is the product's documented
  provisioning path, it runs before the server starts, and it writes what an
  operator's own provisioning would write. It is not a substitute for the
  journey -- every state a spec asserts has to be reached from that starting
  point through the rendered product, and no fixture may answer a request.
- **Never assert only on an API response.** An assertion lands on what the
  product renders. A review rule: the gates can see that a spec has a test,
  not what its assertion reads. The one exception is a registered fixture-type
  journey, and only for the ids listed in the fixture-type allowance above.
- **Artifacts stay out of the tree.** Every Playwright artifact path —
  `outputDir`, the HTML reporter's `outputFolder`, any `outputFile` — is under
  the run's own `/tmp` root, and the honesty gate rejects a path that is not.

### Registered off-UI API assertions

Three settings refusals have no rendered control to drive. Each is registered
here, and each is driven through the product's API by the same administrator
account in its own API session, beside the rendered assertion — never instead
of it. The rendered assertion still carries the journey; the API answer is the
sibling evidence for a refusal the page cannot express.

| Refusal | Rendered assertion | API assertion |
|---|---|---|
| A protected tenant's settings write | `tenant-admin: the protected organization renders no switch and its write is refused` renders the read-only notice and no switch | `PUT` on a policy answers `403` `urn:speed:settings:protected` |
| A platform-enforced policy is turned off | `tenant-admin: a platform-enforced policy is read-only and cannot be turned off` renders the policy's switch checked and disabled with the platform-enforced notice | `PUT` on that policy answers `409` `urn:speed:settings:tenant-admission-enforced` |
| A platform-level setting is addressed from the tenant | the main journey's settings step renders neither the platform keys nor their namespaces | the tenant listing returns only the two tenant keys; `PUT` and `DELETE` on each platform key answer `404`; a namespace read answers `404` or `405` |

None of the three counts toward REQ-tenant-admin-020: that requirement is
about refusing a `layer` or `tenant` query parameter with `400`, and no API
assertion here sends either parameter.

## Adding a journey spec

Every spec declares, in its header comment:

```ts
// @journey journey-first-registration-to-personal-organization.md
// @covers REQ-signin-001, REQ-signup-001
```

- `@journey` names one document under `docs/prd/journeys`.
- `@covers` lists the requirement ids *this spec actually exercises* — the
  assertions that can fail when the requirement is broken — not every id the
  journey document lists. Each must be an id that document carries; the gate
  checks the membership, and review checks that the claim is not wider than the
  assertions. `journeys.ts`'s own `covers` array is the other direction: it
  repeats the ids the journey document lists, so the registry and the document
  can be compared without opening the spec.
- A spec must contain at least one `test(...)` that runs. A header with no
  body, or a suite made only of `test.skip`/`test.fixme`, does not cover
  anything and fails the honesty gate.
- Expected copy is read from the shipped en-US locale bundles through
  `helpers/i18n.ts` (`copy`, `copyPattern`), not hardcoded, so a sentence that
  changes in the product changes with it. The browser is pinned to `en-US` and
  `UTC` by the Playwright config.
- Step labels (`test.step`) follow the journey document's own steps, so a
  failing run names the step a person would have been on.
- **Sign-in locators are scoped to the surface they belong to.** Every review
  sign-in surface carries the enterprise entry beside the form channels, so a
  role query over the whole page can match more than one control with the same
  accessible name — the second-factor step's "Continue" and the enterprise
  entry's "Continue" are the pair. A locator addresses the container the
  control renders in: the form channel's own panel, the enterprise entry, the
  step region. An unscoped role query over the sign-in page is a defect in the
  spec, not a reason to change the assembly.

`journeys.ts` is the registry of every journey document and how the tier
relates to it. A journey is `covered` by a spec; `pending` when the browser
tier can drive it but its spec has not been written yet, with the reason saying
the path is reachable and the spec is scheduled; `unimplemented` when the
product does not present the path yet, so no spec could pass today, with the
reason saying what is missing; or `unreachable` when a precondition the tier
cannot provide stands in the way, which the reason names.
The distinction is load-bearing: calling a reachable journey `unreachable`
tells a reader the browser cannot get there when it can, and calling a
designed-but-unbuilt path `pending` claims only a spec stands in the way; either
silences the honesty gate for that journey. The gate requires a reason on every
non-covered state, rejects any other status, and prints the covered / pending /
unreachable / unimplemented counts on every run; a journey that is neither
covered nor registered non-covered fails `make repo-check`.

## Where the artifacts are

Each run mints an id and keeps its artifacts under `/tmp/speed-e2e-<run-id>`, so
two runs in different worktrees never share a manifest or an output directory
(Playwright clears `outputDir` when it starts). The id travels in
`SPEED_E2E_RUN_ID` from the config process to the global setup and the fixture
workers.

**At most one run per checkout at a time.** The front-end build writes
`starters/demo/server/dist-e2e` inside the checkout (the e2e-tagged binaries
embed that directory), and both binaries are built from it, so this one
product is not per-run. Two runs on the same checkout would race on the
directory and could embed a half-written bundle; two runs in different
worktrees are isolated because each worktree owns its own `dist-e2e`.

| Path | Contents |
|---|---|
| `<root>/harness` | The build the run shares: the review binary and the manifest the fixtures read |
| `<root>/artifacts` | Playwright's per-test output: failure screenshot and trace (`retain-on-failure`), plus the attached process log |
| `<root>/report` | The HTML report (`open: 'never'`); open it to read a failed run's attachments |

`<root>` is `/tmp/speed-e2e-<run-id>`, computed in `fixtures/paths.ts` and used
by the config for every artifact path. The tier leaves nothing in the
repository except the git-ignored `starters/demo/server/dist-e2e` it builds
into. A failed spec also keeps its server's temporary directory (under
the system temp directory) so the sqlite file and configuration can be
inspected; the path is attached to the failure.

The run directory is bounded. At the start of every run, global setup removes
earlier run directories under `/tmp` whose whole name matches
`speed-e2e-<pid>-<clock>-<nonce>` and whose modification time is older than
one day (`RETENTION_WINDOW_MS` in `fixtures/retention.ts`). The same sweep
removes the per-spec directories a failed test keeps -- the review server's
`speed-e2e-server-<six>` and the deploy fixture's `speed-e2e-deploy-<six>` --
once they age past the window, under the system temp directory the fixtures
create them in. A name that does not match one of those shapes exactly is never
a candidate, and a symbolic link is never followed. A directory that holds
read-only files has its owner write permission restored before it is deleted,
so a test that locked its own fixture cannot leave the directory behind. When
the whole run passes, `fixtures/retention-reporter.ts` removes this run's
directory from Playwright's `onExit`, after the HTML report has been written. A
failed, timed-out or interrupted run keeps the directory and the reporter
prints its path (and the report's) at the end of the run; the failure-kept
per-spec directories are cleaned by the next run only after the same window.
