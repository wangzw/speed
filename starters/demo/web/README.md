# starters/demo/web

The frontend application of the demo skeleton: the browser application a
copied-out product grows from. It composes the platform's `@speed/*` packages
into one program -- the session, the tenant-facing shell, the platform console
and the routing between them -- and builds into `../server/dist`, which the Go
program in the sibling directory embeds with `go:embed` and serves through
`pkg/webui`.

This directory is a pnpm workspace member of `web/` (see `web/pnpm-workspace.yaml`)
and lives outside `web/` on purpose: a delivered consumer project's frontend
sits with the project, not inside the package library.

## Develop

```
pnpm install --filter demo-web      # or: pnpm -C web install, from the repo root
pnpm dev                            # vite dev server
```

`pnpm dev` serves the app with hot module replacement on vite's own port. The
app talks to its own origin by default (the client base URL falls back to
`window.location.origin`), and the dev server proxies the namespaces the
backend endpoints claim onto the demo's backend listener: the api, account,
admin and content endpoints all answer on one port. The claimed prefixes live in
`backend-prefixes.json` beside this package (`/api`, `/content`; `/api/v1/auth`,
`/api/v1/features` and `/api/v1/workspace`;
`/api/v1/system/audit`, `/api/v1/system/auth`, `/api/v1/system/permissions`,
`/api/v1/system/settings`, `/api/v1/system/sms` and `/api/v1/system/tenants`), and
`vite.config.ts` generates one segment-anchored proxy key per prefix from that
list. The target defaults to the `sqlite-memory` stack's backend listener
(`127.0.0.1:8080`); override it with `DEMO_WEB_BACKEND_PROXY` when a run moves
it.

Run the backend first (`go -C starters/demo/server run . --config
file:testdata/sqlite-memory.yaml sqlite-memory serve`), then `pnpm dev`.

## Build

```
pnpm build                          # tsc typecheck, then vite build into ../server/dist
```

The output is one entry document (`index.html`) plus a content-addressed
`assets/` subtree, written into the Go module beside this directory: the Go
program embeds `../server/dist`, so **the frontend is built first and the Go
binary second** -- that order is a hard constraint on the combined deployment,
and it does not change when the bundle changes. The bundle lands there rather
than here because `go:embed` reaches only files under the embedding package's
own directory and a pattern may not climb out with `..`.

`server/dist/` is a build output and is git-ignored, but `go:embed` refuses to
compile a directory with no files in it, so a checkout carries one committed
stand-in there, `placeholder.txt`. The build neither writes nor clears it
(`vite` would empty the outDir by default; `vite.config.ts` sets the outDir and
turns that off), and everything the build does write under it is ignored, so
building leaves the working tree clean. Until the frontend is built the
directory holds no entry document, and `pkg/webui` answers 404 to every request
rather than failing to start.

## The API contract

The typed API surface comes from two generation levels, and a command per level
owns its outputs:

```
pnpm api:gen                        # from web/: regenerates the platform package
pnpm --filter demo-web run api:gen  # from web/: regenerates this app's own surface
```

This app's `api:gen` runs four legs in order:

1. The Go host's `gen` command merges the OpenAPI fragments the binary's imports
   reach and writes the merged document to `contracts/api.json` at the
   repository root, and with `--features` writes the feature vocabulary to
   `contracts/features.json` beside it (`starters/demo/server/main.go`'s
   `genDocument`). Every feature entry and every operation carries an owner
   (`platform` or `application`) that `gen` derives from the declaring
   module's Go package; this app's own route and feature are application-owned.
2. `node scripts/feature-names.mjs ../../../contracts/features.json
   src/app-api/features.ts` generates this app's module augmentation of the
   feature registry from the application-owned entries. The script is this
   app's own copy; the platform-owned half of the vocabulary is the platform
   package's output and this app never writes it.
3. `pnpm dlx orval@8.17.0 --config orval.config.ts` generates this app's own
   surface from that document into `src/app-api/index.ts`.
4. `node scripts/orval-nodenext-fixup.mjs src/app-api/index.ts
   src/app-api/runtime.ts` rewrites the generated mutator import to its explicit
   `.js` form.

The platform level's `api:gen` (in `web/package.json`) owns the published
package's outputs: it generates `web/packages/api-sdk/src/features.ts` from the
platform-owned entries, then runs the platform orval configuration and the same
fixup. Neither command writes into the other's tree.

| Output | Owner | Command |
|---|---|---|
| `contracts/api.json`, `contracts/features.json` | the host's `gen` | this app's `api:gen` |
| `src/app-api/index.ts` | this app's orval configuration | this app's `api:gen` |
| `src/app-api/features.ts` | the app's feature-name copy | this app's `api:gen` |
| `web/packages/api-sdk/src/index.ts` | `web/orval.config.ts` | the platform `api:gen` |
| `web/packages/api-sdk/src/features.ts` | the platform feature-name generator | the platform `api:gen` |

The two generation scripts exist as an app copy and a repository copy. The
fixup is byte-identical on both sides; the feature-name generator differs only
in the documented generation-mode block (the repository copy also writes the
package registry). `web/scripts/starter-script-consistency.test.mjs` pins both
relationships.

orval is never a dependency of this workspace: `pnpm dlx` fetches the pinned
version on demand and it stays out of `web/pnpm-lock.yaml`.

| Level | Configuration | Input | Output |
|---|---|---|---|
| platform | `web/orval.config.ts` | `contracts/api.json`, excluding the application-owner tag | the published `@speed/api-sdk`, in `web/packages/api-sdk/src/index.ts` |
| app | `orval.config.ts` (this directory) | `contracts/api.json`, unfiltered | `src/app-api/index.ts` |

The platform level belongs to the package library and produces the package the
shell packages import; the app level belongs to the app and produces a surface
inside it. Both read `contracts/api.json` -- the document the host's `gen`
command writes, carrying every fragment the binary's imports reach, so the
platform fragments plus the app's own. This starter's own fragment is the
personal-tenant route (`starters/demo/server/personal-openapi.json`): it is in
the merged document and in the app surface, and the platform configuration's
`input.filters` excludes it so the published package stays platform-only. Both
levels reach the backend through the same seam: `src/app-api/runtime.ts`
re-exports the mutators `@speed/api-sdk/runtime` exports, so the one
`bindRequestFn` call at bootstrap serves both generated surfaces and no
generated or bridge file performs HTTP itself.

`src/app-api/index.ts` is generated output carrying a DO-NOT-EDIT header, and it
is committed -- regenerating it produces the same file. `src/app-api/runtime.ts`
is the one hand-written file there, and orval's output paths never cover it.

## Where the dependencies come from

This app declares its dependency on the published `@speed/*` packages by
registry version range (`^0.0.1`), **not** the workspace protocol. Inside this
repository the workspace still resolves those ranges to the sibling packages
under `web/packages/*` (`linkWorkspacePackages` in `web/pnpm-workspace.yaml`
links a workspace member when the requested range is satisfied locally), so the
app builds and tests against the sibling source without a rebuild of the
packages. Copied out of the repository, the same `package.json` installs those
packages from the registry, with no `workspace:` protocol to rewrite.

Three kinds of repo-internal wiring make the in-repo build resolve to the
siblings' **source** (a published `dist/` is never committed and is not built
when this app's typecheck runs), and **all of it is repository-internal
development wiring that a copied-out project drops**:

- `vite.config.ts`, `vitest.config.ts` and `vitest.a11y.config.ts` import the
  workspace's single package map (`web/scripts/speed-aliases.mjs`) and alias
  every `@speed/*` specifier onto its sibling's `src/`.
- `tsconfig.json` carries the map's `paths` projection minus the workspace's
  private test-support entries (tsconfig cannot import; the app carries its own
  fixtures under `src/test-utils/` instead), pinned against that projection by
  `src/alias-manifest.test.ts`.
- `eslint.config.mjs` imports `web/eslint.config.mjs` and scopes the two
  workspace discipline rules (`speed/no-literal-text`, `speed/no-direct-http`)
  to this app's runtime `src/`.

## What is here

| Path | What it is |
|---|---|
| `index.html` | The entry document; mounts `src/main.tsx`'s bootstrap into `#root`. |
| `src/main.tsx` | `bootstrapDemoApp`: reads the runtime API origin, then hands the app's declarative definition to `@speed/product-shell/bootstrap`'s `bootstrapSpeedApp`. |
| `src/app.tsx` | `DemoApp`: the route provider around the shell switch -- the tenant shell or the console area, chosen by the URL. |
| `src/routes.tsx` | The route table: the business slot plus the mounted console area. |
| `src/paths.ts` | The app's own route paths, shared by the route table and the chrome that links into them. |
| `src/chrome.tsx` | The shared top-bar chrome: the brand, the app-wide header actions (language switch, color-mode toggle), the tenant switcher and the avatar account menu. |
| `src/console/console-area.tsx` | The platform console, the second shell, over `@speed/console-ui`. |
| `src/pages/` | The app's pages: the starting page (the `/` card grid over the assembled capabilities), the account page, and the setup and verification landing pages a registration completes on. |
| `src/runtime-config.ts` | Reads the deployment's runtime configuration from `runtime-config.json` at the deployment root (resolved against the entry document, not the current URL): the API origin, the shell layout and the default theme preset, each falling back on its own. |
| `src/document-metadata.ts` | Keeps the document title in step with the active language. `<html lang>` is the assembly's (the app-entry bootstrap binds it and follows every switch). |
| `src/resources.ts`, `src/locales/` | The app's bilingual i18n bundle ("demo" namespace). |
| `orval.config.ts`, `src/app-api/` | The app's own generation level: the second orval configuration and the surface it generates, plus the seam that surface calls (see "The API contract"). |
| `AGENTS.md` | This directory's own contract for an agent working here. |

### The deployment configuration at runtime

One build output is shared across deployment topologies and white-label
styles, so those facts are not baked into the bundle: the app reads
`runtime-config.json` from beside the entry document (at the deployment
root, so the read never needs an origin to resolve one and a deep link
cannot shift it) before it renders. The committed
`public/runtime-config.json` is `{}` -- the defaults -- and the build
copies it into the served output.

| Field | Value | When absent or invalid |
|---|---|---|
| `apiOrigin` | the API origin, e.g. `https://api.example.com` | the page's own origin (the same-origin deployment shape) |
| `shellLayout` | `topbar` or `sidebar` | `topbar` |
| `themePreset` | one of the shipped preset ids: `default`, `classic`, `modern`, `compact`, `glass`, `ascii` | `default` |

```json
{
  "apiOrigin": "https://api.example.com",
  "shellLayout": "sidebar",
  "themePreset": "glass"
}
```

Every field is read on its own: a malformed value for one leaves the
others intact, so a typo in `apiOrigin` cannot silently drop the chosen
layout or preset. An unknown value is ignored. To change the shell form
of a deployment, set `shellLayout` to the other value -- nothing else in
the app changes; the value rides down to both frames (`ProductShell` and
`ConsoleShell`), so the tenant area and the console render the same
arrangement. The app's dev server and a reverse-proxied deployment use
the same-origin shape above when `apiOrigin` is absent.

`themePreset` is only the default a device starts on. The user's pick in
the account settings page (see "The account page and the app chrome")
is stored on the device and wins over it on the next boot; with no
stored choice the deployment value applies, and with neither the app
starts on `default`. The config value is checked against the shipped
shelf, so an unknown id falls back instead of reaching the provider.

### One origin for the API

The app reaches every backend call at a single origin -- the client base URL,
same-origin by default or the `runtime-config.json` origin -- but the demo's Go
program mounts the API, account and admin surfaces on separate ports
(`http.endpoints` in `server/defaults.yaml`). The one origin the app talks to is
therefore a **gateway** that routes each claimed namespace to the endpoint it
belongs to (`backend-prefixes.json` lists them):

- In development the vite dev server is that gateway: `pnpm dev` proxies every
  claimed prefix onto the endpoints (`DEMO_WEB_BACKEND_PROXY` above).
- The demo's Go program ships no such gateway. Its `web` endpoint serves the
  built bundle alone, so loading the bundle straight from the Go program
  (`http://127.0.0.1:8084/`) renders for static browsing but cannot sign in:
  `POST /api/v1/auth/flows` goes back to the page's own origin, which mounts no authn
  route, and no single `apiOrigin` reaches the demo's three API ports at once.

A deployment that serves the built bundle must place it behind the same gateway
that fronts the API -- one origin whose reverse proxy answers the bundle and the
API endpoints -- and set `runtime-config.json`'s `apiOrigin` to that origin.

### The tenant on the wire

The backend's `api` endpoint is tenant-scoped: `tenant.resolve` reads the tenant
from the request's `X-Tenant` header (the header extractor the platform defaults
select, whose header name is the module's own default) and looks the
name up in the roster's **alias** column, so an `api` call that announces no
tenant -- or a name the roster no longer holds -- is refused 404. The tenant is a
request-time routing fact, not a claim of the access token (the token carries
none), so the client announces it as a header. The host keeps the tenant's two
names apart: the switcher keys on the tenant id (`src/chrome.tsx`'s
`getCurrentTenantId`; a switch goes through the same file's
`useTenantSelection`, which writes it, and `DemoApp` clears it when a session
ends), while
`bootstrapDemoApp` wires the client's `tenantProvider` to the current tenant's
**alias** (`getCurrentTenantAlias`), which the client reads on every attempt and
sends as `X-Tenant`. A rename moves the alias and leaves the id, so the app reads the caller's own
tenant list (`GET /api/v1/auth/me/tenants`, the `tenant.self` module) once per
identity: authenticated, no permission needed, it answers each active
membership's identifier and current alias. That list fills the switcher and is
the alias source every tenant-scoped request announces. A platform staff
member's roster read (`tenantAdminTenantsList`, `tenant.admin`'s management
operation, which needs `tenant.read` in the **platform** domain) adds the
aliases of tenants the caller list does not hold -- a staff member entering a
tenant they are not a member of -- and names the staff default. An ordinary
member is answered 403 there and settles from its own list alone. When neither
source names the current tenant, the client announces nothing and the
tenant-scoped reads stay disabled: the skeleton holds no placeholder roster,
and a name no source holds would be refused 404.

A product whose `api` endpoint resolves the tenant
differently (a subdomain label, a path segment) repoints the server
configuration and leaves this wiring alone -- the transport still announces the
same value, and the copy decides where the server reads it.

### Sessions and the console

The session is delivered as the server's httpOnly cookie: the login flow is
started with `delivery: 'session'`, the server sets its cookie on the same
response, and the client sends it with `credentials: 'same-origin'`. So **a
page reload keeps the viewer signed in** -- the assembly re-reads
`GET /api/v1/auth/session` at boot and re-establishes the session from that cookie.
Both shells branch on the same session snapshot: a session that genuinely ends
(a sign-out, or a cookie that no longer holds) turns the page to sign-in rather
than a view that never settles. Nothing persists a credential to storage; the
cookie is the browser's, not a script's.

The tenant frame's `headerActions` slot carries a console entry pointing at
`/console`, which
redirects to the console's first page. It sits with the language switch,
the color-mode toggle and the notification bell -- in the top bar in the
default shell form, in the drawer paper's bottom row in the `sidebar`
form -- not in the navigation, and appears only to a principal that holds a platform
permission, so an ordinary member is offered no way in. The console area itself
gates each page on its permission code (composed host-side) and shows a "no
access" state for a code the session is known to lack, so a member who reaches
`/console` directly is turned away there.

The entry's visibility turns on the caller's permission sets, which the host
attaches (`src/platform-permissions.tsx`, wired into the assembly in
`src/main.tsx`). It reads `GET /api/v1/authz/me/permissions`, the caller-scoped
read that answers the permissions the caller holds in the platform
domain and in the tenant the request resolved to; both lists are attached to the
session through `setPermissionSet`, and the entry turns on the platform list
being non-empty. The caller is the session's actor, so during an impersonation
the read still answers the operator's own sets -- the operator's platform list
and the operator's grants in the impersonation record's tenant -- never the
impersonated account's. The read is repeated whenever the effective identity or the
current tenant changes, so a tenant that settles after the first read, or a
switch to another tenant, replaces the tenant list with the set read under the
current one. The read needs no platform-staff permission: an ordinary member is
answered an empty platform list rather than a 403, so the entry stays hidden,
and the tenant list carries the permissions that member actually holds in the
tenant the request resolved to (empty when the request resolved no tenant or
the member holds nothing there). A read that fails or is refused attaches the
empty set to both domains (fail-closed), and a feature document that is not
ready leaves both sets unknown
so the console shows its wait or failure instead of misreading the member.

### The account page and the app chrome

The tenant area ships one account page (`/account`, in the navigation) that
mounts `@speed/account-ui`'s sections -- the appearance theme choice, the
self-service writes (display
name, password, sign-in identifiers and the registration contact's
verification), then preferences, active sessions, social bindings and the
step-up-gated two-step-verification setup -- under a `@speed/ui-kit`
`PageHeader`. The appearance section is
`account-ui`'s controlled `AppearanceSection`, placed ahead of the
preferences section: it lists the shipped preset shelf with this bundle's
names (`themePreset.*` in `src/locales/`), reports a pick, and the page
applies it through `useAppThemePreset` and writes it to the device
(`demo-web.theme-preset`, `src/chrome.tsx`'s `persistThemePreset`), so the
theme changes immediately and a reload starts on it. The boot order is the
stored choice, then the deployment's `themePreset`, then `default`. The
phone write is not mounted: its two routes
are registered only when the deployment delivers an SMS code provider, which
this skeleton's baseline does not. The social-bindings section takes the session and
the channels the host offers; the skeleton configures none, so it lists
existing bindings with no add area, and the binding-callback route is mounted
beside the page for a product that configures a provider.

The shell chrome carries the tenant switcher (over the caller tenant list from
the `tenant.self` read), an app-wide language switch and a color-mode toggle, and
an avatar account menu (profile / account settings / sign out), all from
`src/chrome.tsx`; AppShell places them in the top bar or, in the `sidebar`
form, in the drawer paper's top and bottom rows. The language and
color-mode controls appear in the console frame too; the tenant switcher
does not (the console resolves no tenant).
The appearance preset selector does not live in the shell chrome at all --
it is the account page's appearance section, while the chrome keeps the
color-mode toggle; the two choices are orthogonal and both persist.
The color-mode choice is written to the device and read back at boot (with the
operating system's `prefers-color-scheme` as the fallback), so a reload starts
in the mode the user left. A tenant switch evicts the departing tenant's
cached rows (`src/chrome.tsx`'s `useTenantSelection` calls auth-core's
`evictTenantQueries` with the id read before the switch), so the next
tenant's reads never see the previous one's data.

### The starting page and the first account

The tenant area's landing (`/`) is the skeleton's front door, not a business
surface: `src/pages/home-page.tsx` renders the platform's assembled
capabilities as a card grid -- the account settings, the platform console and
the notification centre -- each a real route the app already serves. A
product replaces the cards with its own surfaces.

The sign-in surface (`src/pages/sign-in-view.tsx`) frames auth-ui's
`SignInScreen` with the app's brand and heading; the methods it offers are the
deployment's discovery document's. A redirect method's authorization URL is
navigated in the current window, and its round trip returns to the app's
`/sign-in/callback` route. The surface carries no deployment-operation copy:
which methods are enabled, and whether registration is offered, are the
deployment's facts, not text the page asserts.

A password registration opens the account without proving its email address,
then signs in with the same credentials; the host lands that sign-in on
`/account/verify` (`src/pages/account-verify-page.tsx`), which renders
account-ui's `ContactVerificationSection`. While `GET /api/v1/auth/me`
reports `verification.state` as `pending`, the tenant frame shows a persistent
notice below the app bar (`src/contact-verification-banner.tsx`) naming the
target address and linking to the verify page; its "verify" action goes to
`/account/verify` and its "change address" action to
`/account/verify?change=email`, where the page starts the section in the
change form. The notice is a `region`, has no close control, and stays until
the address is proved. It is separate from the
once-only "finish account setup" notice: the predicates are
`contactVerificationPending` and `setupIncomplete` in `src/account-setup.ts`,
and a password registration is setup-complete while verification is still
pending. The frame calls the personal-tenant ensure regardless of the
verification state; the notice is independent of it.

## What to change when you copy this out

- Rename the package (`package.json`'s `name`) and the app's title
  (`src/locales/*`).
- Inline `tsconfig.json`'s `extends: "../../../web/tsconfig.base.json"` into the
  file: the base compiler options it supplies (strictness, module resolution,
  `noEmit`) are ordinary settings, but the file it points at lives in the
  repository and is not part of the skeleton.
- Remove the in-repo alias configuration: the `paths` section in
  `tsconfig.json`, the `speedAliases()` imports and `resolve.alias` entries in
  `vite.config.ts`, `vitest.config.ts` and `vitest.a11y.config.ts`, the
  `fs.allow` workspace root in `vite.config.ts`, and `src/alias-manifest.test.ts`
  (the drift test for that section). Outside this repository the packages
  resolve from `node_modules` and there is no `web/scripts` to import.
  `eslint.config.mjs` likewise imports the repository's `web/eslint.config.mjs`;
  replace that with a config of your own. `vitest.a11y.config.ts` keeps
  working without the aliases -- it is the same config as
  `vitest.config.ts` except for the file set -- so `test:a11y` still runs
  after the edit.
- Serve the app under a sub-path instead of an origin root: set vite's `base`
  in `vite.config.ts` (the bundle's asset URLs follow it), make
  `runtimeConfigUrl()` in `src/runtime-config.ts` resolve the config file
  under that prefix instead of the origin root -- it is deliberately
  root-absolute so a deep link cannot shift the read, which also means it
  ignores a deployment prefix -- and pass the same prefix as `basename` to
  `RouteProvider` in `src/app.tsx`, so route-kit's hrefs and navigation carry
  it. The three must agree; a mismatch resolves assets, the runtime config and
  routes under different roots.
- The social-binding round trip (`SocialBindingsSection` in
  `src/pages/account-page.tsx`) sends `returnTo` as the link flow's top-level
  `return_to`, and the backend lands the browser there once the provider
  answers. The app passes its root-absolute `ACCOUNT_CALLBACK_PATH`, which is
  right when the page and the API share an origin. A deployment that separates
  them (the API on another origin, the page on a static host) must pass an
  absolute URL on an origin the deployment trusts instead, or the browser is
  sent to the API host rather than back to the page.
- Dependency contract: every `@speed/*` package a non-test file under `src/`
  imports must be declared in `dependencies` at a registry range
  (`^0.0.1`-style). Packages used only by suites belong in `devDependencies`.
  Never use the `workspace:` protocol in any dependency block -- a copied-out
  skeleton has no workspace to link from. Bump the ranges to the versions you
  install. Keep these four properties when you edit the manifest or the
  sources: `src/copy-out-guards.test.ts` asserts them (no `workspace:`
  specifier, every runtime `@speed/*` import declared, no suite importing
  repository source through a relative path, and no source file importing the
  workspace's private `@speed/test-utils` package).
- `src/test-utils/` carries the host's own test fixtures (the scripted session
  harness, the emitted-CSS probe and the matchMedia stub). They are part of
  the skeleton and import only `@speed` packages and the standard test
  toolchain, so they travel with the copy.
- Replace the business slot: put your pages under `src/pages/` and name them in
  `src/routes.tsx`. Replace the starting page's cards
  (`src/pages/home-page.tsx`) with your own entry points, and replace the
  sign-in note (`src/pages/sign-in-view.tsx`) with the registration the product
  offers.
- Retarget `api:gen` and `orval.config.ts` at your own setup: the generation
  scripts travel with the copy in `scripts/`, and the `gen` step writes
  `contracts/api.json` at the repository root (the document path
  `orval.config.ts` reads). The platform package's `features.ts` and generated
  surface are the package library's outputs; a copy-out project installs the
  published package instead of regenerating them.
- Keep the console area only if the product has a platform-staff face; drop it
  and its `@speed/console-ui` dependency otherwise.

## Checks

```
pnpm lint        # eslint
pnpm typecheck   # tsc --noEmit
pnpm test        # vitest
pnpm test:a11y   # the route-level axe sweep (its own config and file set)
pnpm build       # tsc + vite build into ../server/dist
```
