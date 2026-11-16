/**
 * Shared DOM-test harness for admin-ui tests.
 *
 * renderWithProviders mounts a unit under the tree a real host builds:
 * I18nextProvider (the pages' translations need) around AppThemeProvider
 * (theme + MUI locale linkage + CssBaseline) around TestTimeZoneProvider
 * (the display zone the pages format times in, TEST_TIME_ZONE unless the
 * suite names another) around QueryClientProvider (the console operations bundle's
 * hooks run on a query client) around an optional
 * AdminOperationsProvider. The client is fresh per call and
 * retries nothing: an operation the test scripts to fail surfaces its
 * error on the first attempt, not after react-query default retries, and
 * a mutation must not outlive its test.
 *
 * A test may supply its own operations bundle (see test-utils/operations)
 * to script the pages; omitting it leaves the pages on the shipped
 * adminOperations, so a suite can exercise the generated surface the
 * package binds (binding a request function through the api-sdk runtime
 * seam first).
 *
 * The i18n instance is created per call through the shared createTestI18n
 * with every namespace a rendered component reads registered: the
 * admin-ui namespace for this package's own strings, the ui-kit
 * namespace (the pages and the shell compose ui-kit components whose
 * built-in strings speak ui-kit-namespace keys), and the layout-kit
 * namespace (AdminShell renders @speed/layout-kit's AppShell frame,
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
  ORG_CREATE_UI_NAMESPACE,
  orgCreateUiResources,
} from '@speed/org-create-ui'
import {
  AppThemeProvider,
  UI_KIT_NAMESPACE,
  uiKitResources,
} from '@speed/ui-kit'
import {
  createTestI18n,
  createTestQueryClient,
  TestTimeZoneProvider,
} from '@speed/test-utils/render'
import type {
  RenderWithProvidersOptions as BaseOptions,
  RenderWithProvidersResult as BaseResult,
} from '@speed/test-utils/render'
import type { ThemePreset } from '@speed/theme-presets'
import { ADMIN_UI_NAMESPACE, adminUiResources } from '../src/resources.js'
import { AdminOperationsProvider } from '../src/operations.js'
import type { AdminOperations } from '../src/operations.js'
import { AdminFeatureProvider } from '../src/internal/features.js'
import { AdminSessionProvider } from '../src/internal/session.js'

export interface RenderWithProvidersOptions extends BaseOptions {
  /**
   * The operations bundle the tree renders with. Optional: supplying one
   * scripts the pages (see test-utils/operations); omitting it leaves
   * them on the shipped adminOperations, which reach the generated
   * @speed/api-sdk surface over whatever request function is bound.
   */
  readonly operations?: AdminOperations
  /**
   * The session the console's step-up-gated writes elevate. Optional:
   * supplying one mounts AdminSessionProvider, so a write a test
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
  /**
   * The theme's starting color mode; omitted, the provider's light default.
   * A suite that renders both modes passes the mode it wants.
   */
  readonly defaultMode?: 'light' | 'dark' | 'system'
  /** The preset shelf the theme provider may switch between. */
  readonly presets?: readonly ThemePreset[]
  /** The preset id the provider starts at. */
  readonly defaultPreset?: string
}

export interface RenderWithProvidersResult extends BaseResult {
  /** The query client the tree renders with; tests act on it to seed or await state. */
  readonly queryClient: QueryClient
}

/** Fresh bilingual instance with every namespace a render reads registered. */
export function createAdminUiI18n(language: string = 'zh-CN'): I18nInstance {
  const instance = createTestI18n(language)
  registerNamespace(instance, ADMIN_UI_NAMESPACE, adminUiResources)
  registerNamespace(instance, UI_KIT_NAMESPACE, uiKitResources)
  registerNamespace(instance, LAYOUT_KIT_NAMESPACE, layoutKitResources)
  registerNamespace(instance, ORG_CREATE_UI_NAMESPACE, orgCreateUiResources)
  return instance
}

export { createTestQueryClient }

export function renderWithProviders(
  ui: ReactElement,
  options: RenderWithProvidersOptions = {},
): RenderWithProvidersResult {
  const {
    language = 'zh-CN',
    i18n,
    timeZone,
    operations,
    session,
    defaultMode,
    presets,
    defaultPreset,
  } = options
  const hasFeature =
    options.hasFeature === undefined ? () => true : options.hasFeature
  const instance = i18n ?? createAdminUiI18n(language)
  const queryClient = createTestQueryClient()
  let tree: ReactElement =
    operations === undefined ? (
      ui
    ) : (
      <AdminOperationsProvider operations={operations}>{ui}</AdminOperationsProvider>
    )
  if (session !== undefined) {
    tree = <AdminSessionProvider session={session}>{tree}</AdminSessionProvider>
  }
  if (hasFeature !== null) {
    tree = <AdminFeatureProvider hasFeature={hasFeature}>{tree}</AdminFeatureProvider>
  }
  const result = render(
    <I18nextProvider i18n={instance}>
      <AppThemeProvider
        i18n={instance}
        presets={presets}
        defaultPreset={defaultPreset}
        defaultMode={defaultMode}
      >
        <TestTimeZoneProvider timeZone={timeZone}>
          <QueryClientProvider client={queryClient}>{tree}</QueryClientProvider>
        </TestTimeZoneProvider>
      </AppThemeProvider>
    </I18nextProvider>,
  )
  return { ...result, i18n: instance, queryClient }
}
