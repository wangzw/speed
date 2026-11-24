/**
 * Shared DOM-test harness for ui-kit tests: mounts a unit under
 * `I18nextProvider` and `AppThemeProvider`. The tree is RTL's `wrapper`, not
 * a hand wrapper, so `rerender` keeps the providers mounted.
 */

import { render } from '@testing-library/react'
import type { ReactElement, ReactNode } from 'react'
import { I18nextProvider, registerNamespace } from '@speed/i18n'
import type { I18nInstance } from '@speed/i18n'
import type { TokensOverride } from '@speed/tokens'
import type { ThemePreset } from '@speed/theme-presets'
import { createTestI18n } from '@speed/test-utils/render'
import type { RenderWithProvidersOptions as BaseOptions } from '@speed/test-utils/render'
import type { RenderWithProvidersResult } from '@speed/test-utils/render'
import { AppThemeProvider } from '../src/theme/AppThemeProvider.js'
import type { AppThemeModePreference } from '../src/theme/createAppTheme.js'
import { UI_KIT_NAMESPACE, uiKitResources } from '../src/resources.js'

export type { RenderWithProvidersResult }

export interface RenderWithProvidersOptions extends BaseOptions {
  /** Project-layer token overrides passed to AppThemeProvider. */
  readonly projectTokens?: TokensOverride
  /** Tenant-layer token overrides passed to AppThemeProvider. */
  readonly tenantOverrides?: TokensOverride
  readonly presets?: readonly ThemePreset[]
  /** The preset id the provider starts at. */
  readonly defaultPreset?: string
  /** The color-mode preference the provider starts at. */
  readonly defaultMode?: AppThemeModePreference
}

export function createUiKitI18n(language: string = 'zh-CN'): I18nInstance {
  const instance = createTestI18n(language)
  registerNamespace(instance, UI_KIT_NAMESPACE, uiKitResources)
  return instance
}

export function renderWithProviders(
  ui: ReactElement,
  options: RenderWithProvidersOptions = {},
): RenderWithProvidersResult {
  const {
    language = 'zh-CN',
    i18n,
    projectTokens,
    tenantOverrides,
    presets,
    defaultPreset,
    defaultMode,
  } = options
  const instance = i18n ?? createUiKitI18n(language)

  function Wrapper({ children }: { readonly children: ReactNode }): ReactElement {
    return (
      <I18nextProvider i18n={instance}>
        <AppThemeProvider
          i18n={instance}
          projectTokens={projectTokens}
          tenantOverrides={tenantOverrides}
          presets={presets}
          defaultPreset={defaultPreset}
          defaultMode={defaultMode}
        >
          {children}
        </AppThemeProvider>
      </I18nextProvider>
    )
  }

  const result = render(ui, { wrapper: Wrapper })
  return { ...result, i18n: instance }
}
