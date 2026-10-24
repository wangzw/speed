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
  passing test removes both.
- **Accounts are driven through the UI** (`helpers/accounts.ts`): registration,
  the email verification section and password sign-in are filled and clicked by
  accessible label and role, never by calling the API.

## The one non-UI read: the verification code

The review assembly's carriers reach no vendor or relay. The mail and SMS
senders write each message's verification code to the process log as logfmt, so
that is where a person running the review reads it. `fixtures/log.ts` is the
**only** module allowed to interpret those lines; it finds the latest code for a
given kind (`sms`/`mail`) and destination address. The field is
`verification_code`, never `code`: the gateway treats `code` as a sensitive key
name and the logging layer masks its value.

Everything else is an assertion on rendered behaviour: visible text, accessible
roles and names, focus, URL, disabled/selected state, banners and empty states.

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
  the same split by directory. The one log reader a spec or helper may import
  is `waitForVerificationCode`; `fixtures/log.ts` is the only module that
  parses logfmt lines.
- **The backend is real**: the built review binary, a fresh sqlite file, real
  HTTP. Fixtures may assemble the environment (builds, temporary directories,
  random ports, seeding the operator, probes); they may not fake a response.
  The gate cannot prove a fixture is merely assembling, so this is also a
  review rule: a fixture that answers a journey's request is a defect.
- **Never inject data into the database** to manufacture a precondition. A
  precondition the UI cannot reach is registered as unreachable, not bypassed.
  The layer split above keeps a spec and a helper away from a database client;
  the fixtures' own seeding is the operator seed the binary performs, never a
  journey's account, tenant or invitation.
- **Never assert only on an API response.** An assertion lands on what the
  product renders. A review rule: the gates can see that a spec has a test,
  not what its assertion reads.
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

`journeys.ts` is the registry of every journey document and whether the tier
drives it. A journey is either `covered` by a spec or `unreachable` with a
reason saying why the browser tier cannot stand it up (a deployment, a real
identity provider) or that its spec has not been written yet. The honesty gate
reads this file; a journey that is neither covered nor registered fails
`make repo-check`.

## Where the artifacts are

Each run mints an id and keeps everything under `/tmp/speed-e2e-<run-id>`, so
two runs at the same time never share a build, a manifest or an output
directory (Playwright clears `outputDir` when it starts). The id travels in
`SPEED_E2E_RUN_ID` from the config process to the global setup and the fixture
workers.

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
