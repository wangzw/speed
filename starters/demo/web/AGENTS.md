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
- **The sign-in surface names where a fresh deployment's first account comes
  from.** The skeleton folds registration into the sign-in flow and ships
  neither SMS nor social sign-in, so a new deployment has no account and no way
  to make one from the surface; `src/pages/sign-in-view.tsx` carries a note
  under the form pointing at the server's `seed` command. A product that grows
  its own registration replaces the note with it.
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
  `X-Tenant` header (`server/testdata/*.yaml`) and looks the name up in the
  roster's alias column, so the value announced is the current tenant's
  **alias**, never its id. The host keeps the two apart: the switcher keys on the
  tenant id (`getCurrentTenantId` in `src/chrome.tsx`, written on a switch in
  `src/app.tsx`), while `bootstrapDemoApp` wires the client's `tenantProvider` to
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
  it. `api:gen` also runs the host's `gen` command, so the document it generates
  from is fresh rather than a stale committed copy.
- **The API origin is read at runtime, never baked in** (`src/runtime-config.ts`,
  falling back to same-origin). One build output serves every deployment topology.
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
  principal's platform permission set is attached by the assembly
  (`src/platform-permissions.tsx`, from the declared-permission catalog; see
  README "Sessions and the console"). `/console` itself redirects to the
  console's first page (`src/routes.tsx`). The console area -- and only it, since
  the tenant area is gated by the shell -- carries a `RouteGuard` over the
  matched page's permission code, composed host-side, so reaching `/console`
  directly is turned away there.
- **The console routes in place and offers an exit.** The console frame hands
  its navigation entries to the app's router through console-ui's `onNavigate`
  seam (`src/console/console-area.tsx`), and its banner carries an exit back to
  the tenant area beside the sign-out action. Both push a history entry instead
  of reloading the page, so neither the console pages nor the exit are a
  navigation dead end.
- **The app composes its own chrome.** `src/chrome.tsx` holds the shared top-bar
  pieces the tenant frame and the console banner render: the brand, the
  app-wide header actions (the language switch and the color-mode toggle), the
  tenant switcher over the app's placeholder tenant list (`TenantMenu`), and
  the avatar account menu (profile / account settings / sign out). The tenant
  frame puts the actions in `headerActions` and the switcher plus the account
  menu in `userMenu`; the console banner renders the actions and the account
  menu but not the switcher (the console resolves no tenant). `src/paths.ts`
  holds the route paths the chrome links into, shared with the route table.
- **The account page mounts the account-ui family.** `src/pages/account-page.tsx`
  composes `@speed/account-ui`'s sections -- the self-service writes (profile,
  password, identifiers), then preferences, sessions, social bindings and
  MFA -- under a `@speed/ui-kit` `PageHeader`, at `/account` in the tenant
  navigation. The phone write is deliberately left out: its two routes are
  registered only when the deployment delivers an SMS code provider, which this
  skeleton's baseline does not (the SMS family needs a carrier's credentials, so
  a product adds it), and a section whose submit would answer 404 is a write the
  page cannot keep. The skeleton configures no social provider either, so the
  bindings section lists existing bindings with no add area, and the
  binding-callback route is mounted beside it for a product that configures one.
  `src/main.tsx` registers the extra namespaces (console-ui, account-ui,
  tenancy-ui, demo) beside the four the assembly registers automatically.
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

`test:a11y` is the route-level accessibility sweep (`src/a11y/`): it walks the
route table and renders the assembled app at every route in the tenant and
console shells, in three data states, scanning each with axe-core and failing on
`serious`/`critical` findings (the WCAG 2.1 AA rule set; `moderate`/`minor` are
warned and registered). It is its own frontend-gate leg beside lint / typecheck
/ test -- the root Makefile's `web-a11y` -- and the ordinary `test` run excludes
`src/a11y/**`, so it runs once. axe covers only the mechanically decidable part
of WCAG (roughly 30-40%); the gate proves no regression, and does not replace
manual review. See the frontend coding standards' Accessibility section.

The app is also a leg of the workspace's `make web-check` and an input to the
Go program's build (`go -C starters/demo/server build ./...`), which embeds the
output.
