import webConfig from '../../../web/eslint.config.mjs'

const APP_RUNTIME_SRC = {
  files: ['src/**/*.{ts,tsx}'],
  ignores: ['**/*.test.{ts,tsx}', '**/*.spec.{ts,tsx}', '**/test-utils/**'],
}

export default [
  ...webConfig,
  {
    ...APP_RUNTIME_SRC,
    rules: {
      'speed/no-dangerous-html': 'error',
      'speed/no-double-feedback-region': 'error',
      'speed/no-literal-text': 'error',
      'speed/no-native-disabled-control': 'error',
    },
  },
  {
    ...APP_RUNTIME_SRC,
    ignores: [...APP_RUNTIME_SRC.ignores, 'src/runtime-config.ts'],
    rules: { 'speed/no-direct-http': 'error' },
  },
  {
    files: ['e2e/**/*.ts'],
    rules: { 'speed/no-e2e-bypass': 'error' },
  },
  {
    files: ['e2e/fixtures/**/*.ts', 'e2e/global-setup.ts'],
    rules: { 'speed/no-e2e-bypass': ['error', { allowAssembly: true }] },
  },
]
