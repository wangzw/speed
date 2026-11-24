/**
 * The demo host's ESLint flat configuration.
 *
 * The app lives outside web/ (a workspace member through web/pnpm-workspace.yaml,
 * not a file inside it), so ESLint's walk-up discovery from this directory
 * never reaches web/eslint.config.mjs: the app carries its own config file,
 * importing the web config wholesale and appending the speed discipline
 * rules over the app's own src/. Flat-config objects are anchored to the
 * directory of the config that declared them, so the imported web config's
 * `packages/*` patterns never reach these files -- the rules are restated
 * here, scoped to this app, and each keeps the shared implementation and
 * exemption list in web/eslint-rules/.
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
 * origin contract requires (docs/adr/adr-web-runtime-config.md). The
 * no-native-disabled-control rule turns on over the same runtime src, with
 * the shared implementation and the shared exemption list in
 * web/eslint-rules/.
 *
 * The end-to-end tier under e2e/** is held to the honesty rules instead: it
 * drives the built product through a real browser, so network calls, route
 * interception and in-page script evaluation are the bypasses the
 * `speed/no-e2e-bypass` rule reports. Its one allowed side channel is a
 * WebAuthn.* virtual-authenticator fixture over a CDP session.
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
    // The end-to-end tier: rendered interaction only. The rule lives in the
    // workspace's rule directory and is registered by web/eslint.config.mjs,
    // which this config imports; this block is what scopes it to e2e/**.
    // A spec or helper may not spawn, write files, open a database client or
    // read the raw process log.
    files: ['e2e/**/*.ts'],
    rules: { 'speed/no-e2e-bypass': 'error' },
  },
  {
    // The assembly layer builds the binary, seeds the database, allocates
    // ports and probes the operations listener, so it may use the process,
    // filesystem and socket modules the rule otherwise bans. It is still
    // held to the rendered-interaction rules: no page evaluation, routes or
    // API request context.
    files: ['e2e/fixtures/**/*.ts', 'e2e/global-setup.ts'],
    rules: { 'speed/no-e2e-bypass': ['error', { allowAssembly: true }] },
  },
]
