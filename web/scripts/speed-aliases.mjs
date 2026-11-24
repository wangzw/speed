/**
 * The workspace's single @speed package map and its two derivations (vite/vitest
 * aliases and tsconfig paths). Order is load-bearing: a subpath entry must come
 * before the entry that is its prefix.
 */
import { dirname, join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

/** Absolute path of the workspace root (web/); node:path is used because jsdom's URL is not Node's. */
const WORKSPACE_ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')

/** The workspace's @speed specifiers and the src file each resolves to, in resolution order. */
export const SPEED_PACKAGE_MAP = Object.freeze([
  {
    specifier: '@speed/api-sdk/runtime',
    entry: 'packages/api-sdk/src/runtime.ts',
  },
  {
    specifier: '@speed/api-sdk/features',
    entry: 'packages/api-sdk/src/features.ts',
  },
  { specifier: '@speed/api-sdk', entry: 'packages/api-sdk/src/index.ts' },
  { specifier: '@speed/api-client', entry: 'packages/api-client/src/index.ts' },
  {
    specifier: '@speed/auth-core/otpauth',
    entry: 'packages/auth-core/src/otpauth.ts',
  },
  {
    specifier: '@speed/auth-core/webauthn',
    entry: 'packages/auth-core/src/webauthn.ts',
  },
  { specifier: '@speed/auth-core', entry: 'packages/auth-core/src/index.ts' },
  { specifier: '@speed/auth-ui', entry: 'packages/auth-ui/src/index.ts' },
  { specifier: '@speed/tenancy-ui', entry: 'packages/tenancy-ui/src/index.ts' },
  { specifier: '@speed/account-ui', entry: 'packages/account-ui/src/index.ts' },
  { specifier: '@speed/admin-ui', entry: 'packages/admin-ui/src/index.ts' },
  {
    specifier: '@speed/org-create-ui',
    entry: 'packages/org-create-ui/src/index.ts',
  },
  {
    specifier: '@speed/notification-ui',
    entry: 'packages/notification-ui/src/index.ts',
  },
  {
    specifier: '@speed/product-shell/bootstrap',
    entry: 'packages/product-shell/src/bootstrap.tsx',
  },
  {
    specifier: '@speed/product-shell',
    entry: 'packages/product-shell/src/index.ts',
  },
  { specifier: '@speed/layout-kit', entry: 'packages/layout-kit/src/index.ts' },
  { specifier: '@speed/route-kit', entry: 'packages/route-kit/src/index.ts' },
  {
    specifier: '@speed/i18n/mui-locale',
    entry: 'packages/i18n/src/mui-locale.ts',
  },
  {
    specifier: '@speed/i18n/platform-errors',
    entry: 'packages/i18n/src/platform-errors.ts',
  },
  { specifier: '@speed/i18n', entry: 'packages/i18n/src/index.ts' },
  { specifier: '@speed/tokens', entry: 'packages/tokens/src/index.ts' },
  { specifier: '@speed/ui-kit', entry: 'packages/ui-kit/src/index.ts' },
  { specifier: '@speed/test-utils/axe', entry: 'test-utils/src/axe.ts' },
  {
    specifier: '@speed/test-utils/emitted-css',
    entry: 'test-utils/src/emitted-css.ts',
  },
  {
    specifier: '@speed/test-utils/matchMedia',
    entry: 'test-utils/src/matchMedia.ts',
  },
  {
    specifier: '@speed/test-utils/real-client',
    entry: 'test-utils/src/real-client.ts',
  },
  {
    specifier: '@speed/test-utils/render',
    entry: 'test-utils/src/render.tsx',
  },
  {
    specifier: '@speed/test-utils/session-harness',
    entry: 'test-utils/src/session-harness.ts',
  },
  {
    specifier: '@speed/test-utils/turnstile',
    entry: 'test-utils/src/turnstile.ts',
  },
  {
    specifier: '@speed/test-utils/timers',
    entry: 'test-utils/src/timers.ts',
  },
  { specifier: '@speed/test-utils/wait', entry: 'test-utils/src/wait.ts' },
  { specifier: '@speed/test-utils/setup', entry: 'test-utils/src/setup.ts' },
  {
    specifier: '@speed/theme-presets',
    entry: 'packages/theme-presets/src/index.ts',
  },
])

function entryPath(entry) {
  return join(WORKSPACE_ROOT, entry)
}

/** The map as vite/vitest `resolve.alias` entries with absolute replacements, order preserved. */
export function speedAliases(map = SPEED_PACKAGE_MAP) {
  return map.map(({ specifier, entry }) => ({
    find: specifier,
    replacement: entryPath(entry),
  }))
}

/** The map as a tsconfig `paths` object relative to the consumer's own directory; tsconfig cannot import, so a drift test pins it. */
export function speedTsconfigPaths(consumerDir, map = SPEED_PACKAGE_MAP) {
  const paths = {}
  for (const { specifier, entry } of map) {
    const rel = relative(consumerDir, entryPath(entry)).split(sep).join('/')
    paths[specifier] = [rel.startsWith('.') ? rel : `./${rel}`]
  }
  return paths
}
