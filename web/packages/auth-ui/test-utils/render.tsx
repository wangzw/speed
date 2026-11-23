/**
 * Shared DOM-test harness for auth-ui tests.
 *
 * renderWithProviders mounts a unit under the tree a real host builds:
 * I18nextProvider (the app-level provider components' translations need)
 * around AppThemeProvider (theme + MUI locale linkage + CssBaseline).
 * The i18n instance is created per call through the shared
 * createTestI18n (see @speed/test-utils/render's header), with both
 * namespaces a rendered form family can read registered: the auth-ui
 * namespace for this package's own strings and the ui-kit namespace,
 * because the react-hook-form fields render through ui-kit's FormField,
 * whose required/invalid rules speak ui-kit-namespace keys
 * ('form.required').
 *
 * Tests that exercise a language switch keep the returned instance and
 * act on it (await switchLanguage(i18n, 'en-US')); the provider's
 * 'languageChanged' subscription re-renders the tree.
 *
 * The tree renders under TestTimeZoneProvider, so a suite that names no
 * zone formats times in the fixed TEST_TIME_ZONE instead of the
 * machine's own -- a rendered time never depends on where the test runs.
 * A case that exercises another zone passes `timeZone`.
 *
 * The provider tree is passed as the RTL `wrapper` option, not wrapped
 * around `ui` by hand: RTL re-wraps a `rerender(ui)` call in the very
 * same wrapper it rendered with, so a rerendered unit stays inside the
 * providers and React reconciles it in place. A hand-wrapped tree
 * instead gets REPLACED by the bare rerendered `ui` (the wrapper was
 * applied outside RTL's knowledge), which unmounts the providers and
 * remounts the unit -- a fresh instance whose refs and state reset on
 * the first rerender.
 *
 * A test that really needs a fresh mount changes the unit's `key`: the
 * element type and position stay the same, so only the keyed unit
 * remounts, while the provider tree and its i18n instance both survive.
 * Unmounting the tree with the result's `unmount` and calling
 * renderWithProviders again is the other way to start over, and it does
 * start over: the new call builds a fresh i18n instance unless the test
 * passes its own. Pick the first when only the unit should reset, the
 * second when the whole provider stack should.
 */

import { render } from '@testing-library/react'
import type { ReactElement, ReactNode } from 'react'
import { I18nextProvider, registerNamespace } from '@speed/i18n'
import type { I18nInstance } from '@speed/i18n'
import { AppThemeProvider } from '@speed/ui-kit'
import { UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import { createTestI18n, TestTimeZoneProvider } from '@speed/test-utils/render'
import type { RenderWithProvidersOptions } from '@speed/test-utils/render'
import type { RenderWithProvidersResult } from '@speed/test-utils/render'
import { AUTH_UI_NAMESPACE, authUiResources } from '../src/resources.js'

export type { RenderWithProvidersOptions, RenderWithProvidersResult }

/** Fresh bilingual instance with both namespaces registered. */
export function createAuthUiI18n(language: string = 'zh-CN'): I18nInstance {
  const instance = createTestI18n(language)
  registerNamespace(instance, AUTH_UI_NAMESPACE, authUiResources)
  registerNamespace(instance, UI_KIT_NAMESPACE, uiKitResources)
  return instance
}

export function renderWithProviders(
  ui: ReactElement,
  options: RenderWithProvidersOptions = {},
): RenderWithProvidersResult {
  const { language = 'zh-CN', i18n, timeZone } = options
  const instance = i18n ?? createAuthUiI18n(language)

  function Wrapper({ children }: { readonly children: ReactNode }): ReactElement {
    return (
      <TestTimeZoneProvider timeZone={timeZone}>
        <I18nextProvider i18n={instance}>
          <AppThemeProvider i18n={instance}>{children}</AppThemeProvider>
        </I18nextProvider>
      </TestTimeZoneProvider>
    )
  }

  const result = render(ui, { wrapper: Wrapper })
  return { ...result, i18n: instance }
}
