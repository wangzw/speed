/**
 * Shared DOM-test harness for console-ui tests.
 *
 * renderWithProviders mounts a unit under the tree a real host builds:
 * I18nextProvider (the pages' translations need) around AppThemeProvider
 * (theme + MUI locale linkage + CssBaseline) around QueryClientProvider
 * (the console operations bundle's hooks run on a query client) around an
 * optional ConsoleOperationsProvider. The client is fresh per call and
 * retries nothing: an operation the test scripts to fail surfaces its
 * error on the first attempt, not after react-query default retries, and
 * a mutation must not outlive its test.
 *
 * A test may supply its own operations bundle (see test-utils/operations)
 * to script the pages; omitting it leaves the pages on the shipped
 * consoleOperations, so a suite can exercise the generated surface the
 * package binds (binding a request function through the api-sdk runtime
 * seam first).
 *
 * The i18n instance is created per call through the shared createTestI18n
 * with every namespace a rendered component reads registered: the
 * console-ui namespace for this package's own strings, the ui-kit
 * namespace (the pages and the shell compose ui-kit components whose
 * built-in strings speak ui-kit-namespace keys), and the layout-kit
 * namespace (ConsoleShell renders @speed/layout-kit's AppShell frame,
 * whose banner skip link and nav landmark labels speak layout-kit-namespace
 * keys).
 *
 * The tree renders under the package's feature context by default, with
 * every feature present: a page test that does not care about gating sees
 * the whole page, not the fail-closed surface a provider-less render gets
 * in production. A gating test supplies its own predicate, and `null`
 * mounts no provider at all (the fail-closed default itself).
 */

import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactElement } from 'react'
import type { AuthSession } from '@speed/auth-core'
import type { FeatureName } from '@speed/api-sdk/features'
import { I18nextProvider, registerNamespace } from '@speed/i18n'
import type { I18nInstance } from '@speed/i18n'
import { LAYOUT_KIT_NAMESPACE, layoutKitResources } from '@speed/layout-kit'
import {
  AppThemeProvider,
  UI_KIT_NAMESPACE,
  uiKitResources,
} from '@speed/ui-kit'
import {
  createTestI18n,
  createTestQueryClient,
} from '@speed/test-utils/render'
import type {
  RenderWithProvidersOptions as BaseOptions,
  RenderWithProvidersResult as BaseResult,
} from '@speed/test-utils/render'
import { CONSOLE_UI_NAMESPACE, consoleUiResources } from '../src/resources.js'
import { ConsoleOperationsProvider } from '../src/operations.js'
import type { ConsoleOperations } from '../src/operations.js'
import { ConsoleFeatureProvider } from '../src/internal/features.js'
import { ConsoleSessionProvider } from '../src/internal/session.js'

export interface RenderWithProvidersOptions extends BaseOptions {
  /**
   * The operations bundle the tree renders with. Optional: supplying one
   * scripts the pages (see test-utils/operations); omitting it leaves
   * them on the shipped consoleOperations, which reach the generated
   * @speed/api-sdk surface over whatever request function is bound.
   */
  readonly operations?: ConsoleOperations
  /**
   * The session the console's step-up-gated writes elevate. Optional:
   * supplying one mounts ConsoleSessionProvider, so a write a test
   * answers with a step-up refusal opens the re-auth dialog; omitting it
   * leaves the pages with no session to elevate.
   */
  readonly session?: AuthSession
  /**
   * The feature answer the tree renders with. Omitted, every feature is
   * present; `null` mounts no provider, so the context keeps its
   * fail-closed default.
   */
  readonly hasFeature?: ((name: FeatureName) => boolean) | null
}

export interface RenderWithProvidersResult extends BaseResult {
  /** The query client the tree renders with; tests act on it to seed or await state. */
  readonly queryClient: QueryClient
}

/** Fresh bilingual instance with every namespace a render reads registered. */
export function createConsoleUiI18n(language: string = 'zh-CN'): I18nInstance {
  const instance = createTestI18n(language)
  registerNamespace(instance, CONSOLE_UI_NAMESPACE, consoleUiResources)
  registerNamespace(instance, UI_KIT_NAMESPACE, uiKitResources)
  registerNamespace(instance, LAYOUT_KIT_NAMESPACE, layoutKitResources)
  return instance
}

export { createTestQueryClient }

export function renderWithProviders(
  ui: ReactElement,
  options: RenderWithProvidersOptions = {},
): RenderWithProvidersResult {
  const { language = 'zh-CN', i18n, operations, session } = options
  const hasFeature =
    options.hasFeature === undefined ? () => true : options.hasFeature
  const instance = i18n ?? createConsoleUiI18n(language)
  const queryClient = createTestQueryClient()
  let tree: ReactElement =
    operations === undefined ? (
      ui
    ) : (
      <ConsoleOperationsProvider operations={operations}>{ui}</ConsoleOperationsProvider>
    )
  if (session !== undefined) {
    tree = <ConsoleSessionProvider session={session}>{tree}</ConsoleSessionProvider>
  }
  if (hasFeature !== null) {
    tree = <ConsoleFeatureProvider hasFeature={hasFeature}>{tree}</ConsoleFeatureProvider>
  }
  const result = render(
    <I18nextProvider i18n={instance}>
      <AppThemeProvider i18n={instance}>
        <QueryClientProvider client={queryClient}>{tree}</QueryClientProvider>
      </AppThemeProvider>
    </I18nextProvider>,
  )
  return { ...result, i18n: instance, queryClient }
}
