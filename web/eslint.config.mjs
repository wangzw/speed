/**
 * Flat-config ESLint setup for the whole web/ workspace.
 *
 * The single config site: every package runs "eslint ." from its own
 * directory and ESLint discovers this file by walking up, so a new package
 * needs no config of its own -- and a rule change lands in one place.
 *
 * Deliberately minimal (see AGENTS.md's deferral notes):
 * typescript-eslint's recommended rules plus an explicit
 * no-explicit-any error, and the no-literal-text rule (web/eslint-rules/
 * no-literal-text.js, tested in web/eslint-rules/no-literal-text.test.mjs)
 * enforcing that package UI text comes from the i18n namespace. It applies
 * to every package's src but deliberately not to test files and test
 * utilities: fixture strings are data, not rendered product text. The
 * no-direct-http rule (web/eslint-rules/no-direct-http.js, tested in
 * web/eslint-rules/no-direct-http.test.mjs) enforces the other half of the
 * API-contract discipline: hand-written HTTP happens only inside
 * @speed/api-client, which the rule's sole config-level whitelist
 * exempts -- the no-literal-text rule still applies inside api-client,
 * whitelisting HTTP does not whitelist text. The no-dangerous-html rule
 * (web/eslint-rules/no-dangerous-html.js, tested in
 * web/eslint-rules/no-dangerous-html.test.mjs) holds the premise the
 * i18n instance's interpolation.escapeValue: false rests on: no runtime
 * layer may consume HTML, so a markup sink added later cannot silently
 * turn a verbatim interpolated value into markup. The
 * no-e2e-bypass rule (web/eslint-rules/no-e2e-bypass.js, tested in
 * web/eslint-rules/no-e2e-bypass.test.mjs) guards the end-to-end tier's
 * honesty: it reports network, route-interception, evaluate and non-WebAuthn
 * CDP calls, allowing only the WebAuthn.* virtual-authenticator setup the
 * requirements author ruled a browser-device fixture may use. The demo host
 * turns it on over its e2e/** directory. The no-double-feedback-region rule
 * (web/eslint-rules/no-double-feedback-region.js, tested in
 * web/eslint-rules/no-double-feedback-region.test.mjs) holds a component to
 * one WriteFeedbackRegion per control group: two regions in one component
 * can show two results at once. Its exemption list states which components
 * legitimately own several control groups, and the rule's scan test keeps
 * that list exactly equal to the tree. The no-native-disabled-control rule
 * (web/eslint-rules/no-native-disabled-control.js, tested in
 * web/eslint-rules/no-native-disabled-control.test.mjs) reports a `disabled`
 * value that is not a literal and a `loading` value other than the literal
 * false on a button-shaped MUI control: either renders natively disabled,
 * and a focused control that becomes natively disabled drops focus to the
 * document body. Its
 * exemption list states which controls cannot hold focus at that moment,
 * and the rule's scan test keeps that list exactly equal to the tree. The
 * react-hooks plugin stays out: no stateful component exists outside
 * ui-kit's controlled
 * set -- state belongs to the host and every interaction reports up
 * through callbacks, the FileUploader queue rendering from host-owned
 * rows props with the upload transport as host code -- and staying out
 * keeps this config dependency-free.
 */
import { defineConfig } from 'eslint/config'
import tseslint from 'typescript-eslint'
import { noDangerousHtmlRule } from './eslint-rules/no-dangerous-html.js'
import { noDirectHttpRule } from './eslint-rules/no-direct-http.js'
import { noDoubleFeedbackRegionRule } from './eslint-rules/no-double-feedback-region.js'
import { noE2eBypassRule } from './eslint-rules/no-e2e-bypass.js'
import { noLiteralTextRule } from './eslint-rules/no-literal-text.js'
import { noNativeDisabledControlRule } from './eslint-rules/no-native-disabled-control.js'

/**
 * Inline plugin holding the workspace's own rules. Flat config has no
 * "local rule" slot -- every rule must live behind a plugin namespace,
 * hence the bare 'speed' prefix used by the rules below.
 */
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

/** Package runtime code: src minus tests and test utilities. */
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
    // The browser cases (`*.browser.test.tsx`) are measured by the
    // separate browser tier (web/vitest.browser.config.ts, `make
    // web-browser`) and deliberately stay out of the unit packages'
    // lint and test legs. The demo host imports this config, so the same
    // ignore reaches its cases under starters/demo/web/src. A package's
    // browser cases are typechecked through its tsconfig.browser.json; the
    // demo host's are typechecked through starters/demo/web/tsconfig.json,
    // whose program includes all of src.
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
      'speed/no-native-disabled-control': 'warn',
    },
  },
  {
    ...PACKAGE_RUNTIME_SRC,
    // The single whitelist: api-client is the one package where
    // hand-written HTTP happens, by design (its createClient wires the
    // injectable fetch, timeout, retry and silent refresh). Extending
    // this whitelist is an architecture change -- see the rule file.
    ignores: [...PACKAGE_RUNTIME_SRC.ignores, 'packages/api-client/**'],
    rules: {
      'speed/no-direct-http': 'error',
    },
  },
)
