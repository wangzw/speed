# starters/demo/web

The demo skeleton's frontend application. It composes the platform's `@speed/*`
packages into one browser program and builds into `dist/`, which the Go program
in the parent directory embeds with `go:embed` and serves through `pkg/webui`.
See `README.md` for the layout and the commands.

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
  are the place a product's own pages land; the skeleton ships one placeholder.
- **The build order is frontend first, Go second.** The Go program embeds
  `web/dist`, so `dist/` must exist and hold a file before the Go build runs --
  which is why a checkout commits the stand-in `dist/placeholder.txt` there.
  The build writes nothing to that name and does not clear the directory
  (`vite.config.ts` disables vite's default outDir emptying), so the stand-in
  survives every build.
- **`dist/` is a build output.** It is git-ignored; the only tracked file in it
  is the stand-in `dist/placeholder.txt` (committed with `git add -f`).
  Everything a build writes there is ignored, so building leaves the working
  tree clean. `pkg/webui` answers 404 to every request until the frontend is
  built, rather than failing to start.

## Rules

- **Every user-facing string renders from the "demo" i18n namespace**
  (`src/locales/{en-US,zh-CN}.json`, identical leaf key sets). A navigation
  label that must translate reactively is a component (`src/nav-label.tsx`),
  because a declaration's label is a React element.
- **Every backend call goes through `@speed/api-sdk`'s generated operations**
  over the request function the assembly binds into the runtime seam
  (`bootstrapSpeedApp`). The one hand-written request is `src/runtime-config.ts`,
  which reads a static deployment asset beside the entry document -- a static
  file, not an API endpoint -- and is the module `eslint.config.mjs` exempts
  from the no-direct-http rule.
- **The API origin is read at runtime, never baked in** (`src/runtime-config.ts`,
  falling back to same-origin). One `dist/` serves every deployment topology.
- **The two shells live in disjoint path spaces.** The tenant area renders
  `@speed/product-shell`; the console area renders `@speed/console-ui` under
  `CONSOLE_PREFIX`. `src/app.tsx` picks the shell from the route table's
  selected entry, so a console page carries a label.

## Checks

Run from this directory, or from `web/` with `pnpm --filter demo-web <script>`:

```
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```

The app is also a leg of the workspace's `make web-check` and an input to the
Go program's build (`go -C starters/demo build ./...`), which embeds `dist/`.
