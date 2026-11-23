/**
 * Shared DOM-test harness for tenancy-ui tests.
 *
 * renderWithProviders mounts a unit under the tree a real host builds:
 * I18nextProvider (the app-level provider component translations need)
 * around AppThemeProvider (theme + MUI locale linkage + CssBaseline).
 * The i18n instance is created per call through the shared
 * createTestI18n (see @speed/test-utils/render's header), with both
 * namespaces the rendered affordance can read registered: the tenancy-ui
 * namespace for this package's own strings and the ui-kit namespace --
 * carried over from the harness shape the auth-ui suite established (its
 * provider tree registers both namespaces the tree renders under), so a
 * later tenancy-ui affordance rendering ui-kit text needs no harness
 * change.
 *
 * Tests that exercise a language switch keep the returned instance and
 * act on it (await switchLanguage(i18n, 'en-US')); the provider's
 * 'languageChanged' subscription re-renders the tree.
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
import { createTestI18n } from '@speed/test-utils/render'
import type { RenderWithProvidersOptions } from '@speed/test-utils/render'
import type { RenderWithProvidersResult } from '@speed/test-utils/render'
import { TENANCY_UI_NAMESPACE, tenancyUiResources } from '../src/resources.js'

export type { RenderWithProvidersOptions, RenderWithProvidersResult }

/** Fresh bilingual instance with both namespaces registered. */
export function createTenancyUiI18n(language: string = 'zh-CN'): I18nInstance {
  const instance = createTestI18n(language)
  registerNamespace(instance, TENANCY_UI_NAMESPACE, tenancyUiResources)
  registerNamespace(instance, UI_KIT_NAMESPACE, uiKitResources)
  return instance
}

export function renderWithProviders(
  ui: ReactElement,
  options: RenderWithProvidersOptions = {},
): RenderWithProvidersResult {
  const { language = 'zh-CN', i18n } = options
  const instance = i18n ?? createTenancyUiI18n(language)

  function Wrapper({ children }: { readonly children: ReactNode }): ReactElement {
    return (
      <I18nextProvider i18n={instance}>
        <AppThemeProvider i18n={instance}>{children}</AppThemeProvider>
      </I18nextProvider>
    )
  }

  const result = render(ui, { wrapper: Wrapper })
  return { ...result, i18n: instance }
}
