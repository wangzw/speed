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

const outDir = process.env.DEMO_WEB_OUT_DIR?.trim() || '../server/dist'

export default defineConfig({
  resolve: {
    alias: speedAliases(),
  },
  build: {
    outDir,
    emptyOutDir: false,
    rolldownOptions: {
      // rolldown drops top-level 'use client' directives in a browser build; disable this check so only that warning is silenced.
      checks: { moduleLevelDirective: false },
      output: {
        codeSplitting: {
          // Split the workspace packages by role rather than into one chunk: the generated SDK and the session layer change on a different rhythm from the UI packages.
          groups: [
            {
              name: 'react',
              test: /node_modules[\\/](react|react-dom|react-router|react-router-dom|scheduler)[\\/]/,
            },
            { name: 'mui', test: /node_modules[\\/]@mui[\\/]/ },
            {
              name: 'speed-runtime',
              test: /[\\/]web[\\/]packages[\\/](api-client|api-sdk|auth-core|i18n|route-kit|tokens)[\\/]src[\\/]/,
            },
            {
              name: 'speed-admin-locales',
              test: /[\\/]web[\\/]packages[\\/]admin-ui[\\/]src[\\/]locales[\\/]/,
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
    host: devServer.host,
    port: devServer.port,
    strictPort: devServer.strictPort,
    fs: {
      allow: [appRoot, workspaceRoot],
    },
    proxy: backendProxyRules(devServer.proxyTarget),
  },
})
