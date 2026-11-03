# web/ — the frontend package library

The repo's npm side. Every deliverable npm package of the platform lives
under `web/packages/*` as a **pnpm workspace** rooted here. The workspace
also carries two private members: the shared test-support package
`web/test-utils/` and the demo starter's web host `starters/demo/web/`
(see `web/pnpm-workspace.yaml`).

## Why web/ is its own workspace root

The repo root is a Go workspace (`go.work` + one module per directory under
`pkg/`) and must stay one. The frontend deliberately does **not** share the
root:

- `go work` and other Go tooling walk module roots; a root-level
  `package.json` or `pnpm-workspace.yaml` would sit in that walk and invite
  cross-tool confusion.
- pnpm's workspace protocol (`workspace:*`) and its single lockfile want a
  root of their own; the `go.work` file and pnpm's artifacts have nothing in
  common.
- Nothing Go-side ever needs to resolve an npm package, and nothing npm-side
  ever needs a Go module. Two roots, one repo, zero overlap.

This is also why the demo starter's web host joins as an **external member**
(`../starters/demo/web`) rather than being folded into `web/`: it lives
exactly where a delivered consumer project lives, outside the workspace
root, and still installs, lints, typechecks, tests and builds under the same
one-lockfile workspace. Unlike the packages, it declares its `@speed`
dependencies by registry version range (`^0.0.1`) rather than the workspace
protocol: `linkWorkspacePackages` in `pnpm-workspace.yaml` links a member
when the requested range is satisfied locally, so the in-repo build resolves
them to the sibling source while the same manifest, copied out of the
repository, installs them from the registry unchanged.

Tooling treats the boundary the same way: Go checks run from module
directories, npm checks run from `web/`.

## Node and pnpm versions: single sources

| What | Single source of truth | Consumers |
|---|---|---|
| Node version | `web/.nvmrc` (content: `24`) | any `node-version-file` consumer; local `nvm`/`mise` if you use one |
| pnpm version | `"packageManager": "pnpm@11.1.2"` in `web/package.json` | `corepack` installs exactly that version |

The Node major is pinned to 24;
`web/package.json`'s `engines` accepts `>=24` so local development on newer
Node (e.g. 26) keeps working. Never add a second version source; when either
version moves, update the source row above together with every consumer.

## Layout

```
web/
  package.json          private root; scripts and shared devDependencies only
  pnpm-workspace.yaml   the workspace's members (packages/*, test-utils, the demo host)
  pnpm-lock.yaml        committed; installs run with --frozen-lockfile
  tsconfig.base.json    strict base every package extends
  eslint.config.mjs     one flat config for the whole workspace
  packages/
    tokens/             @speed/tokens      -- design tokens, zero dependencies
    theme-presets/      @speed/theme-presets -- pure-data token presets (classic/modern/
                                             compact/glass/ascii) layered over the defaults
    i18n/               @speed/i18n        -- react-i18next wrapper + namespace registry
    ui-kit/             @speed/ui-kit      -- theme factory + controlled components + shared state primitives
    api-client/         @speed/api-client  -- hand-written HTTP runtime; fetch, retry, ApiError
    api-sdk/            @speed/api-sdk    -- generated API surface (orval output from the
                                             merged platform document); calls api-client through a seam
    layout-kit/         @speed/layout-kit -- AppShell/RouteGuard chrome, no auth opinion
    route-kit/          @speed/route-kit -- routing and navigation as one declarative
                                             registration surface: a RouteTable of
                                             path/view/label/permission declarations that
                                             RouteProvider installs over react-router-dom, with
                                             RouteView, useNavItems, RouteLink and useNavigate;
                                             no sibling package dependency
    auth-core/          @speed/auth-core  -- headless session layer over the generated authn
                                             surface; memory-only, no UI, no storage writes
    auth-ui/            @speed/auth-ui    -- the sign-in family over an auth-core session:
                                             password and SMS channels plus the social
                                             pair, sign-out and session-ended; controlled
    tenancy-ui/         @speed/tenancy-ui -- the tenant-switch affordance: one controlled
                                             TenantSwitcher fed the current tenant by the
                                             host, reporting each commit through onSwitched
    product-shell/      @speed/product-shell -- the tenant-facing assembly shell: ProductShell
                                             composes the AppShell frame, the sign-in family
                                             and the session hooks into one three-branch view
                                             machine, and renders one string of its own:
                                             the session-ended announcement under
                                             announcements.sessionEnded, from its own
                                             bilingual product-shell namespace (a zh-CN
                                             twin under the same key); its ./bootstrap
                                             subpath ships the app-entry assembly
                                             (bootstrapSpeedApp), which wires the whole page
                                             composition from one declarative definition
    account-ui/         @speed/account-ui    -- the signed-in account-management family:
                                             sessions, sign-in history, social bindings and
                                             step-up-gated two-factor setup over the
                                             generated authn operations
    admin-ui/         @speed/admin-ui  -- the platform-staff admin, the second shell:
                                             the tenant roster, settings and audit
                                             administration surfaces as one frame with three
                                             pages, each a path/view/label/permission
                                             declaration; no tenant switcher and no
                                             tenant-scoped key, since its endpoints do not
                                             resolve a tenant
    notification-ui/    @speed/notification-ui -- the notification center: the header bell,
                                             the inbox with read/mark-all-read, the per-type
                                             channel preference matrix, the composed page and
                                             the live server-sent-event subscription, over the
                                             generated notification operations
```

Root `package.json` holds only what every package shares (typescript,
vitest, eslint, typescript-eslint) plus the browser tier's runner
(`@vitest/browser`, `@vitest/browser-playwright`, `playwright`) and the
workspace scripts; a package's own `package.json` holds its dependencies,
peers and its `exports` map. There is deliberately **no React, no MUI, no
DOM/component test framework** at the root — vitest itself is shared
(every package unit-tests with it), but nothing DOM-shaped such as jsdom or
Testing Library, and no component rendering, lives there; each package
declares what it actually uses, so `pnpm -r` never installs a framework a
package does not need. The browser runner is the one exception, and it
carries no DOM-testing layer of its own: the cases reuse the packages'
existing Testing Library, theme and i18n fixtures.

The `api-sdk/` entry is different by design: it ships **generated**
source. `src/index.ts` is orval output from the merged platform document —
the OpenAPI fragments the platform modules declare, merged by the demo host's
`gen` command — stamped with a
DO-NOT-EDIT header carrying the pinned orval version, regenerated from
`web/` (pinned `pnpm dlx orval@8.17.0` plus
`scripts/orval-nodenext-fixup.mjs`; orval itself is never a workspace
dependency, so it never enters the lockfile) and never hand-edited. Its only
hand-written source is the seam `src/runtime.ts`, kept out of orval's way by
an exports-map `./runtime` subpath; the package's AGENTS.md documents the
seam and the fixup's why. `@speed/auth-core` sits directly above it — the
generated authn surface's first in-workspace compile consumer, whose suite
binds the same `./runtime` seam a host's real client binds.

## Commands (run from web/)

```
pnpm install --frozen-lockfile   # reproduce CI's install exactly
pnpm -r lint                     # eslint over every package (flat config)
pnpm -r typecheck                # tsc --noEmit over every package
pnpm -r test                     # vitest run over every package
pnpm -r build                    # emit dist/ (ESM .js + .d.ts) per package
```

Single-package runs work from the package directory (`pnpm test`, `pnpm
build`, ...) or with `pnpm --filter @speed/<name> <script>` from `web/`.

## Browser tier

The cases whose subject is real layout or real focusability live beside
the component under test as `*.browser.test.tsx` and run in Chromium
through Vitest's browser mode (`web/vitest.browser.config.ts`, the
Playwright provider). They are excluded from each package's default
`test`, `typecheck` and `lint` legs, and they are not part of
`make check`: the root `make web-browser` entry runs only them, and the
scheduled `.github/workflows/browser.yml` job calls that same entry.

The tier's `include` also names the demo host's route-level sweep
(`starters/demo/web/src/a11y/routes.browser.test.tsx`): it walks the
assembled app's routes in real Chromium to settle axe's
`label-content-name-mismatch` (WCAG 2.5.3), which jsdom cannot decide. The
demo lives outside `web/` but is a workspace member, so the config names
its directory for Vite's file access as well.

Chromium is not part of the `.mise.toml` toolchain; install it once:

```
pnpm -C web exec playwright install chromium
```

Then, from the repository root:

```
make web-browser
```

What the tier asserts is invariants, never pixels: geometry (each spacer
equals the fixed head's height, a point below the head hits content
itself, a table container does not overflow its own width), focus order
(the skip link is the first Tab stop) and focusability (axe's
`scrollable-region-focusable` rule finds nothing). Its first batch covers
the shell's offset and spacer across three viewport classes and three
banner heights, the skip link, the impersonation banner's live region,
the data table's per-column content cap, and every admin list page's
horizontal containment at the sm/md/lg floor widths with fixed data. The
two shells' header band invariant runs as the full matrix: ProductShell and
AdminShell, each at 375px and 430px in en-US and zh-CN, plus the md
single row, with every rendered header control asserted reachable and
clickable.

It does not cover color contrast, screenshot or pixel regression, or
anything a jsdom unit test can already assert. Geometry assertions keep a
one-pixel tolerance for subpixel rounding. The setup pins the document's
family to Arial / Liberation Sans for Latin text and to full-width CJK
families for Chinese text, so the measured text uses metric-compatible
fonts on macOS and on the CI image; measurements still belong to the
machine's layout engine, which is one reason the tier runs on a schedule
rather than in the commit gate.

## Package shape (all packages follow it)

- `"type": "module"`; **ESM-only** publish: `exports` maps point `types` and
  `import` at `dist/` `.d.ts`/`.js` pairs. NodeNext consumers import
  normally; no CJS twin ships.
- `"files": ["dist", "README.md", "AGENTS.md"]` — the dist/ tree plus the
  docs travel with the package.
- `tsconfig.json` (typecheck) extends `../../tsconfig.base.json` with
  `noEmit`; `tsconfig.build.json` emits declarations into `dist/` with
  `rootDir: src` so tests and `test-utils/` never leak into the artifact.
- The build runs under `module`/`moduleResolution: nodenext`, which emits
  relative imports verbatim: **sources write explicit `.js` extensions on
  every relative import** (TS2835 otherwise), so the shipped `dist/` is
  loadable by Node ESM and typecheckable by NodeNext consumers as-is.
- Sources: one domain per file, tests colocated (`x.ts` -> `x.test.ts`),
  shared test helpers under the package's `test-utils/`, never duplicated.
- Repository language rule: everything here is English; CJK-bearing
  fixtures live only under a `locales/`-named directory and `.ts` sources
  assert against imported fixtures rather than embedding language literals.

## Status

The release plan is the lockstep plan shared with the Go half: the
changesets bootstrap at `.changeset/` (a fixed version group over the
deliverable packages in `web/packages/`, `config.json`). The release
coordinator that used to verify it from the repository root -- that the
fixed group covers exactly the packages that exist, that package versions
are uniform, and that the Go half of the same one-version plan is
consistent -- retired with the earlier scaffolding, and the release plan
is deferred to its own round: nothing in the tree verifies the fixed
group today. Real publishing — changesets installed in `web/` with version
and publish runs — comes with the v1.0 release.

The lockfile is **not** carried over from the earlier implementation: the
workspace's members changed when this tree was rebuilt, so `pnpm-lock.yaml`
is regenerated by the install leg, never copied.
