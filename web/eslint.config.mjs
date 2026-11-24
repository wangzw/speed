import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'eslint/config'
import tseslint from 'typescript-eslint'
import { noDangerousHtmlRule } from './eslint-rules/no-dangerous-html.js'
import { noDirectHttpRule } from './eslint-rules/no-direct-http.js'
import { noDoubleFeedbackRegionRule } from './eslint-rules/no-double-feedback-region.js'
import { noE2eBypassRule } from './eslint-rules/no-e2e-bypass.js'
import { noLiteralTextRule } from './eslint-rules/no-literal-text.js'
import { noNativeDisabledControlRule } from './eslint-rules/no-native-disabled-control.js'
import { eslintCacheVersion } from './scripts/eslint-cache-version.mjs'

const configDir = dirname(fileURLToPath(import.meta.url))

// Flat config has no "local rule" slot, so every local rule lives behind
// this plugin namespace.
const localPlugin = {
  plugins: {
    speed: {
      meta: {
        name: 'speed-local',
        version: eslintCacheVersion(
          join(configDir, 'eslint-rules'),
          join(configDir, 'pnpm-lock.yaml'),
        ),
      },
      rules: {
        'no-dangerous-html': noDangerousHtmlRule,
        'no-direct-http': noDirectHttpRule,
        'no-double-feedback-region': noDoubleFeedbackRegionRule,
        'no-e2e-bypass': noE2eBypassRule,
        'no-literal-text': noLiteralTextRule,
        'no-native-disabled-control': noNativeDisabledControlRule,
      },
    },
  },
}

const PACKAGE_RUNTIME_SRC = {
  files: ['packages/*/src/**/*.{ts,tsx}'],
  ignores: [
    '**/*.test.{ts,tsx}',
    '**/*.spec.{ts,tsx}',
    '**/test-utils/**',
  ],
}

/**
 * The one message every restricted pager import reports. It names the two
 * sanctioned routes and the reason MUI's modules are refused.
 */
const MUI_PAGINATION_MESSAGE =
  'Use TablePager or a DataTable pagination prop instead; MUI pagination moves focus off a direction once it becomes unavailable.'

/**
 * The pager belongs to ui-kit. MUI's pagination actions move focus off a
 * direction the moment it becomes unavailable, so the table footer is reached
 * through TablePager (the one module that replaces those actions) or a
 * DataTable `pagination` prop. Both ESLint surfaces share this table so the
 * two cannot drift; the demo host imports it rather than restating it.
 */
export const RESTRICTED_PAGINATION_IMPORTS = {
  paths: [
    {
      name: '@mui/material/TablePagination',
      message: MUI_PAGINATION_MESSAGE,
    },
    {
      name: '@mui/material/TablePaginationActions',
      message: MUI_PAGINATION_MESSAGE,
    },
    {
      name: '@mui/material/Pagination',
      message: MUI_PAGINATION_MESSAGE,
    },
    {
      name: '@mui/material',
      importNames: ['TablePagination', 'TablePaginationActions', 'Pagination'],
      message: MUI_PAGINATION_MESSAGE,
    },
  ],
  patterns: [
    {
      group: [
        '@mui/material/TablePagination/*',
        '@mui/material/TablePaginationActions/*',
        '@mui/material/Pagination/*',
      ],
      message: MUI_PAGINATION_MESSAGE,
    },
  ],
}

export default defineConfig(
  {
    ignores: ['**/node_modules/**', '**/dist/**', '**/*.browser.test.tsx'],
  },
  ...tseslint.configs.recommended,
  localPlugin,
  {
    rules: {
      '@typescript-eslint/no-explicit-any': 'error',
    },
  },
  {
    ...PACKAGE_RUNTIME_SRC,
    rules: {
      'speed/no-dangerous-html': 'error',
      'speed/no-double-feedback-region': 'error',
      'speed/no-literal-text': 'error',
      'speed/no-native-disabled-control': 'error',
    },
  },
  {
    ...PACKAGE_RUNTIME_SRC,
    ignores: [...PACKAGE_RUNTIME_SRC.ignores, 'packages/api-client/**'],
    rules: {
      'speed/no-direct-http': 'error',
    },
  },
  {
    ...PACKAGE_RUNTIME_SRC,
    rules: {
      'no-restricted-imports': ['error', RESTRICTED_PAGINATION_IMPORTS],
    },
  },
  {
    // The one source allowed to render MUI's pager: it replaces the default
    // actions so a direction that becomes unavailable keeps its focus. A
    // flat-config block that repeats a rule replaces it wholesale, so this
    // must follow the block that enables it.
    files: ['packages/ui-kit/src/components/TablePager.tsx'],
    rules: {
      'no-restricted-imports': 'off',
    },
  },
)
