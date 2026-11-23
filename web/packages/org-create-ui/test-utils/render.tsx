/**
 * Shared DOM-test harness for org-create-ui tests.
 *
 * renderWithProviders mounts a unit under the tree a host builds: the
 * i18n provider around the app theme provider (theme, MUI locale linkage
 * and CssBaseline). No query client or tenant provider is needed: the
 * package owns no request and reads no tenant state.
 *
 * The i18n instance is created per call through the shared createTestI18n
 * with both namespaces a render reads registered: the org-create-ui
 * namespace for this package's strings and the ui-kit namespace for the
 * components it composes.
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
import { AppThemeProvider, UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import { createTestI18n } from '@speed/test-utils/render'
import type {
  RenderWithProvidersOptions,
  RenderWithProvidersResult,
} from '@speed/test-utils/render'
import {
  ORG_CREATE_UI_NAMESPACE,
  orgCreateUiResources,
} from '../src/resources.js'

export type { RenderWithProvidersOptions, RenderWithProvidersResult }

/** Fresh bilingual instance with both namespaces registered. */
export function createOrgCreateUiI18n(
  language: string = 'zh-CN',
): I18nInstance {
  const instance = createTestI18n(language)
  registerNamespace(instance, ORG_CREATE_UI_NAMESPACE, orgCreateUiResources)
  registerNamespace(instance, UI_KIT_NAMESPACE, uiKitResources)
  return instance
}

export function renderWithProviders(
  ui: ReactElement,
  options: RenderWithProvidersOptions = {},
): RenderWithProvidersResult {
  const { language = 'zh-CN', i18n } = options
  const instance = i18n ?? createOrgCreateUiI18n(language)

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
