# @speed/test-utils

The workspace's shared frontend test-support package: the single home of
the DOM-test scaffolding the `@speed/*` packages would otherwise each
carry a copy of.

It is **private and never published** — a workspace member consumed by
the packages' tests, not a deliverable library. It lives beside
`packages/` rather than inside it because `web/packages/*` is the
release plan's deliverable set (the deferred lockstep release treats
exactly that directory as the npm publish set, without a private-package
filter and under one uniform version), and a private member inside that
set would enter the release plan and the publish loop.

The demo starter's web host — the workspace's other private member —
lives outside `web/packages/*` for the same reason, and it does **not**
consume this package. A copyable application has to declare every
`@speed` dependency by registry version range so its manifest installs
unchanged after a copy-out, and a never-published package has no registry
version to name. The demo host therefore carries its own equivalent
under `starters/demo/web/src/test-utils/` — its vitest setup and its
`DemoApp` render harness — instead of importing this one.

## Resolution

Consumers reach the package through the workspace's single package map
(`web/scripts/speed-aliases.mjs`): the specifiers `@speed/test-utils/<module>`
alias onto this package's live `src/<module>.ts(x)` in every vitest/vite
config (the map's `speedAliases()` derivation), and each consuming
package's tsconfig `paths` section pins the same targets for `tsc`. Each
consuming package declares the dependency in its `devDependencies`.
There is no `dist`: the package is consumed as source, the same way the
map resolves every workspace sibling, so `build` is the standalone
compile check.

The map is workspace-wide, but the demo host's tsconfig deliberately
carries none of this package's specifiers: the app imports none of them,
and its own fixtures live under `starters/demo/web/src/test-utils/`. The
host's alias-manifest suite compares `speedTsconfigPaths()` against the
map with these specifiers filtered out, so what it pins is the map's
projection minus the workspace's private test-support entries, not the
whole map.

This package's own `@speed` imports (the session harness drives
`@speed/auth-core` sessions over `@speed/api-client` and the
`@speed/api-sdk` runtime seam; the render layer builds on `@speed/i18n`
and on `@speed/ui-kit`'s display-time-zone provider) resolve through the
same alias map and resolve in `tsc` through this
package's `paths`, and are deliberately **not** declared in this
manifest: a declared edge onto a package that declares this one back --
auth-core, whose tests drive the harness -- would make the workspace
dependency graph cyclic, which pnpm reports as a standing warning on
every install. Only the third-party libraries (react, axe-core,
testing-library, react-query) are declared here, because those resolve
through `node_modules` rather than the map.

## Modules

- `axe.ts` — the axe-core assertion harness: `expectNoAxeViolations`
  (the widget tier, `region`/`landmark-one-main` disabled),
  `expectNoAxeViolationsForPage` (the page tier for packages whose unit
  under test is page-level chrome), and `runHeadingOrderCheck` (the
  single-rule probe for level-dependent headings). Each package's
  `test-utils/axe.ts` re-exports the tier its scans need. Two DOM
  indeterminacies are pinned rather than left to whoever reads the log:
  while an overlay is open, the page-context exemption is applied only
  after the helper asserts the page behind it is `aria-hidden` (the
  ARIA-forbidden tags aside), and the browser tier decides
  `aria-hidden-focus` for an open aria-modal MUI Dialog or Drawer by
  pressing Tab and Shift+Tab through the overlay's focus trap — a trap
  that holds resolves the rule with no report, an escape fails the scan.
  Overlays Tab would dismiss (Menu, Select) keep their report, which
  names that reason. That report is an accepted known limitation, recorded
  in `docs/design/modules/web/design-web-ui-kit.md` under
  "非模态浮层的焦点空档".
- `render.tsx` — the render-primitive layer: `TEST_LANGUAGES`, the
  deterministic bilingual `createTestI18n` base, the
  retries-nothing `createTestQueryClient`, and the shared
  `RenderWithProvidersOptions` / `RenderWithProvidersResult` shapes.
  The render options carry the display time zone as well, and
  `TestTimeZoneProvider` wraps a tree in the display-time-zone context
  at `TEST_TIME_ZONE` unless the suite names another zone — a rendered
  time then does not depend on the machine's own zone. That default is
  neither the chain's terminal fallback (`UTC`) nor the browser zone the
  packages pin, so a suite that names no zone still tells the provider's
  effect apart from an implementation that skips it. Each
  package's `test-utils/render.tsx` keeps its own provider tree (which
  providers a package's units need is package policy) and builds it on
  these primitives.
- `setup.ts` — the vitest setup side effect every suite shares: jest-dom
  matchers plus the explicit RTL cleanup (vitest exposes no global
  `afterEach`, and RTL's automatic cleanup fires only off one).
- `matchMedia.ts` — the MediaQueryList stub for the AppShell frame's
  `useMediaQuery` call, with a handle to flip the reported answer and
  fire the stub's own change listeners for a mounted tree.
- `emitted-css.ts` — the emotion stylesheet-text probe for
  breakpoint-keyed `sx` rules jsdom cannot evaluate.
- `session-harness.ts` — the scripted-request-function harness for
  `@speed/auth-core` sessions: a fake `RequestFn` bound through the
  api-sdk runtime seam drives a real session over a fresh memory store.
- `real-client.ts` — the real-client network rig: a genuine
  `@speed/api-client` client over a fetch stand-in answering from a
  script, optionally over the session's own refresh seam; each
  consuming package projects the observed request into its own recorded
  call shape. Also the shared `jsonResponse` / `errorResponse` /
  `signInWithPassword` answer helpers.
- `timers.ts` — `restoreRealTimers()`, the one way a test hands the
  clock back to real timers. jsdom 31 serves `requestAnimationFrame` and
  its focus fixups from one lazily created interval; a focus change made
  while fake timers are installed arms it as a fake timer, and a direct
  `vi.useRealTimers()` strands it, so no frame runs again in that
  worker. The helper advances the fake clock through two rendering
  frames first, which lets the pending fixup run and the interval go
  idle, and restores the real clock in a `finally`. With no fake timers
  installed it only calls `vi.useRealTimers()`. The workspace guard
  rejects every direct `vi.useRealTimers` in the test sources and
  test-support sources it scans and exempts only this file and the demo
  host's copy.

## Testing

`src/session-harness.test.ts` is the harness's own regression (the
script dispatch gate's prototype-chain branch); `src/axe.test.ts` pins the
pure decisions the axe harness makes without a DOM (the tier it runs in,
decided by whether the browser setup installed the real-keyboard driver,
the tags that cannot carry `aria-hidden`, the overlay family a marker
class list names, and the check that holds each browser determination to
a browser case that runs the axe assertion); `src/axe.dom.test.ts` pins
the jsdom tier's side of that decision (a user agent that names a browser
does not hand a jsdom scan a keyboard it cannot press); and
`src/axe.browser.test.tsx` pins what an open overlay faces: a trapped
dialog passes with no report, an untrapped one's focus escape fails the
scan in either Tab direction, a background left readable fails the
page-context exemption, and a menu's unexercised trap is reported with
the reason. `src/timers.test.ts` reproduces the stranded-frame failure:
it takes the jsdom environment for its own file, makes a focus change
under fake timers, restores the real clock through `restoreRealTimers()`
and requires a frame requested after that to run. Run them with
`pnpm test` from this directory; the browser case runs under
`make web-browser`.
