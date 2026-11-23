/**
 * Shared DOM-test harness for account-ui tests.
 *
 * renderWithProviders mounts a unit under the tree a real host builds:
 * I18nextProvider (the app-level provider components' translations need)
 * around AppThemeProvider (theme + MUI locale linkage + CssBaseline),
 * around QueryClientProvider -- account-ui surfaces read their data
 * through the @tanstack/react-query hooks generated into @speed/api-sdk,
 * so every rendered unit needs a query client, the one provider the
 * auth-ui harness does not carry. The client is fresh per call and
 * retries nothing: an operation the test scripts to fail must surface its
 * error on the first attempt, not after react-query default retries, and
 * a mutation must not outlive its test.
 *
 * The i18n instance is created per call through the shared
 * createTestI18n (see @speed/test-utils/render's header), with both
 * namespaces a rendered component family can read registered: the
 * account-ui namespace for this package's own strings and the ui-kit
 * namespace, because the surfaces compose ui-kit components whose
 * built-in strings speak ui-kit-namespace keys.
 *
 * Tests that exercise a language switch keep the returned instance and
 * act on it (await switchLanguage(i18n, 'en-US')); the provider's
 * 'languageChanged' subscription re-renders the tree.
 *
 * The tree renders under TestTimeZoneProvider, so a suite that names no
 * zone formats times in the fixed TEST_TIME_ZONE instead of the
 * machine's own -- a rendered time never depends on where the test runs.
 * A case that exercises another zone passes `timeZone`, the way a host
 * supplies the account's stored choice through the display context.
 *
 * The provider tree is passed as the RTL `wrapper` option, not wrapped
 * around `ui` by hand: RTL re-wraps a `rerender(ui)` call in the very
 * same wrapper it rendered with, so a rerendered unit stays inside the
 * providers and React reconciles it in place. A hand-wrapped tree
 * instead gets REPLACED by the bare rerendered `ui` (the wrapper was
 * applied outside RTL's knowledge), which unmounts the providers and
 * remounts the unit -- a fresh instance whose refs and state reset, and
 * whose query and theme context are rebuilt, on the first rerender.
 *
 * A test that really needs a fresh mount changes the unit's `key`: the
 * element type and position stay the same, so only the keyed unit
 * remounts, while the provider tree, its query client and its i18n
 * instance all survive -- the query cache keeps its entries. Unmounting
 * the tree with the result's `unmount` and calling renderWithProviders
 * again is the other way to start over, and it does start over: the new
 * call builds a fresh query client (the old cache is gone) and, when the
 * test passes no instance of its own, a fresh i18n instance. Pick the
 * first when only the unit should reset, the second when the whole
 * provider stack should.
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
  /**
   * The query client the tree renders with. Tests whose operations
   * invalidate or seed react-query state act on it.
   */
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
              {/* The account page every account-ui surface lives on: the
                  page's h1 above the section under test. Every axe scan in
                  the suite runs at the end of a behavioural test, and
                  page-has-heading-one is determinate in jsdom now (see
                  @speed/test-utils/axe's header) -- a section rendered with
                  no page heading context would fail the scan instead of
                  passing by indeterminacy. The section's own header is a
                  level-2 heading standing in under this h1. */}
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
