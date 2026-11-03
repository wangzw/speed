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
- **A control group reports through one feedback region.** The host's `src`
  runs the workspace's `speed/no-double-feedback-region` rule from
  `web/eslint-rules/`, with the same exemption list and the same reasons;
  the host is part of the product, so a component that renders two
  `WriteFeedbackRegion` elements can show two results at once there too.
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
  A rename moves the alias and leaves the id, so `src/app.tsx` reads the
  caller's own tenant list (`GET /api/v1/auth/me/tenants`, the `tenant.self`
  module) once per identity and takes each tenant's current alias from it. A
  platform staff member's roster read (`tenantAdminTenantsList`, `tenant.admin`'s
  platform-scoped operation, `tenant.read` in the platform domain) adds the
  aliases of tenants the caller list does not hold -- a staff member entering a
  tenant they are not a member of -- and names the staff default; an ordinary
  member is answered 403 there and settles from its own list alone. Neither
  source has a placeholder behind it: when no source names the current tenant,
  `getCurrentTenantAlias` answers null, the client announces nothing, and
  tenant-scoped reads stay disabled until the host can name one.
  A tenant-scoped `api` call without the header -- or with a stale name -- is
  refused 404. The tenant is not a claim of the access token. See README "The
  tenant on the wire".
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
  `DemoApp` to both `ProductShell` and `AdminShell`, so the tenant area
  and the admin present the same arrangement; the preset seeds the theme
  provider's `defaultPreset`, and a device-stored choice wins over it at
  boot.
- **The app reaches the API at one origin, which must be a gateway.** The demo's
  Go program mounts the api, account and admin surfaces on separate ports, so the
  built bundle served straight from it renders for static browsing but cannot
  sign in; a deployment fronts the bundle and those endpoints behind one origin
  (as `pnpm dev` proxies in development). See README "One origin for the API".
- **The two shells live in disjoint path spaces.** The tenant area renders
  `@speed/product-shell`; the admin area renders `@speed/admin-ui` under
  `ADMIN_PREFIX`. `src/app.tsx` picks the shell from the route table's
  selected entry, so an admin page carries a label.
- **The admin has a host entry and a host route guard.** The tenant
  navigation is the labelled entries outside the admin path space; the admin
  entry is not among them -- it rides the tenant frame's `headerActions` slot
  (`src/app.tsx`'s `AdminEntry`) and appears only to a principal holding a
  platform permission, so an ordinary member is offered no way in. The
  principal's permission sets are attached by the assembly
  (`src/platform-permissions.tsx`, from the caller's own permission read at
  `GET /api/v1/authz/me/permissions`; see README "Sessions and the admin"). `/admin` itself redirects to the
  admin's first page (`src/routes.tsx`). The admin area -- and only it, since
  the tenant area is gated by the shell -- carries a `RouteGuard` over the
  matched page's permission code, composed host-side, so reaching `/admin`
  directly is turned away there.
- **The admin routes in place and offers an exit.** The admin frame hands
  its navigation entries to the app's router through admin-ui's `onNavigate`
  seam (`src/admin/admin-area.tsx`), and its frame chrome carries an exit back to
  the tenant area beside the sign-out action. Both push a history entry instead
  of reloading the page, so neither the admin pages nor the exit are a
  navigation dead end.
- **The app composes its own chrome.** `src/chrome.tsx` holds the shared top-bar
  pieces the tenant frame and the admin frame render: the brand, the tenant
  switcher over the caller tenant list (`TenantMenu`), and the avatar account
  menu (the notification centre, the account panes and sign out). Neither
  frame carries a color-mode toggle: the choice is the account page's
  `ColorModeSection`.
  `TenantMenu` passes `announcement="visually-hidden"`, so a switch is
  announced through the status region but never painted beside the trigger.
  The tenant frame puts the actions in `headerActions` and the tenant switcher
  in `userMenu`. The account entry is placed by the literal constant
  `ACCOUNT_ENTRY` in `src/account-entry.ts`: `'sidebar'` (the default) renders
  `AccountEntry` at the bottom of the navigation column through AppShell's
  `navFooter` slot and no header avatar menu; `'header'` renders the
  `UserMenu` in the `userMenu` slot and no column row; `'both'` renders both.
  The admin frame follows the same `ACCOUNT_ENTRY` constant (default
  `'sidebar'`: a row at the bottom of the column, no header avatar menu;
  `'header'`: the header menu only; `'both'`: both), and it renders the
  actions but not the switcher (the admin resolves no tenant). Entry-point actions in that chrome are always outlined; plain text
  is reserved for labels that are not clickable. The slot composition is identical in both `shellLayout` forms:
  AppShell moves the same slots into its drawer paper in the sidebar form, so
  `src/chrome.tsx` never branches on the layout. `src/paths.ts` holds the
  route paths the chrome links into, shared with the route table.
- **The account entry is the bottom bar of the navigation column.** In the
  `'sidebar'` placement `src/chrome.tsx` renders a `Divider` above a full-width
  row carrying the signed-in user's avatar, display name and disclosure
  chevron, with 12px above and below the row. The avatar starts 16px in from
  the column's edge -- the row's leading inset -- and its icon box advances the
  name to the point a navigation label starts. All of these are fixed pixel
  values, not theme spacing steps, so a preset with a different spacing base
  moves none of them. The row opens a menu upward whose entries each carry the
  navigation's own icon mark: a 24px glyph centred in the navigation's icon
  cell, MUI's `ListItemIcon` reserve of `theme.spacing(4.5)` (36px at the
  default 8px spacing base). The cell scales with the spacing base exactly as
  the navigation's does, so in every preset an entry's icon cell and label
  start where a navigation item's do: the notification centre, the four
  account panes and sign out; an impersonation adds its end entry above them.
  The menu paper keeps the preset's fill and gains only a 1px divider-coloured
  border and the theme's elevation-12 shadow; it is 238px wide and sits 9px in
  from the column's edge. The navigation column itself lists the home entry
  alone: the notification centre and the account panes are menu destinations,
  not navigation items.
- **The account settings is one pane per route.** `src/pages/account-page.tsx`
  composes `@speed/account-ui`'s sections into four panes, one route each:
  `/account/profile` (profile, identifiers and the registration contact's
  verification), `/account/security` (password, sign-in ways, social bindings
  and MFA), `/account/sessions`, and `/account/preferences` (the appearance
  theme choice, the color mode, language and time zone). The preferences pane is the only
  place the interface language is set; the header carries no language switch.
  `src/paths.ts` declares the pane list
  and its paths. `src/routes.tsx` gives every pane a page of its own and makes
  `/account` a group address with no page: it replaces the history entry with
  the pane an older `?section=` named, else `/account/profile`, carrying the
  rest of the query along so `/account?verify=1` and `/account?change=email`
  still land where they meant to. Each pane composes a `@speed/ui-kit`
  `PageLayout` naming it, and every block inside a pane is its own outlined
  card. The four panes are entries of the account entry's menu, each with its
  own glyph. The appearance section is `account-ui`'s controlled
  `AppearanceSection`: the page maps the shipped preset shelf to this
  bundle's names and passes the provider's active preset and setter
  (`useAppThemePreset`), and a pick calls `setPreset` and
  `persistThemePreset` in one handler, so the theme applies at once and the
  device remembers it (`demo-web.theme-preset`; `src/chrome.tsx` resolves
  the boot choice as device > deployment > `default`). The color mode sits
  beside it as `account-ui`'s `ColorModeSection`, which reads and writes the
  account's `color_mode` itself; the page supplies the provider's current
  preference as `effective` and, in `onApplied`, calls the provider's
  `setPreference` and `persistColorModePreference`, so the account is the
  source of truth and `demo-web.color-mode` is only the first-paint mirror.
  A `ColorModeSync` in the tenant frame applies a stored account value that
  differs from the current preference once the profile read lands. The phone write is
  deliberately left out: its two routes are
  registered only when the deployment delivers an SMS code provider, which this
  skeleton's baseline does not (the SMS family needs a carrier's credentials, so
  a product adds it), and a section whose submit would answer 404 is a write the
  page cannot keep. The skeleton configures no social provider either, so the social card is
  the assembly's answer: the page asks `useSocialMethodsAvailable` and
  renders the card only when the deployment serves a redirect method or the
  account already holds a binding (an existing binding stays visible even
  when that read fails), and the binding-callback route is mounted beside
  the pane for a product that configures a provider.
  `src/main.tsx` registers the extra namespaces (admin-ui, account-ui,
  tenancy-ui, notification-ui, demo) beside the four the assembly registers
  automatically.
- **Registration verification lives on the profile pane, behind a banner.**
  The verification surface is `@speed/account-ui`'s
  `ContactVerificationSection`, rendered as the email card on `/account/profile`
  rather than a route of its own. A password registration's automatic sign-in
  lands on `${ACCOUNT_PATH}?verify=1` (`src/pages/sign-in-view.tsx`); the group
  address redirects that to `/account/profile?verify=1`, where the pane reads
  the flag and passes `autoSend`. The tenant frame does not read the profile's
  `verification.state` for the ensure: it calls the personal-tenant ensure
  whenever an authenticated, non-impersonating viewer has no current tenant,
  and an unverified account gets its workspace like any other. A persistent
  notice below the app bar
  (`src/contact-verification-banner.tsx`) shows the target address and the
  verify/change links while pending; verify goes to `/account?verify=1`, change
  to `/account?change=email`, and the group address maps each onto the profile
  pane. The plain landing sends one code per mount once the profile reads
  `pending` with a target address; the change landing starts the section in the
  change-address form and sends nothing, even after the form is closed. It is a
  `region`, not a live region, and it has no close control: it stays until the
  address is proved. Its predicate is `contactVerificationPending` in
  `src/contact-verification.ts`.
- **The anonymous password reset has its own page and entry.**
  `/password-reset` (`src/pages/password-reset-page.tsx`) renders
  auth-ui's `PasswordResetScreen` outside both shells: the viewer arrives
  without a session, so no tenant context or authenticated frame is
  involved. The page reads the deployment's channels from the same discovery
  document the sign-in surface reads (`session.listMethods`, and its
  `password_reset` member) and binds the screen's operations to the app's
  generated surface (`src/app-api`); an absent or disabled member renders the
  screen's unavailable notice. `SignInScreen` carries the forgot-password
  entry only while that member is enabled, and only the application's own
  sign-in page binds `onForgotPassword`: the invitation landing and the
  admin gate own their landing and leave the entry out, because navigating
  away would drop the token or the path the viewer arrived with.

- **A page reload keeps the viewer signed in.** The session is delivered as
  the server's httpOnly cookie (the login flow requests `delivery: 'session'`,
  the client sends it with `credentials: 'same-origin'`), so a reload
  re-establishes the session from that cookie instead of dropping to anonymous.
  The two areas still show different surfaces when a session genuinely ends
  (a sign-out, or a cookie that no longer holds): the tenant area is gated by
  product-shell, whose session-ended branch renders the end screen with its own
  "sign in again" action for a session that turned anonymous after having been
  authenticated (only a never-authenticated visitor meets the sign-in surface);
  the admin area carries its own session gate, which sends any anonymous
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
admin shells, in three data states, scanning each with axe-core and failing on
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
once. axe's `label-content-name-mismatch` (WCAG 2.5.3) cannot be decided under
jsdom (its visible-text probe needs a real canvas, which the jsdom setup stubs
to null), so the sweep reports that rule's incomplete result as a hint with the
node's selector and never asserts it. The rule's verdict lives in the browser
leg below.

The browser leg: `src/a11y/routes.browser.test.tsx` walks the same route/state
matrix (`src/a11y/route-table.ts`) in real Chromium, rendering the assembled
app exactly as the jsdom sweep does but scanning only
`label-content-name-mismatch`. There the rule is determinate: an incomplete or
a violating node fails the case with its selector named, and the sweep asserts
the rule evaluated at least one node so an inapplicable run cannot read as a
pass. It also drives the shell's own chrome — both layouts, the account menu,
and the notification bell at zero, one and above the cap — because the shared
`renderWithProviders` harness a package uses cannot see how the demo composes
them. It is part of the workspace browser tier, not this app's `test` or
`test:a11y`: the root `make web-browser` entry discovers it through
`web/vitest.browser.config.ts` (which names this directory's glob) and runs it
in the one Chromium instance beside the packages' browser cases. The same tier
carries `src/a11y/form-actions.browser.test.tsx`, which opens every form on the
profile and security panes and measures each submit against its fields and the
form's content right edge. Chromium is not part of the `.mise.toml` toolchain,
so this leg is not in `make check`.

axe covers only the mechanically decidable part of WCAG (roughly 30-40%);
the gate proves no regression, and does not replace manual review. See the
frontend coding standards' Accessibility section.

Checks the jsdom gate cannot make, recorded so they are not read as
"covered" (each needs a real browser / a person):

- **Color contrast (WCAG AA)**. jsdom performs no layout or color computation,
  so axe's `color-contrast` is indeterminate for every node. The gate excludes
  it from its rule set (`src/a11y/scan.ts`'s `UNSWEEPABLE_RULES`); before the
  exclusion the named undecidable nodes were the AppBar brand text at `/` and a
  breadcrumb link at `/admin/audit/sample [error]`, both chrome text over an
  emotion-styled background jsdom cannot resolve. Contrast there is a
  browser-side / manual check.
- **Label in Name (WCAG 2.5.3)** and **ARIA required children (WCAG 1.3.1)**.
  Both are inside the gate's rule set (axe ids `label-content-name-mismatch`,
  `aria-required-children`) yet jsdom resolves each only to `incomplete`, so
  the gate warns and registers them (`result.incomplete`) and never fails on
  them. `label-content-name-mismatch` compares a control's visible text
  against its accessible name and reads the visible text through the canvas
  text-measurement API; jsdom's `getContext('2d')` returns null (the test
  setup stubs it), so axe's probe throws and the rule is indeterminate for
  every node. The browser tier settles it for real: `routes.browser.test.tsx`
  walks every route/state of this app, and the package cases cover the
  notification bell (zero, one and above the cap — at zero the bell renders no
  badge, so no hidden "0" can sit outside the accessible name), the inbox's
  row action, and the settings row's Remove control. Each runs the rule alone
  in Chromium and asserts neither violations nor incomplete results.
  `aria-required-children` (axe rates it critical) probes an element's
  required descendants through visibility, likewise layout-bound. A genuine
  2.5.3 defect therefore cannot fail the jsdom gate; these two rules run and
  report as indeterminate rather than being swept out. The sweep's guard in
  `routes.test.tsx` asserts that `color-contrast` and
  `aria-prohibited-attr` stay absent from the incomplete set, and neither of
  these two rules is asserted there.
- **Touch-target size (≥44px, WCAG 2.5.5)** and **screen-reader behaviour
  (VoiceOver/NVDA)**. Neither is measurable in jsdom. MUI's default
  `IconButton` box is ~40px (and `size="small"` smaller), below the 44px
  target; the shipped theme sets no minimum target size, so a deployment that
  requires it needs a theme-level minimum-size override. Both are manual /
  user-side checks, not verified here.

The app is also a leg of the workspace's `make web-check` and an input to the
Go program's build (`go -C starters/demo/server build ./...`), which embeds the
output.
