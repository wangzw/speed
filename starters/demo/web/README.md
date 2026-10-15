# starters/demo/web

The frontend application of the demo skeleton: the browser application a
copied-out product grows from. It composes the platform's `@speed/*` packages
into one program -- the session, the tenant-facing shell, the platform console
and the routing between them -- and builds into `dist/`, which the Go program
embeds with `go:embed` and serves through `pkg/webui`.

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

Run the backend first (`go -C starters/demo run . --config
file:testdata/sqlite-memory.yaml sqlite-memory serve`), then `pnpm dev`.

## Build

```
pnpm build                          # tsc typecheck, then vite build into dist/
```

The output is one entry document (`dist/index.html`) plus a content-addressed
`dist/assets/` subtree. The Go program embeds `web/dist`, so **the frontend is
built first and the Go binary second** -- that order is a hard constraint on
the combined deployment, and it does not change when the bundle changes.

`dist/` is a build output and is git-ignored, but `go:embed` refuses to compile
a directory with no files in it, so a checkout carries one committed stand-in
there, `dist/placeholder.txt`. The build neither writes nor clears it (`vite`
would empty the outDir by default; `vite.config.ts` turns that off), and
everything the build does write under `dist/` is ignored, so building leaves
the working tree clean. Until the frontend is built the directory holds no
entry document, and `pkg/webui` answers 404 to every request rather than
failing to start.

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
| `dist/` | The build output the Go program embeds (see above). |
| `AGENTS.md` | This directory's own contract for an agent working here. |

### The API origin at runtime

One `dist/` is shared across deployment topologies. The API origin is therefore
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
- Keep the console area only if the product has a platform-staff face; drop it
  and its `@speed/console-ui` dependency otherwise.

## Checks

```
pnpm lint        # eslint
pnpm typecheck   # tsc --noEmit
pnpm test        # vitest
pnpm build       # tsc + vite build into dist/
```
