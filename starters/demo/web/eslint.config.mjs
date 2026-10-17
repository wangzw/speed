/**
 * The demo host's ESLint flat configuration.
 *
 * The app lives outside web/ (a workspace member through web/pnpm-workspace.yaml,
 * not a file inside it), so ESLint's walk-up discovery from this directory
 * never reaches web/eslint.config.mjs: the app carries its own config file,
 * importing the web config wholesale and appending the speed discipline
 * rules over the app's own src/. Flat-config objects are anchored to the
 * directory of the config that declared them, so the imported web config's
 * `packages/*` patterns never reach these files -- the two rules are restated
 * here, scoped to this app.
 *
 * The app's runtime src must not carry inline user-facing text (every string
 * renders from the demo namespace), and must not call HTTP directly: the app
 * reaches the backend through @speed/api-sdk's generated operations over the
 * bindRequestFn seam. It also may not consume HTML: the i18n instance inserts
 * interpolated values verbatim (interpolation.escapeValue is false), which is
 * safe only while no runtime layer treats a rendered string as markup. The one
 * exempted module is src/runtime-config.ts, whose
 * sole request reads the deployment's static runtime-config.json beside the
 * entry document -- a static asset, not an API call, that the runtime API
 * origin contract requires (docs/adr/adr-web-runtime-config-2026-10-15.md).
 */
import webConfig from '../../../web/eslint.config.mjs'

/** Runtime src of the app: where the discipline rules apply. */
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
      'speed/no-literal-text': 'error',
    },
  },
  {
    ...APP_RUNTIME_SRC,
    ignores: [...APP_RUNTIME_SRC.ignores, 'src/runtime-config.ts'],
    rules: { 'speed/no-direct-http': 'error' },
  },
]
