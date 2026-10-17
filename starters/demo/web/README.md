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
backend endpoints claim onto the demo's backend listener: the api, account and
admin endpoints all answer on one port. The claimed prefixes live in
`backend-prefixes.json` beside this package (`/api`; `/auth` and `/features`;
`/audit`, `/authz`, `/settings`, `/sms` and `/tenant`), and `vite.config.ts`
generates one segment-anchored proxy key per prefix from that list. The target
defaults to the `sqlite-memory` stack's backend listener (`127.0.0.1:8080`);
override it with `DEMO_WEB_BACKEND_PROXY` when a run moves it.

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

The typed API surface comes from two generation levels, and `api:gen` runs the
app's own:

```
pnpm api:gen                        # or: pnpm --filter demo-web api:gen, from web/
```

It runs three legs in order:

1. The Go host's `gen` command merges the OpenAPI fragments the binary's imports
   reach and writes the merged document to `contracts/api.json` at the
   repository root (`starters/demo/server/main.go`'s `genDocument`).
2. `pnpm dlx orval@8.17.0 --config orval.config.ts` generates this app's own
   surface from that document into `src/app-api/index.ts`.
3. `node ../../../web/scripts/orval-nodenext-fixup.mjs src/app-api/index.ts
   src/app-api/runtime.ts` rewrites the generated mutator import to its explicit
   `.js` form.

orval is never a dependency of this workspace: `pnpm dlx` fetches the pinned
version on demand and it stays out of `web/pnpm-lock.yaml`.

| Level | Configuration | Input | Output |
|---|---|---|---|
| platform | `web/orval.config.ts` | `contracts/api.json` | the published `@speed/api-sdk`, in `web/packages/api-sdk/src/index.ts` |
| app | `orval.config.ts` (this directory) | `contracts/api.json` | `src/app-api/index.ts` |

The platform level belongs to the package library and produces the package the
shell packages import; the app level belongs to the app and produces a surface
inside it. Both read `contracts/api.json` -- the document the host's `gen`
command writes, carrying every fragment the binary's imports reach, so the
platform fragments plus the app's own, should it declare any. This starter
declares none, so the two levels cover the same operations. Both levels reach the backend
through the same seam: `src/app-api/runtime.ts` re-exports the mutators
`@speed/api-sdk/runtime` exports, so the one `bindRequestFn` call at bootstrap
serves both generated surfaces and no generated or bridge file performs HTTP
itself.

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

Three repo-internal pieces make the in-repo build resolve to the siblings'
**source** (a published `dist/` is never committed and is not built when this
app's typecheck runs), and **all three are repository-internal development
wiring that a copied-out project drops**:

- `vite.config.ts` and `vitest.config.ts` import the workspace's single package
  map (`web/scripts/speed-aliases.mjs`) and alias every `@speed/*` specifier onto
  its sibling's `src/`.
- `tsconfig.json` carries the same map as a `paths` section (tsconfig cannot
  import), pinned against the map by `src/alias-manifest.test.ts`.
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
| `src/pages/` | The app's pages: the starting page (the `/` card grid over the assembled capabilities) and the account page. |
| `src/runtime-config.ts` | Reads the deployment's API origin from `runtime-config.json` at the deployment root (resolved against the entry document, not the current URL), falling back to same-origin. |
| `src/document-metadata.ts` | Keeps `<html lang>` and the document title in step with the active language. |
| `src/resources.ts`, `src/locales/` | The app's bilingual i18n bundle ("demo" namespace). |
| `orval.config.ts`, `src/app-api/` | The app's own generation level: the second orval configuration and the surface it generates, plus the seam that surface calls (see "The API contract"). |
| `AGENTS.md` | This directory's own contract for an agent working here. |

### The API origin at runtime

One build output is shared across deployment topologies. The API origin is therefore
not baked into the bundle: the app reads `runtime-config.json` from beside the
entry document (by its relative path, so the read never needs an origin to
resolve one) and passes its `apiOrigin` to the client base URL. When the file is
absent, unreadable, or carries no origin, the app falls back to the page's own
origin -- the same-origin deployment shape, which is what the demo's dev server
and a reverse-proxied deployment use. A cross-origin deployment places the file
beside the bundle:

```json
{ "apiOrigin": "https://api.example.com" }
```

### One origin for the API

The app reaches every backend call at a single origin -- the client base URL,
same-origin by default or the `runtime-config.json` origin -- but the demo's Go
program mounts the API, account and admin surfaces on separate ports
(`http.endpoints` in `testdata/sqlite.yaml`). The one origin the app talks to is
therefore a **gateway** that routes each claimed namespace to the endpoint it
belongs to (`backend-prefixes.json` lists them):

- In development the vite dev server is that gateway: `pnpm dev` proxies every
  claimed prefix onto the endpoints (`DEMO_WEB_BACKEND_PROXY` above).
- The demo's Go program ships no such gateway. Its `web` endpoint serves the
  built bundle alone, so loading the bundle straight from the Go program
  (`http://127.0.0.1:8084/`) renders for static browsing but cannot sign in:
  `POST /auth/flows` goes back to the page's own origin, which mounts no authn
  route, and no single `apiOrigin` reaches the demo's three API ports at once.

A deployment that serves the built bundle must place it behind the same gateway
that fronts the API -- one origin whose reverse proxy answers the bundle and the
API endpoints -- and set `runtime-config.json`'s `apiOrigin` to that origin.

### The tenant on the wire

The backend's `api` endpoint is tenant-scoped: `tenant.resolve` reads the tenant
from the request's `X-Tenant` header (`server/testdata/*.yaml`) and looks the
name up in the roster's **alias** column, so an `api` call that announces no
tenant -- or a name the roster no longer holds -- is refused 404. The tenant is a
request-time routing fact, not a claim of the access token (the token carries
none), so the client announces it as a header. The host keeps the tenant's two
names apart: the switcher keys on the tenant id (`src/chrome.tsx`'s
`getCurrentTenantId`; a switch in `src/app.tsx` writes it), while
`bootstrapDemoApp` wires the client's `tenantProvider` to the current tenant's
**alias** (`getCurrentTenantAlias`), which the client reads on every attempt and
sends as `X-Tenant`. A rename moves the alias and leaves the id, so once a viewer
is signed in the app reads the roster (`tenantAdminTenantsList`, its own
generated operation) and announces each tenant's current alias rather than a name
a placeholder fixed.

That read is `tenant.admin`'s management operation and needs `tenant.read` in the
**platform** domain (`pkg/tenant/admin` holds the route to it), which the demo's
operator has; a viewer without it -- an ordinary member, who holds only
tenant-domain roles -- is answered 403, and the app keeps the placeholder aliases
(`DEMO_TENANTS`). That fallback answers only while the roster's alias still equals
the placeholder's value: the roster read is the app's only route to a renamed
alias and no member-facing endpoint carries one, so once a tenant has been renamed
a viewer without the permission announces a name the roster no longer holds and
every tenant-scoped request is refused 404. The demo's own seed signs in the
platform administrator alone, which holds every permission, so the seeded
deployment always answers; a copy that signs in ordinary members must give them
their own alias source -- the tenants their memberships name, each with its
current alias, handed to `setTenantAliases` -- or keep each tenant's alias equal
to the placeholder's.

A product whose `api` endpoint resolves the tenant
differently (a subdomain label, a path segment) repoints the server
configuration and leaves this wiring alone -- the transport still announces the
same value, and the copy decides where the server reads it.

### Sessions and the console

The session is delivered as the server's httpOnly cookie: the login flow is
started with `delivery: 'session'`, the server sets its cookie on the same
response, and the client sends it with `credentials: 'same-origin'`. So **a
page reload keeps the viewer signed in** -- the assembly re-reads
`GET /auth/session` at boot and re-establishes the session from that cookie.
Both shells branch on the same session snapshot: a session that genuinely ends
(a sign-out, or a cookie that no longer holds) turns the page to sign-in rather
than a view that never settles. Nothing persists a credential to storage; the
cookie is the browser's, not a script's.

The tenant frame's header carries a console entry pointing at `/console`, which
redirects to the console's first page. It sits at the right end of the bar, with
the language switch, the color-mode toggle and the notification bell -- not in
the navigation -- and appears only to a principal that holds a platform
permission, so an ordinary member is offered no way in. The console area itself
gates each page on its permission code (composed host-side) and shows a "no
access" state for a code the session is known to lack, so a member who reaches
`/console` directly is turned away there.

The entry's visibility turns on the caller's platform permission set, which the
host attaches (`src/platform-permissions.tsx`, wired into the assembly in
`src/main.tsx`). The platform carries no read of the caller's *held* permissions,
so the app reads the declared-permission catalog (`GET /authz/admin/permissions`)
-- a platform-staff operation that answers only to a principal holding
`authz.role.read` in the platform domain, and whose answer is every permission
the process declares. The seed's platform administrator holds exactly that set,
so the app attaches the catalog's codes as the caller's platform permissions; an
ordinary member is answered 403 and the app attaches the empty set. A copy whose
principals hold a subset of the declared permissions must replace this with its
own source of the caller's held set.

### The account page and the app chrome

The tenant area ships one account page (`/account`, in the navigation) that
mounts `@speed/account-ui`'s sections -- the self-service writes (display
name, password and sign-in identifiers), then preferences, active sessions,
social bindings and the step-up-gated two-step-verification setup -- under a
`@speed/ui-kit` `PageHeader`. The phone write is not mounted: its two routes
are registered only when the deployment delivers an SMS code provider, which
this skeleton's baseline does not. The social-bindings section takes the session and
the channels the host offers; the skeleton configures none, so it lists
existing bindings with no add area, and the binding-callback route is mounted
beside the page for a product that configures a provider.

The top bar carries the tenant switcher (over the app's placeholder tenant
list), an app-wide language switch and a color-mode toggle, and an avatar
account menu (profile / account settings / sign out), all from
`src/chrome.tsx`. The language and color-mode controls appear in the console
banner too; the tenant switcher does not (the console resolves no tenant).

### The starting page and the first account

The tenant area's landing (`/`) is the skeleton's front door, not a business
surface: `src/pages/home-page.tsx` renders the platform's assembled
capabilities as a card grid -- the account settings, the platform console, the
object store and the notification centre -- each a real route the app already
serves. A product replaces the cards with its own surfaces.

Because the skeleton folds registration into the sign-in flow and ships
neither SMS nor social sign-in, a fresh deployment is born with no account and
no way to make one from the surface. The sign-in surface
(`src/pages/sign-in-view.tsx`) therefore carries a note under the form saying
where the first account comes from -- the server's `seed` command
(`starters/demo/AGENTS.md`). A product that grows its own registration
replaces the note with it.

## What to change when you copy this out

- Rename the package (`package.json`'s `name`) and the app's title
  (`src/locales/*`).
- Bump the `@speed/*` ranges to the versions you install, and remove the three
  repository-internal wiring pieces above (the alias imports, the tsconfig
  `paths` section with its drift test, and the eslint config's import of
  `web/eslint.config.mjs`) -- outside this repository the packages resolve from
  `node_modules` and there is no `web/scripts` to import.
- Replace the business slot: put your pages under `src/pages/` and name them in
  `src/routes.tsx`. Replace the starting page's cards
  (`src/pages/home-page.tsx`) with your own entry points, and replace the
  sign-in note (`src/pages/sign-in-view.tsx`) with the registration the product
  offers.
- Retarget `api:gen` and `orval.config.ts` at your own setup: the script's
  fixup step points at the workspace's
  `web/scripts/orval-nodenext-fixup.mjs`, and its `gen` step writes
  `contracts/api.json` at the repository root (the document path
  `orval.config.ts` reads, and the input the platform level uses too).
- Keep the console area only if the product has a platform-staff face; drop it
  and its `@speed/console-ui` dependency otherwise.

## Checks

```
pnpm lint        # eslint
pnpm typecheck   # tsc --noEmit
pnpm test        # vitest
pnpm build       # tsc + vite build into ../server/dist
```
