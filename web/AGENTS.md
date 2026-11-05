# AGENTS.md — web workspace

Working conventions for the pnpm workspace under `web/`. The repository-wide
rules in the root `CLAUDE.md` and the root `README.md`'s status table apply;
this file adds what is specific to the npm side.

## Before you edit

- Every deliverable npm package ships **English** documentation, comments
  and commit messages. CJK appears only in product resources and test
  fixtures under directories named `locales/`, `locale/`, `i18n/` or
  `translations/` (the repo CI CJK scanner exempts those names).
- Never add a second Node- or pnpm-version source. Node: `web/.nvmrc`;
  pnpm: `web/package.json`'s `packageManager`. Every Node toolchain
  installer reads those two files directly.
- Root `package.json` carries only shared dev tooling. A package's real
  dependencies, peers and `exports` map live in its own manifest.

## Adding a package

1. Scaffold under `web/packages/<name>` following the shape in the root
   `web/README.md` (ESM-only exports map, tsconfig pair, scripts
   `lint`/`typecheck`/`test`/`build`).
2. Add it to `web/pnpm-workspace.yaml`'s package list, then run
   `pnpm install` from `web/` so the lockfile records the new importer.
3. Write README.md + AGENTS.md and run every gate from the package
   directory: `pnpm lint`, `pnpm typecheck`, `pnpm test`, `pnpm build`, all
   zero warnings.
4. New runtime/peer dependencies need justification in the pull request:
   they land in every consuming project's bundle or `package.json` peer set.
5. Public API is frozen by convention like Go module APIs (lockstep
   versioning is repo-wide): a breaking change ships deliberately or not
   at all.

## Test placement and hygiene

- Unit tests are colocated per source file (`registry.ts` ->
  `registry.test.ts`). A test file that does not map 1:1 to a source file is
  named for the behavior it verifies.
- Shared helpers live in the package's `test-utils/` (e.g.
  `i18n/test-utils/welcome.ts`) and are imported by tests; never duplicate
  them inline.
- Environment-dependent globals (navigator, localStorage, location) are
  never relied on implicitly: inject deterministic stand-ins or guard the
  reads, or the suite breaks under Node and CI.
- Vitest runs from the package directory, and every package with tests
  carries a `vitest.config.ts` whose `test` block is built with
  `sharedTestOptions()` from `web/vitest.shared.ts` -- the workspace's one
  timeout and coverage block. A package config declares only what differs:
  a DOM-rendering package sets the jsdom environment and its setup file,
  and keeps jsdom, Testing Library and friends in its own devDependencies,
  never the shared root. The workspace guard
  (`web/scripts/workspace-test-config.test.mjs`) fails a package whose
  config stops spreading the preset.
- Browser cases are named `*.browser.test.tsx` and live beside the
  component under test. They run only through the root `make web-browser`
  (Vitest browser mode, Playwright provider, Chromium --
  `web/vitest.browser.config.ts`); the shared preset excludes them from
  every package's `test` leg, and the eslint config and base tsconfig keep
  them out of the default lint and typecheck legs. Install Chromium once
  with `pnpm -C web exec playwright install chromium`.
- Browser cases assert invariants -- geometry, focus order, focusability
  -- never pixels, and they wait on events rather than fixed sleeps.
  Interface text in an assertion comes from the package's shipped
  resources (`src/locales/en-US.json` or `resources.ts`), never from a
  literal; test comments stay English. The cases render in en-US and the
  tier's setup pins the document's family to Arial / Liberation Sans, so
  the measured text keeps the same metrics on macOS and on the CI image.
- CI runs `pnpm install --frozen-lockfile` from `web/`, then
  lint/typecheck/test/build per package with the root-provided toolchain.
  The browser tier is a separate scheduled job
  (`.github/workflows/browser.yml`) because Chromium is not part of the
  `.mise.toml` toolchain.

## Cross-package duplication kept in lockstep

The flow-step renderer (`src/internal/flow-step.*`) exists once in each of
`auth-ui`, `account-ui` and `admin-ui`. The copies are deliberate -- the
renderer owns UI and each package owns its own step vocabulary -- and they
carry a sync obligation: a change to how a step renders lands in all three
copies in the same change. A source-level parity suite in
`packages/admin-ui` (`src/internal/flow-step-parity.test.ts`) reads the
three files and fails when they disagree on the rendered step kinds, the
answer properties they report, the render primitives they use, or the
WebAuthn seam they drive, naming the copy that drifted. The same suite
holds every copy to the shared TOTP pieces: each one must import
`TotpEnrollmentPanel` from `@speed/ui-kit` and `parseOtpauth` from
`@speed/auth-core/otpauth`, and the failure names the copy and the
missing piece. It also scans `packages/` (skipping `node_modules` and
`dist`) so the otpauth parser and the QR renderer keep one home: a local
`parseOtpauth`/`formatSecretForDisplay` implementation, a second direct
import of `qrcode-generator`, or `qrcode-generator` declared outside
`ui-kit`'s manifest fails with the file and the reason named.

## The strict typecheck

`web/tsconfig.base.json` is strict: `strict`, `noUncheckedIndexedAccess`,
`verbatimModuleSyntax` (type-only imports must be `import type`),
`isolatedModules`, `resolveJsonModule`. Shape-drift guardrails in tests use
`// @ts-expect-error` lines, which the typecheck verifies. Do not loosen the
base to accommodate new code; adjust the code.

## The eslint surface

One flat config at `web/eslint.config.mjs` serves the whole workspace
(discovered by ascending from each package). It enforces
`typescript-eslint` recommended plus `no-explicit-any` as an error, and
carries the workspace's own rules behind the `speed/` plugin namespace:
`speed/no-literal-text` (implementation and rule tests in
`web/eslint-rules/`) errors on user-facing text written inline in
package `src`, `speed/no-direct-http` errors on hand-written HTTP
(fetch/XMLHttpRequest/axios/node-fetch) anywhere but `@speed/api-client`,
the rule's single config-level whitelist -- every request must route
through the api-client request function (see CLAUDE.md's API contract
section) -- and `speed/no-dangerous-html` errors on markup sinks
(`innerHTML`, `dangerouslySetInnerHTML`, `insertAdjacentHTML`,
`document.write` and the like), holding the premise the i18n instance's
verbatim interpolation rests on. `speed/no-double-feedback-region` holds a
component to one `WriteFeedbackRegion`: a feedback scope is one
`useWriteFeedback` instance plus one region for its control group, and two
regions in one component can show two results at once. Its message names
the file, the component and the region count. The demo host turns this rule
on over its own `src` too: a host is part of the product, so it uses the same
rule and the same exemption list rather than a copy. Package tests and
`test-utils/` are exempt by config for these rules, because fixture strings
and scripted stand-ins are data.

### One feedback region per component

The rule counts `WriteFeedbackRegion` per declaring component, not per
file: a page component may own several control groups, each with its own
scope and region under its own controls. Those components are registered
in the rule's `WRITE_FEEDBACK_REGION_EXEMPTIONS`
(`web/eslint-rules/no-double-feedback-region.js`), each entry carrying the
reason the regions are separate control groups:

- `packages/admin-ui/src/AccountsPage.tsx` — `AccountsPage`: the list
  result and the detail panel's action row, sessions and identifiers
  sections.
- `packages/admin-ui/src/AccountsPage.tsx` — `AccountDetailWrites`: six
  rows (status, MFA requirement, profile patch, temporary password,
  identifier add, secret create), each with its own group.
- `packages/admin-ui/src/PermissionsPage.tsx` — `PermissionsPage`: roles,
  grants, bindings and lookup sections.
- `packages/admin-ui/src/SmsPage.tsx` — `SmsPage`: register form, offer
  form and templates table.
- `packages/admin-ui/src/SsoPage.tsx` — `SsoPage`: providers list,
  provider write form and domains section.
- `packages/account-ui/src/PreferencesSection.tsx` — `PreferencesSection`:
  language row and time-zone row.
- `packages/admin-ui/src/ImpersonationsPage.tsx` —
  `ImpersonationsPage`: start form and the records table with its row
  actions.
- `packages/admin-ui/src/InvitationsPage.tsx` — `InvitationsPage`:
  invitations table and members table.
- `packages/admin-ui/src/OrganizationMembersPage.tsx` —
  `OrganizationMembersPage`: members table and invitations table.

Adding a multi-region component means adding an entry with a reason; the
rule's scan test (`no-double-feedback-region.test.mjs`) reads every
runtime TSX file under `packages/*/src` and the demo host's
`starters/demo/web/src` -- the scopes the two lint configs give the rule,
with tests and `test-utils/` left out -- and fails when the list and the
trees disagree in either direction, so a new component cannot slip
through unregistered and a stale entry is removed. A package entry is
keyed from `web/` (`packages/...`), a host entry from the repository root
(`starters/demo/web/src/...`). A queue
of independent per-row transfers is the design's allowed exception (each
upload row is its own slot): `FileUploader` renders each row's status as
text from its `rows` props and never renders `WriteFeedbackRegion`, and a
region rendered once inside a `rows.map(...)` is one JSX element rather
than one per row, so the rule does not reach it.

The rules' unit tests run from the workspace root, not from a package
directory (the rules live outside every package, so no per-package
suite picks them up), together with the workspace configuration guard
(`web/scripts/workspace-test-config.test.mjs`): `make web-check` runs
both, and locally via `pnpm exec vitest run eslint-rules/ scripts/`
from `web/`.

Still deferred -- do not
half-enable: the `react-hooks` plugin is not adopted, keeping
`eslint.config.mjs` free of plugin dependencies (see that file's own
header).

## Bilingual fixtures

Bilingual test fixtures live under
`packages/<name>/test-utils/locales/<namespace>/<lang>.json` -- or,
when the fixtures ARE the shipped resources, under the package's
`src/locales/` (ui-kit's zh-CN/en-US bundles); `.ts` and `.md` files
assert or describe content by importing those fixtures, never by
embedding language text.
