/**
 * Shared DOM-test harness for account-ui tests: the host tree
 * (i18n, theme, query client, fixed display zone) with a fresh query client
 * that retries nothing.
 *
 * The tree is the RTL `wrapper`, so a rerender reconciles in place; change
 * the unit's `key` when only the unit must remount.
 */

import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactElement, ReactNode } from 'react'
import { I18nextProvider, registerNamespace } from '@speed/i18n'
import type { I18nInstance } from '@speed/i18n'
import { AppThemeProvider } from '@speed/ui-kit'
import { UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import {
  createTestI18n,
  createTestQueryClient,
  TestTimeZoneProvider,
} from '@speed/test-utils/render'
import type { RenderWithProvidersOptions } from '@speed/test-utils/render'
import type { RenderWithProvidersResult as BaseResult } from '@speed/test-utils/render'
import { ACCOUNT_UI_NAMESPACE, accountUiResources } from '../src/resources.js'

export type { RenderWithProvidersOptions }

export interface RenderWithProvidersResult extends BaseResult {
  /** The query client the tree renders with. */
  readonly queryClient: QueryClient
}

/** Fresh bilingual instance with both namespaces registered. */
export function createAccountUiI18n(language: string = 'zh-CN'): I18nInstance {
  const instance = createTestI18n(language)
  registerNamespace(instance, ACCOUNT_UI_NAMESPACE, accountUiResources)
  registerNamespace(instance, UI_KIT_NAMESPACE, uiKitResources)
  return instance
}

export { createTestQueryClient }

export function renderWithProviders(
  ui: ReactElement,
  options: RenderWithProvidersOptions = {},
): RenderWithProvidersResult {
  const { language = 'zh-CN', i18n, timeZone } = options
  const instance = i18n ?? createAccountUiI18n(language)
  const queryClient = createTestQueryClient()

  function Wrapper({ children }: { readonly children: ReactNode }): ReactElement {
    return (
      <TestTimeZoneProvider timeZone={timeZone}>
        <I18nextProvider i18n={instance}>
          <AppThemeProvider i18n={instance}>
            <QueryClientProvider client={queryClient}>
              {/* The page h1 an axe scan's page-has-heading-one needs. */}
              <h1>Account</h1>
              {children}
            </QueryClientProvider>
          </AppThemeProvider>
        </I18nextProvider>
      </TestTimeZoneProvider>
    )
  }

  const result = render(ui, { wrapper: Wrapper })
  return { ...result, i18n: instance, queryClient }
}
