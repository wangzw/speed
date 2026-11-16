/**
 * Shared DOM-test harness for notification-ui tests.
 *
 * renderWithProviders mounts a unit under the tree a real host builds:
 * I18nextProvider (the app-level provider components' translations need)
 * around AppThemeProvider (theme + MUI locale linkage + CssBaseline),
 * around QueryClientProvider (the surfaces read their data through the
 * @tanstack/react-query hooks generated into @speed/api-sdk), around
 * TenantProvider (the surfaces key their reads under the acting tenant
 * through auth-core's useCurrentTenant). The client is fresh per call and
 * retries nothing: an operation the test scripts to fail must surface its
 * error on the first attempt, and a mutation must not outlive its test.
 *
 * The i18n instance is created per call through the shared createTestI18n
 * (see @speed/test-utils/render's header), with both namespaces a
 * rendered component family can read registered: the notification-ui
 * namespace for this package's own strings and the ui-kit namespace,
 * because the surfaces compose ui-kit components whose built-in strings
 * speak ui-kit-namespace keys.
 *
 * Tests that exercise a language switch keep the returned instance and
 * act on it (await switchLanguage(i18n, 'en-US')); the provider's
 * 'languageChanged' subscription re-renders the tree.
 *
 * The tree renders under TestTimeZoneProvider, so a suite that names no
 * zone formats times in the fixed TEST_TIME_ZONE instead of the
 * machine's own -- a rendered time never depends on where the test runs.
 * A case that exercises another zone passes `timeZone`.
 */

import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactElement } from 'react'
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
  /** The query client the tree renders with. Tests whose operations
   * invalidate or seed react-query state act on it. */
  readonly queryClient: QueryClient
}

/** The tenant the harness's TenantProvider announces: the reads key under
 * it and a test asserts against it. */
export const TEST_TENANT_ID = 'acme'

/** Fresh bilingual instance with both namespaces registered. */
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
  const result = render(
    <TestTimeZoneProvider timeZone={timeZone}>
      <I18nextProvider i18n={instance}>
        <AppThemeProvider i18n={instance}>
          <QueryClientProvider client={queryClient}>
            {/* The page heading the surfaces nest under: every axe scan in
                the suite runs at the end of a behavioural test, and
                page-has-heading-one needs a level-1 heading in the tree. */}
            <TenantProvider tenantId={TEST_TENANT_ID}>
              <h1>App</h1>
              {ui}
            </TenantProvider>
          </QueryClientProvider>
        </AppThemeProvider>
      </I18nextProvider>
    </TestTimeZoneProvider>,
  )
  return { ...result, i18n: instance, queryClient }
}
