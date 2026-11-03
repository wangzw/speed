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
 * Dev server: the demo's backend listener carries every endpoint -- api,
 * account, admin, content, web and the operations probes -- on one port
 * (starters/demo/server/defaults.go). The app talks to its own origin by
 * default (the client base URL falls back to window.location.origin), so this
 * server proxies the namespaces those endpoints claim onto that port --
 * overridable through DEMO_WEB_BACKEND_PROXY when a run moves it. The dev
 * server itself listens on the IPv4 loopback (127.0.0.1, not `::1`) at 8080,
 * overridable through DEMO_WEB_PORT; the host stays pinned, and an override
 * that is not a port stops the load with an error rather than silently
 * keeping the default. The claimed namespaces come from
 * backend-prefixes.json (src/backend-prefixes.ts) -- a hand-kept mirror of the
 * claims in the server configuration, held to them by the server's
 * TestThePrefixManifestAgreesWithTheDifferentialTableAndTheClaims.
 */
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import { speedAliases } from '../../../web/scripts/speed-aliases.mjs'
import { backendProxyRules } from './src/backend-prefixes.js'
import { devServerPlan } from './src/dev-server.js'

const sibling = (path: string): string =>
  fileURLToPath(new URL(path, import.meta.url))

const appRoot = sibling('.')
const workspaceRoot = sibling('../../../web')

const devServer = devServerPlan(process.env)

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
    // one file. Grouping the vendored libraries and the workspace
    // packages splits that entry so no chunk crosses the threshold, and
    // gives the framework and platform chunks a hash stable across
    // app-only edits -- a consumer's own changes no longer re-download
    // React, MUI or the platform packages. The app's own code is what
    // stays in the entry chunk. If that grows past the threshold, it is a
    // route-level problem (lazy-load the app's own pages), not something
    // a wider vendor split fixes.
    rolldownOptions: {
      // The build also prints one MODULE_LEVEL_DIRECTIVE warning per
      // `'use client'` directive carried by a bundled dependency's ESM build
      // (@mui/*, @tanstack/react-query, react-router). The directive is React
      // Server Components metadata, and rolldown drops top-level directives
      // from a bundled browser build anyway; the check exists for library
      // authors whose output must keep them, which this app is not. It is
      // switched off through the documented check rather than an onwarn or
      // log-level filter, so a warning from any other directive still shows.
      checks: { moduleLevelDirective: false },
      output: {
        codeSplitting: {
          // First match wins, so the narrower groups precede the
          // catch-all: the pnpm store path nests each package under its
          // own `node_modules/`, so a bare specifier directory is what
          // these match at any depth.
          groups: [
            {
              name: 'react',
              test: /node_modules[\\/](react|react-dom|react-router|react-router-dom|scheduler)[\\/]/,
            },
            { name: 'mui', test: /node_modules[\\/]@mui[\\/]/ },
            // The workspace packages are aliased onto their live source
            // (web/packages/<name>/src), so they never match the
            // node_modules groups. They are split by role rather than
            // pooled into one chunk: the generated API and the session
            // layer change on a different rhythm from the UI packages,
            // and a single platform chunk would sit above the size
            // heuristic on its own. The demo's own pages stay in the
            // entry chunk.
            {
              name: 'speed-runtime',
              test: /[\\/]web[\\/]packages[\\/](api-client|api-sdk|auth-core|i18n|route-kit|tokens)[\\/]src[\\/]/,
            },
            {
              name: 'speed-admin',
              test: /[\\/]web[\\/]packages[\\/]admin-ui[\\/]src[\\/]/,
            },
            {
              name: 'speed-ui',
              test: /[\\/]web[\\/]packages[\\/][^\\/]+[\\/]src[\\/]/,
            },
            { name: 'vendor', test: /node_modules[\\/]/ },
          ],
        },
      },
    },
  },
  server: {
    // Bind the IPv4 loopback explicitly rather than leaving the host unset:
    // vite then listens on `localhost`, whose resolution can land on `::1`,
    // while the backend listener this server proxies to is IPv4. The host is
    // not an override; only the port moves.
    host: devServer.host,
    // The port a local run yields to another checkout's run through
    // DEMO_WEB_PORT. strictPort keeps two runs from fighting over 8080: a
    // taken port fails the start instead of quietly moving to another one the
    // backend proxy does not know about.
    port: devServer.port,
    strictPort: devServer.strictPort,
    // The dev server serves this directory and the workspace siblings the
    // aliases reach into (their own dependencies resolve out of the
    // workspace's node_modules store, also under the workspace root).
    fs: {
      allow: [appRoot, workspaceRoot],
    },
    // Every namespace the app's generated calls address answers on the same
    // backend listener, so every claimed prefix proxies to the same origin.
    // The keys are segment-anchored regular expressions (`^/auth(?:[/?]|$)`
    // rather than `/api/v1/auth`), because vite matches a key that begins with `^`
    // as a regular expression and anything else with `startsWith`: a plain
    // `/api/v1/auth` key would also forward `/api/v1/system`.
    proxy: backendProxyRules(devServer.proxyTarget),
  },
})
