/**
 * The demo host's vite configuration: its dev server and its production
 * bundler. A browser application carries its own bundler -- the web/packages
 * libraries stay bundler-free by discipline (each app builds them from source
 * through the workspace alias map, or from their published dist/ outside the
 * repository) -- so this is where the skeleton's build tooling lives.
 *
 * Resolution: every @speed/* specifier, subpaths included, maps onto the
 * workspace sibling's live src through the workspace's single package map
 * (web/scripts/speed-aliases.mjs). A sibling's published exports resolve
 * against its dist/, which is never committed and may not be built when the dev
 * server or the build runs, so every leg stays on source exactly as the test
 * leg does. The map is the one place a specifier is added or moved.
 *
 * Build: vite writes a browser bundle -- one entry document plus a
 * content-addressed assets/ subtree -- into server/dist, the directory the Go
 * program beside this package embeds with go:embed
 * (starters/demo/server/main.go) and serves through pkg/webui. The build order
 * is therefore frontend first, Go second.
 *
 * Dev server: the demo's five traffic endpoints listen on separate ports
 * (starters/demo/server/testdata/*.yaml). The app talks to its own origin by
 * default (the client base URL falls back to window.location.origin), so this
 * server proxies the paths the app uses onto the demo's endpoints --
 * overridable through the DEMO_WEB_*_PROXY variables when a run moves them.
 */
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import { speedAliases } from '../../../web/scripts/speed-aliases.mjs'

const sibling = (path: string): string =>
  fileURLToPath(new URL(path, import.meta.url))

const appRoot = sibling('.')
const workspaceRoot = sibling('../../../web')

const apiProxy = process.env.DEMO_WEB_API_PROXY ?? 'http://127.0.0.1:8080'
const accountProxy =
  process.env.DEMO_WEB_ACCOUNT_PROXY ?? 'http://127.0.0.1:8081'
const adminProxy = process.env.DEMO_WEB_ADMIN_PROXY ?? 'http://127.0.0.1:8082'

export default defineConfig({
  resolve: {
    alias: speedAliases(),
  },
  build: {
    // The bundle lands in the Go module beside this package, because go:embed
    // reaches only files in the embedding package's own directory tree: a
    // pattern may not climb out with `..`.
    outDir: '../server/dist',
    // That directory is not emptied before the build. It is the directory the
    // Go program embeds, and it carries a committed stand-in file
    // (placeholder.txt) so that go:embed has something to embed: go:embed
    // refuses a directory with no files. vite empties the outDir by default,
    // which would delete that stand-in and leave the checkout modified after
    // every build -- a check that dirties the tree. Keeping the stand-in means
    // the only cost is that a content-addressed bundle from an earlier build
    // can linger under assets/; everything there is git-ignored, so it is
    // dead weight in a long-lived local checkout and nothing in the tree.
    emptyOutDir: false,
    // Without grouping the whole app lands in one 774 kB entry chunk
    // (247 kB gzip) and vite prints its "chunks larger than 500 kB"
    // warning: the app eagerly composes the entire platform shell, so
    // React, MUI, the query client and every @speed package ship in the
    // one file. Grouping the vendored libraries splits that entry so no
    // chunk crosses the threshold, and gives the framework chunks a hash
    // stable across app-only edits -- a consumer's own changes no longer
    // re-download React or MUI. The app's own code stays in the entry
    // chunk. If the entry grows past the threshold again, that is a
    // route-level problem (lazy-load the app's own pages), not something
    // a wider vendor split fixes.
    rolldownOptions: {
      output: {
        codeSplitting: {
          // First match wins, so the narrower library groups precede the
          // catch-all: the pnpm store path nests each package under its
          // own `node_modules/`, so a bare specifier directory is what
          // these match at any depth.
          groups: [
            {
              name: 'react',
              test: /node_modules[\\/](react|react-dom|react-router|react-router-dom|scheduler)[\\/]/,
            },
            { name: 'mui', test: /node_modules[\\/]@mui[\\/]/ },
            { name: 'vendor', test: /node_modules[\\/]/ },
          ],
        },
      },
    },
    // The build also prints one MODULE_LEVEL_DIRECTIVE warning per
    // `'use client'` directive carried by a bundled dependency's ESM build
    // (@mui/*, @tanstack/react-query, react-router -- 179 files today).
    // The directive is React Server Components metadata from those
    // packages, not this app's, and it is meaningless in a client-only
    // bundle, so nothing here can or should remove it. It is left visible
    // rather than silenced -- no onwarn filter, no log level change, no
    // dependency swap -- and it goes away when the upstream packages stop
    // shipping the directive.
  },
  server: {
    // The dev server serves this directory and the workspace siblings the
    // aliases reach into (their own dependencies resolve out of the
    // workspace's node_modules store, also under the workspace root).
    fs: {
      allow: [appRoot, workspaceRoot],
    },
    // The proxy keys are matched in order, so the more specific path sits
    // before its prefix: /auth/admin answers on the admin endpoint while the
    // rest of /auth mounts on the account endpoint.
    proxy: {
      '/api': { target: apiProxy },
      '/auth/admin': { target: adminProxy },
      '/auth': { target: accountProxy },
      '/tenant': { target: adminProxy },
      '/settings': { target: adminProxy },
      '/audit': { target: adminProxy },
    },
  },
})
