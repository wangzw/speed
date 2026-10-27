# The end-to-end tier

`starters/demo/web/e2e` drives the **built demo binary** through Chromium and
proves the product's documented journeys on what the browser actually renders:
the real review assembly, a real sqlite file, real HTTP, real clicks and typing.

It is not a component test and not an API test. A journey passes only when a
person clicking through the running product would see the documented outcome.

## Run it

```sh
make e2e-install   # once: installs the Chromium build the tier drives
make e2e           # type-checks the tier, then runs every spec
```

A single journey, without the `make` wrapper:

```sh
pnpm -C starters/demo/web exec playwright test \
  --config e2e/playwright.config.ts -g first-registration
```

The Playwright runner transpiles TypeScript but does not check it, so the tier
has its own type-check:

```sh
pnpm -C starters/demo/web run typecheck:e2e
```

The tier is deliberately **not** part of `make check`: it needs Chromium and a
built backend, and it is slower than the commit gate. `tools/check_e2e_honesty.py`
(the `repo-check` leg) holds both facts — that `e2e` exists and that `check`
does not invoke it, directly or through `playwright test`/`typecheck:e2e`.
`.github/workflows/e2e.yml` is the periodic leg that does run it (nightly and
on manual dispatch), so a committed journey is exercised beyond a person
remembering to run it.

## What the harness does

- **Global setup** (`global-setup.ts`) runs once per invocation. The Go program
  embeds `starters/demo/server/dist`, so it builds the front end first
  (`pnpm -C starters/demo/web build`), then the review binary
  (`go -C starters/demo/server build -tags review`). Both products go under
  `<root>/harness`, and a manifest records the binary and the tree it came
  from. Every later server starts that same binary; a failed build fails the
  run with the command's whole output. A successful build still prints its
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
  does not listen), and a bind failure — another process took the port in the
  short window before `serve` bound it — is retried with a fresh pair, bounded
  at three attempts. The fixture's own bounds (seed 20s, start 20s, probes 10s,
  stop 15s) sit inside Playwright's 120s test timeout, so a slow start reports
  the process log and standard error instead of being killed by the test
  timeout first.
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
  console's member page. Every other spec keeps the block's empty defaults —
  the system resolver and the system roots, which is what a deployment keeps —
  and the template's single seeded tenant. This opt-in is separate from
  `socialIdentityProvider`.

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
- **The review SMS carrier's accepted-message line**, for the SMS template
  rollout journey only. The carrier writes one line per message it accepts, and
  that line — destination, template and language — is the evidence a browser
  tier can read that the message left the process. `waitForCarrierMessage` and
  `countCarrierMessages` are the server fixture's reads.
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

Everything else in that journey -- sign-in, the account page, the console, the
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
  a journey the UI can reach but whose spec is not written is `pending`.
  Neither state is a licence to bypass the product. The layer split above
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

## Adding a journey spec

Every spec declares, in its header comment:

```ts
// @journey journey-first-registration-to-personal-workspace.md
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
the path is reachable and the spec is scheduled; or `unreachable` when a
precondition the tier cannot provide stands in the way, which the reason names.
The distinction is load-bearing: calling a reachable journey `unreachable`
tells a reader the browser cannot get there when it can, and silences the
honesty gate for that journey. The gate requires a reason on both non-covered
states, rejects any other status, and prints the covered / pending / unreachable
counts on every run; a journey that is neither covered nor registered
non-covered fails `make repo-check`.

## Where the artifacts are

Each run mints an id and keeps everything under `/tmp/speed-e2e-<run-id>`, so
two runs in different worktrees never share a manifest or an output directory
(Playwright clears `outputDir` when it starts). The id travels in
`SPEED_E2E_RUN_ID` from the config process to the global setup and the fixture
workers.

**At most one run per checkout at a time.** The front-end build writes
`starters/demo/server/dist` inside the checkout (the Go program embeds that
directory), and both binaries are built from it, so this one product is not
per-run. Two runs on the same checkout would race on the directory and could
embed a half-written bundle; two runs in different worktrees are isolated
because each worktree owns its own `dist`.

| Path | Contents |
|---|---|
| `<root>/harness` | The build the run shares: the review binary and the manifest the fixtures read |
| `<root>/artifacts` | Playwright's per-test output: failure screenshot and trace (`retain-on-failure`), plus the attached process log |
| `<root>/report` | The HTML report (`open: 'never'`); open it to read a failed run's attachments |

`<root>` is `/tmp/speed-e2e-<run-id>`, computed in `fixtures/paths.ts` and used
by the config for every artifact path. Nothing the tier writes lands in the
repository. A failed spec also keeps its server's temporary directory (under
the system temp directory) so the sqlite file and configuration can be
inspected; the path is attached to the failure.

The run directory is bounded. At the start of every run, global setup removes
earlier run directories under `/tmp` whose whole name matches
`speed-e2e-<pid>-<clock>-<nonce>` and whose modification time is older than
seven days (`RETENTION_WINDOW_MS` in `fixtures/retention.ts`). The same sweep
removes the per-spec directories a failed test keeps -- the review server's
`speed-e2e-server-<six>` and the deploy fixture's `speed-e2e-deploy-<six>` --
once they age past the window, under the system temp directory the fixtures
create them in. A name that does not match one of those shapes exactly is never
a candidate, and a symbolic link is never followed. When the whole run passes,
`fixtures/retention-reporter.ts` removes this run's directory from Playwright's
`onExit`, after the HTML report has been written. A failed, timed-out or
interrupted run keeps the directory and the reporter prints its path (and the
report's) at the end of the run; the failure-kept per-spec directories are
cleaned by the next run only after the same window.
