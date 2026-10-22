# starters/demo/web

The demo skeleton's frontend application. It composes the platform's `@speed/*`
packages into one browser program and builds into `../server/dist`, which the Go
program beside this directory embeds with `go:embed` and serves through
`pkg/webui`. See `README.md` for the layout and the commands.

## Contract

- **An application, not a package.** Nothing imports this directory; it is a
  workspace member of `web/` so the one toolchain installs, lints, typechecks,
  tests and builds it, and it is never published. Its frontend dependency on
  the platform is the published `@speed/*` packages.
- **It composes, it does not reimplement.** Sessions, the shells, theming,
  routing and i18n come from the packages; this directory wires them and owns
  only what varies per product -- the route table, the pages, the deployment's
  runtime origin, the app's own strings.
- **No business domain.** `src/pages/` and the business slot in `src/routes.tsx`
  are the place a product's own pages land. The skeleton's own pages are the
  starting page at `/` -- a card grid over the capabilities it assembles
  (`src/pages/home-page.tsx`) -- and the account page; a product replaces the
  starting page's cards with its own surfaces.
- **The build order is frontend first, Go second.** The Go program embeds
  `../server/dist`, so that directory must exist and hold a file before the Go
  build runs -- which is why a checkout commits the stand-in `placeholder.txt`
  there. The build writes nothing to that name and does not clear the directory
  (`vite.config.ts` sets the outDir and disables vite's default emptying), so
  the stand-in survives every build.
- **The build output lands in the Go module beside this one.** `go:embed` reaches
  only files under the embedding package's own directory, so the bundle cannot
  stay here and be embedded from there.
- **`server/dist/` is a build output.** It is git-ignored; the only tracked file
  in it is the stand-in `placeholder.txt` (committed with `git add -f`).
  Everything a build writes there is ignored, so building leaves the working
  tree clean. `pkg/webui` answers 404 to every request until the frontend is
  built, rather than failing to start.

## Rules

- **Every user-facing string renders from the "demo" i18n namespace**
  (`src/locales/{en-US,zh-CN}.json`, identical leaf key sets). A navigation
  label that must translate reactively is a component (`src/nav-label.tsx`),
  because a declaration's label is a React element.
- **The sign-in surface carries no deployment-operation copy.** The skeleton
  folds registration into the sign-in flow and reads the login methods from
  the deployment's discovery document, so `src/pages/sign-in-view.tsx` frames
  `SignInScreen` with the app's brand and heading only: which methods are
  enabled, and whether registration is offered, are the deployment's facts
  rather than text the page asserts. A product that wants its own copy adds
  it as host content.
- **Every backend call goes through a generated operation** -- the platform's,
  from `@speed/api-sdk`, or the app's own, from `src/app-api` -- over the
  request function the assembly binds into the runtime seam
  (`bootstrapSpeedApp`). The app's `src/app-api/runtime.ts` re-exports the
  mutators `@speed/api-sdk/runtime` exports, so one binding at bootstrap serves
  both generated surfaces. The one hand-written request is `src/runtime-config.ts`,
  which reads a static deployment asset beside the entry document -- a static
  file, not an API endpoint -- and is the module `eslint.config.mjs` exempts
  from the no-direct-http rule.
- **The tenant rides the transport, not the generated calls, and it rides it by
  its alias.** The backend's `api` endpoint resolves the tenant from the
  `X-Tenant` header (the header extractor the platform defaults select, whose
  header name is the module's own default) and looks the name up in the
  roster's alias column, so the value announced is the current tenant's
  **alias**, never its id. The host keeps the two apart: the switcher keys on the
  tenant id (`getCurrentTenantId` in `src/chrome.tsx`; a switch goes through the
  same file's `useTenantSelection`, which writes it, and `DemoApp` clears it when
  a session ends), while `bootstrapDemoApp` wires the client's `tenantProvider` to
  `getCurrentTenantAlias`; the client announces it on every request, per attempt.
  A rename moves the alias and leaves the id, so `src/app.tsx` reads the roster
  (`tenantAdminTenantsList`, the app's own generated operation) once a viewer is
  signed in and announces each tenant's current alias; a tenant-scoped `api` call
  without the header -- or with a stale name -- is refused 404. That roster read
  is `tenant.admin`'s platform-scoped operation (`tenant.read` in the platform
  domain): the demo's operator holds it, but an ordinary member is answered 403,
  and the app then announces the placeholder alias (`DEMO_TENANTS`), which answers
  only while the roster's alias still equals it -- after a rename a member without
  the permission is refused 404, and no member-facing endpoint carries the current
  alias. A copy that signs in ordinary members must supply their own alias source
  (the tenants their memberships name, each with its current alias, handed to
  `setTenantAliases`) or keep each tenant's alias equal to the placeholder's. The
  tenant is not a claim of the access token. See README "The tenant on the wire".
- **The app's own API surface is generated, not written.** `src/app-api/index.ts`
  is orval output from `contracts/api.json` through `orval.config.ts`,
  regenerated by `pnpm api:gen` and committed; `src/app-api/runtime.ts` is the
  only hand-written file in that directory, and orval's output paths never cover
  it. `src/app-api/features.ts` is generated by the same command: it is the
  module augmentation that adds the application-owned feature names
  (`workspace.personal`) to `FeatureName`, which the published package's
  registry deliberately does not carry. `api:gen` also runs the host's `gen`
  command and the app's own copy of the feature-name generator, so the
  document and the vocabulary it generates from are fresh rather than stale
  committed copies. The platform package's `features.ts` belongs to
  `web/package.json`'s `api:gen`; this app's command never writes it.
- **The deployment's runtime facts are read at runtime, never baked in**
  (`src/runtime-config.ts`): `apiOrigin` (falling back to same-origin),
  `shellLayout` (`topbar` or `sidebar`, falling back to `topbar`) and
  `themePreset` (one of the shipped preset ids, falling back to `default`).
  One build output serves every deployment topology and white-label style.
  Each field is read on its own -- a malformed value never drops the others
  -- and an unknown value is ignored. The layout rides down through
  `DemoApp` to both `ProductShell` and `ConsoleShell`, so the tenant area
  and the console present the same arrangement; the preset seeds the theme
  provider's `defaultPreset`, and a device-stored choice wins over it at
  boot.
- **The app reaches the API at one origin, which must be a gateway.** The demo's
  Go program mounts the api, account and admin surfaces on separate ports, so the
  built bundle served straight from it renders for static browsing but cannot
  sign in; a deployment fronts the bundle and those endpoints behind one origin
  (as `pnpm dev` proxies in development). See README "One origin for the API".
- **The two shells live in disjoint path spaces.** The tenant area renders
  `@speed/product-shell`; the console area renders `@speed/console-ui` under
  `CONSOLE_PREFIX`. `src/app.tsx` picks the shell from the route table's
  selected entry, so a console page carries a label.
- **The console has a host entry and a host route guard.** The tenant
  navigation is the labelled entries outside the console path space; the console
  entry is not among them -- it rides the tenant frame's `headerActions` slot
  (`src/app.tsx`'s `ConsoleEntry`) and appears only to a principal holding a
  platform permission, so an ordinary member is offered no way in. The
  principal's permission sets are attached by the assembly
  (`src/platform-permissions.tsx`, from the caller's own permission read at
  `GET /api/v1/authz/me/permissions`; see README "Sessions and the console"). `/console` itself redirects to the
  console's first page (`src/routes.tsx`). The console area -- and only it, since
  the tenant area is gated by the shell -- carries a `RouteGuard` over the
  matched page's permission code, composed host-side, so reaching `/console`
  directly is turned away there.
- **The console routes in place and offers an exit.** The console frame hands
  its navigation entries to the app's router through console-ui's `onNavigate`
  seam (`src/console/console-area.tsx`), and its frame chrome carries an exit back to
  the tenant area beside the sign-out action. Both push a history entry instead
  of reloading the page, so neither the console pages nor the exit are a
  navigation dead end.
- **The app composes its own chrome.** `src/chrome.tsx` holds the shared top-bar
  pieces the tenant frame and the console frame render: the brand, the
  app-wide header actions (the language switch and the color-mode toggle), the
  tenant switcher over the app's placeholder tenant list (`TenantMenu`), and
  the avatar account menu (profile / account settings / sign out). The tenant
  frame puts the actions in `headerActions` and the switcher plus the account
  menu in `userMenu`; the console frame renders the actions and the account
  menu but not the switcher (the console resolves no tenant). The slot
  composition is identical in both `shellLayout` forms: AppShell moves the
  same slots into its drawer paper in the sidebar form, so `src/chrome.tsx`
  never branches on the layout. `src/paths.ts`
  holds the route paths the chrome links into, shared with the route table.
- **The account page mounts the account-ui family.** `src/pages/account-page.tsx`
  composes `@speed/account-ui`'s sections -- the self-service writes
  (profile, password, identifiers and the registration contact's
  verification), then the appearance theme choice, then preferences,
  sessions, social bindings and MFA -- under a
  `@speed/ui-kit` `PageHeader`, at `/account` in the tenant
  navigation. The appearance section is `account-ui`'s controlled
  `AppearanceSection`: the page maps the shipped preset shelf to this
  bundle's names and passes the provider's active preset and setter
  (`useAppThemePreset`), and a pick calls `setPreset` and
  `persistThemePreset` in one handler, so the theme applies at once and the
  device remembers it (`demo-web.theme-preset`; `src/chrome.tsx` resolves
  the boot choice as device > deployment > `default`). The phone write is
  deliberately left out: its two routes are
  registered only when the deployment delivers an SMS code provider, which this
  skeleton's baseline does not (the SMS family needs a carrier's credentials, so
  a product adds it), and a section whose submit would answer 404 is a write the
  page cannot keep. The skeleton configures no social provider either, so the
  bindings section lists existing bindings with no add area, and the
  binding-callback route is mounted beside it for a product that configures one.
  `src/main.tsx` registers the extra namespaces (console-ui, account-ui,
  objectstore-ui, tenancy-ui, notification-ui, demo) beside the four the
  assembly registers automatically.
- **Registration verification has its own page, banner and ensure gate.**
  `/account/verify` (`src/pages/account-verify-page.tsx`, an account-level
  route) renders account-ui's `ContactVerificationSection` under the session
  gate, and a password registration's automatic sign-in lands there
  (`src/pages/sign-in-view.tsx`). The tenant frame reads the profile's
  `verification.state` (`GET /api/v1/auth/me`) and does three things with it:
  it does not call the personal-tenant ensure while the state is `pending`; on
  a 409 `workspace.verification-required` it navigates to the verification
  page rather than rendering the retryable failure placeholder; and it
  resumes the ensure once the state becomes `verified` (the profile read is
  invalidated by the verify write). A persistent notice below the app bar
  (`src/contact-verification-banner.tsx`) shows the target address and the
  verify/change links while pending; verify goes to `/account/verify`, change
  to `/account/verify?change=email`, whose page starts the section in the
  change-address form. It is a `region`, not a live region, and
  it has no close control: it stays until the address is proved. It is
  separate from the once-only "finish account setup" notice -- the two
  predicates are `contactVerificationPending` and `setupIncomplete` in
  `src/account-setup.ts`, and a password registration is setup-complete while
  verification is still pending.
- **A page reload keeps the viewer signed in.** The session is delivered as
  the server's httpOnly cookie (the login flow requests `delivery: 'session'`,
  the client sends it with `credentials: 'same-origin'`), so a reload
  re-establishes the session from that cookie instead of dropping to anonymous.
  The two areas still show different surfaces when a session genuinely ends
  (a sign-out, or a cookie that no longer holds): the tenant area is gated by
  product-shell, whose session-ended branch renders the end screen with its own
  "sign in again" action for a session that turned anonymous after having been
  authenticated (only a never-authenticated visitor meets the sign-in surface);
  the console area carries its own session gate, which sends any anonymous
  viewer -- including one whose authenticated session just ended -- straight to
  the sign-in surface. Either way a request that dies with its session never
  leaves a view stuck loading. Nothing persists a credential to storage; the
  cookie is the browser's, not a script's.

## Checks

Run from this directory, or from `web/` with `pnpm --filter demo-web <script>`:

```
pnpm lint
pnpm typecheck
pnpm test
pnpm test:a11y
pnpm build
```

The tests use vitest's defaults: this tree deliberately does not take
`web/vitest.shared.ts`, the package library's shared preset, so a case here
times out at 5s rather than the workspace's 20s (a product copied from it whose
tests grow heavier should set its own `testTimeout`).

`test:a11y` is the route-level accessibility sweep (`src/a11y/`): it walks the
route table and renders the assembled app at every route in the tenant and
console shells, in three data states, scanning each with axe-core and failing on
`serious`/`critical` findings **plus any page-structure violation** — a missing
h1, a missing main landmark, a landmark-containment break or a heading-order
break (`src/a11y/scan.ts`'s `PAGE_STRUCTURE_RULES`; those rules carry only axe's
best-practice tag, so the gate runs them by rule id). `moderate`/`minor`
non-page-structure findings are warned and registered. Component-level scans,
where a product adds them, sit one tier below and fail on *any* violation, so
the route gate's impact threshold is the looser of the two — a page-level
regression net, not a superset of a component scan's threshold. `test:a11y` is its own
frontend-gate leg beside lint / typecheck / test -- the root Makefile's
`web-a11y` -- and the ordinary `test` run excludes `src/a11y/**`, so it runs
once. axe covers only the mechanically decidable part of WCAG (roughly 30-40%);
the gate proves no regression, and does not replace manual review. See the
frontend coding standards' Accessibility section.

Checks the jsdom gate cannot make, recorded so they are not read as
"covered" (each needs a real browser / a person):

- **Color contrast (WCAG AA)**. jsdom performs no layout or color computation,
  so axe's `color-contrast` is indeterminate for every node. The gate excludes
  it from its rule set (`src/a11y/scan.ts`'s `UNSWEEPABLE_RULES`); before the
  exclusion the named undecidable nodes were the AppBar brand text at `/` and a
  breadcrumb link at `/console/audit/sample [error]`, both chrome text over an
  emotion-styled background jsdom cannot resolve. Contrast there is a
  browser-side / manual check.
- **Label in Name (WCAG 2.5.3)** and **ARIA required children (WCAG 1.3.1)**.
  Both are inside the gate's rule set (axe ids `label-content-name-mismatch`,
  `aria-required-children`) yet jsdom resolves each only to `incomplete`, so
  the gate warns and registers them (`result.incomplete`) and never fails on
  them. `label-content-name-mismatch` compares a control's visible text
  against its accessible name and must read the visible text from a rendered
  box, so it is indeterminate under jsdom — measured at 1 node on nearly every
  route. `aria-required-children` (axe rates it critical) probes an element's
  required descendants through visibility, likewise layout-bound — measured at
  1 node on `/account [empty]`, the headerless empty placeholder's
  `<ul role="list">`. A genuine 2.5.3 or 1.3.1 defect therefore cannot fail
  this gate. Note the sweep's guard in `routes.test.tsx` asserts only that
  `color-contrast` stays out, so these two rules run and report as
  indeterminate rather than being swept out; both are browser-side / manual
  checks like contrast.
- **Touch-target size (≥44px, WCAG 2.5.5)** and **screen-reader behaviour
  (VoiceOver/NVDA)**. Neither is measurable in jsdom. MUI's default
  `IconButton` box is ~40px (and `size="small"` smaller), below the 44px
  target; the shipped theme sets no minimum target size, so a deployment that
  requires it needs a theme-level minimum-size override. Both are manual /
  user-side checks, not verified here.

The app is also a leg of the workspace's `make web-check` and an input to the
Go program's build (`go -C starters/demo/server build ./...`), which embeds the
output.
