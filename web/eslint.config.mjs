import { defineConfig } from 'eslint/config'
import tseslint from 'typescript-eslint'
import { noDangerousHtmlRule } from './eslint-rules/no-dangerous-html.js'
import { noDirectHttpRule } from './eslint-rules/no-direct-http.js'
import { noDoubleFeedbackRegionRule } from './eslint-rules/no-double-feedback-region.js'
import { noE2eBypassRule } from './eslint-rules/no-e2e-bypass.js'
import { noLiteralTextRule } from './eslint-rules/no-literal-text.js'
import { noNativeDisabledControlRule } from './eslint-rules/no-native-disabled-control.js'

// Flat config has no "local rule" slot, so every local rule lives behind
// this plugin namespace.
const localPlugin = {
  plugins: {
    speed: {
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
    // The single whitelist; extending it is an architecture change.
    ignores: [...PACKAGE_RUNTIME_SRC.ignores, 'packages/api-client/**'],
    rules: {
      'speed/no-direct-http': 'error',
    },
  },
)
