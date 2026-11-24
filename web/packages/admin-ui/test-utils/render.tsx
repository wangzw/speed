/**
 * Shared DOM-test harness: renderWithProviders mounts a unit under the tree a
 * real host builds (i18n, theme, display zone, query client and the optional
 * console providers) as RTL's `wrapper`, so a rerender reconciles in place.
 */

import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactElement, ReactNode } from 'react'
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
  /** The operations bundle the tree renders with; omitted leaves the shipped bundle. */
  readonly operations?: AdminOperations
  /** The session that mounts AdminSessionProvider; omitted leaves pages with none. */
  readonly session?: AuthSession
  /** The feature answer; omitted means every feature present, `null` mounts no provider. */
  readonly hasFeature?: ((name: FeatureName) => boolean) | null
  /** The theme's starting color mode; omitted, the provider's light default. */
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

  function Wrapper({ children }: { readonly children: ReactNode }): ReactElement {
    let tree: ReactNode = children
    if (operations !== undefined) {
      tree = <AdminOperationsProvider operations={operations}>{tree}</AdminOperationsProvider>
    }
    if (session !== undefined) {
      tree = <AdminSessionProvider session={session}>{tree}</AdminSessionProvider>
    }
    if (hasFeature !== null) {
      tree = <AdminFeatureProvider hasFeature={hasFeature}>{tree}</AdminFeatureProvider>
    }
    return (
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
      </I18nextProvider>
    )
  }

  const result = render(ui, { wrapper: Wrapper })
  return { ...result, i18n: instance, queryClient }
}
