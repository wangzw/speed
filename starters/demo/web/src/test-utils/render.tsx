/**
 * The demo host's render harness: the provider tree a component or view suite
 * needs, built from the app's own pieces rather than the production assembly.
 *
 * It mounts DemoApp -- the routing composition and the shell switch -- under a
 * fresh bilingual i18n instance with every namespace the app's views render
 * from, the ui-kit theme provider, a no-retry query client, and the
 * app-services provider carrying a panel session and a client that must not be
 * called (a suite that drives the console pages overrides the shipped console
 * operations instead). The session is attached to the auth hooks, so the
 * shells read it: a fresh anonymous session by default -- the tenant shell then
 * fail-closes to its sign-in branch, exactly as a fresh visitor's page does --
 * and a suite that wants the authenticated frame passes its own (see
 * test-utils/session).
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, type RenderResult } from '@testing-library/react'
import { attachSession, createAuthSession } from '@speed/auth-core'
import type { AuthSession } from '@speed/auth-core'
import { createMemoryAccessTokenStore } from '@speed/api-client'
import type { RequestFn } from '@speed/api-client'
import { AUTH_UI_NAMESPACE, authUiResources } from '@speed/auth-ui'
import { CONSOLE_UI_NAMESPACE, consoleUiResources } from '@speed/console-ui'
import { createI18n, I18nextProvider, registerNamespace } from '@speed/i18n'
import { LAYOUT_KIT_NAMESPACE, layoutKitResources } from '@speed/layout-kit'
import { PRODUCT_SHELL_NAMESPACE, productShellResources } from '@speed/product-shell'
import type { SpeedAppServices } from '@speed/product-shell/bootstrap'
import { AppThemeProvider, UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import { AppServicesProvider } from '../app-services.js'
import { DemoApp } from '../app.js'
import { DEMO_NAMESPACE, demoResources } from '../resources.js'

export interface RenderDemoAppOptions {
  /** The instance's starting language; defaults to en-US. */
  readonly language?: string
  /**
   * The session the trees read, attached to the auth hooks. A fresh anonymous
   * session by default; a suite that wants the authenticated frame (or a
   * session whose state it drives) passes its own.
   */
  readonly session?: AuthSession
}

/** Renders DemoApp under the app's provider tree. */
export function renderDemoApp(
  options: RenderDemoAppOptions = {},
): RenderResult {
  const i18n = createI18n({
    supportedLanguages: ['zh-CN', 'en-US'],
    defaultLanguage: options.language ?? 'en-US',
    storage: null,
    urlParameterName: null,
    navigatorLanguages: [],
  })
  registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)
  registerNamespace(i18n, LAYOUT_KIT_NAMESPACE, layoutKitResources)
  registerNamespace(i18n, AUTH_UI_NAMESPACE, authUiResources)
  registerNamespace(i18n, PRODUCT_SHELL_NAMESPACE, productShellResources)
  registerNamespace(i18n, CONSOLE_UI_NAMESPACE, consoleUiResources)
  registerNamespace(i18n, DEMO_NAMESPACE, demoResources)

  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const api = (async () => {
    throw new Error('the render harness client must not be called')
  }) as RequestFn
  const session =
    options.session ?? createAuthSession(createMemoryAccessTokenStore())
  attachSession(session)
  const services: SpeedAppServices = { session, api, queryClient }

  return render(
    <I18nextProvider i18n={i18n}>
      <AppThemeProvider i18n={i18n}>
        <QueryClientProvider client={queryClient}>
          <AppServicesProvider services={services}>
            <DemoApp />
          </AppServicesProvider>
        </QueryClientProvider>
      </AppThemeProvider>
    </I18nextProvider>,
  )
}
