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
