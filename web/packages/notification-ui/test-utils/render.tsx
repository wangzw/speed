import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactElement, ReactNode } from 'react'
import { TenantProvider } from '@speed/auth-core'
import { I18nextProvider, registerNamespace } from '@speed/i18n'
import type { I18nInstance } from '@speed/i18n'
import { AppThemeProvider, UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import {
  createTestI18n,
  createTestQueryClient,
  TestTimeZoneProvider,
} from '@speed/test-utils/render'
import type { RenderWithProvidersOptions } from '@speed/test-utils/render'
import type { RenderWithProvidersResult as BaseResult } from '@speed/test-utils/render'
import {
  NOTIFICATION_UI_NAMESPACE,
  notificationUiResources,
} from '../src/resources.js'

export type { RenderWithProvidersOptions }

export interface RenderWithProvidersResult extends BaseResult {
  readonly queryClient: QueryClient
}

export const TEST_TENANT_ID = 'acme'

export function createNotificationUiI18n(
  language: string = 'zh-CN',
): I18nInstance {
  const instance = createTestI18n(language)
  registerNamespace(instance, NOTIFICATION_UI_NAMESPACE, notificationUiResources)
  registerNamespace(instance, UI_KIT_NAMESPACE, uiKitResources)
  return instance
}

export { createTestQueryClient }

export function renderWithProviders(
  ui: ReactElement,
  options: RenderWithProvidersOptions = {},
): RenderWithProvidersResult {
  const { language = 'zh-CN', i18n, timeZone } = options
  const instance = i18n ?? createNotificationUiI18n(language)
  const queryClient = createTestQueryClient()

  function Wrapper({ children }: { readonly children: ReactNode }): ReactElement {
    return (
      <TestTimeZoneProvider timeZone={timeZone}>
        <I18nextProvider i18n={instance}>
          <AppThemeProvider i18n={instance}>
            <QueryClientProvider client={queryClient}>
              <TenantProvider tenantId={TEST_TENANT_ID}>
                <h1>App</h1>
                {children}
              </TenantProvider>
            </QueryClientProvider>
          </AppThemeProvider>
        </I18nextProvider>
      </TestTimeZoneProvider>
    )
  }

  const result = render(ui, { wrapper: Wrapper })
  return { ...result, i18n: instance, queryClient }
}
