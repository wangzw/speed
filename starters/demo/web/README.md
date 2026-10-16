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
`window.location.origin`), and the dev server proxies the paths the app uses
onto the demo's endpoints: `/api` to the api endpoint, `/auth` to the account
endpoint, and `/auth/admin`, `/tenant`, `/settings` and `/audit` to the admin
endpoint. The targets default to the `sqlite-memory` stack's ports
(`127.0.0.1:8080/8081/8082`); override them with `DEMO_WEB_API_PROXY`,
`DEMO_WEB_ACCOUNT_PROXY` and `DEMO_WEB_ADMIN_PROXY` when a run moves them.

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
| `src/console/console-area.tsx` | The platform console, the second shell, over `@speed/console-ui`. |
| `src/pages/` | The app's pages; one placeholder stands in for a business domain. |
| `src/runtime-config.ts` | Reads the deployment's API origin from `runtime-config.json` beside the entry document, falling back to same-origin. |
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

## What to change when you copy this out

- Rename the package (`package.json`'s `name`) and the app's title
  (`src/locales/*`).
- Bump the `@speed/*` ranges to the versions you install, and remove the three
  repository-internal wiring pieces above (the alias imports, the tsconfig
  `paths` section with its drift test, and the eslint config's import of
  `web/eslint.config.mjs`) -- outside this repository the packages resolve from
  `node_modules` and there is no `web/scripts` to import.
- Replace the business slot: put your pages under `src/pages/` and name them in
  `src/routes.tsx`.
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
