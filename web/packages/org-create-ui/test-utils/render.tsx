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
 */

import { render } from '@testing-library/react'
import type { ReactElement } from 'react'
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
  const result = render(
    <I18nextProvider i18n={instance}>
      <AppThemeProvider i18n={instance}>{ui}</AppThemeProvider>
    </I18nextProvider>,
  )
  return { ...result, i18n: instance }
}
