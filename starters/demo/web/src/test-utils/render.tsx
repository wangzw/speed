/**
 * The demo host's render harness: the provider tree a component or view suite
 * needs, built from the app's own pieces rather than the production assembly.
 *
 * It mounts DemoApp -- the routing composition and the shell switch -- under a
 * fresh bilingual i18n instance with every namespace the app's views render
 * from, the ui-kit theme provider, a no-retry query client, and the
 * app-services provider carrying a session and a client that must not be
 * called (a suite that drives the console pages overrides the shipped console
 * operations instead). The session is attached to the auth hooks, so the
 * shells read it: the test session by default (test-utils/session) -- an
 * anonymous snapshot whose discovery reads resolve, so the sign-in surface
 * renders without a network -- and a suite that wants the authenticated frame
 * passes its own.
 */

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, type RenderResult } from '@testing-library/react'
import { attachSession } from '@speed/auth-core'
import type { AuthSession } from '@speed/auth-core'
import type { RequestFn, SpeedClient } from '@speed/api-client'
import { ACCOUNT_UI_NAMESPACE, accountUiResources } from '@speed/account-ui'
import { AUTH_UI_NAMESPACE, authUiResources } from '@speed/auth-ui'
import { CONSOLE_UI_NAMESPACE, consoleUiResources } from '@speed/console-ui'
import { createI18n, I18nextProvider, registerNamespace } from '@speed/i18n'
import { LAYOUT_KIT_NAMESPACE, layoutKitResources } from '@speed/layout-kit'
import {
  NOTIFICATION_UI_NAMESPACE,
  notificationUiResources,
} from '@speed/notification-ui'
import { PRODUCT_SHELL_NAMESPACE, productShellResources } from '@speed/product-shell'
import { TENANCY_UI_NAMESPACE, tenancyUiResources } from '@speed/tenancy-ui'
import type { SpeedAppServices } from '@speed/product-shell/bootstrap'
import { AppThemeProvider, UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import { AppServicesProvider } from '../app-services.js'
import { DemoApp } from '../app.js'
import { DEMO_NAMESPACE, demoResources } from '../resources.js'
import { createTestSession } from './session.js'

export interface RenderDemoAppOptions {
  /** The instance's starting language; defaults to en-US. */
  readonly language?: string
  /**
   * The session the trees read, attached to the auth hooks. A fresh anonymous
   * session by default; a suite that wants the authenticated frame (or a
   * session whose state it drives) passes its own.
   */
  readonly session?: AuthSession
  /**
   * The client handed to the app services. Defaults to the inert harness
   * client; a suite that observes a client seam (the event-stream opener, for
   * example) passes its own. It still must not answer requests unless the test
   * scripts it.
   */
  readonly api?: SpeedClient
}

/**
 * The render harness's stand-in client: it satisfies SpeedClient -- the
 * request function plus the event-stream opener the notification
 * subscription reads -- but throws if a request is actually issued, since
 * a demo-app render must not reach the network. The opener is inert (it
 * returns a handle whose close is a no-op), so a mounted
 * NotificationStreamProvider opens nothing.
 */
export function createHarnessClient(): SpeedClient {
  const request = (async () => {
    throw new Error('the render harness client must not be called')
  }) as RequestFn
  return Object.assign(request, {
    openEventStream: () => ({ close: () => undefined }),
  })
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
  registerNamespace(i18n, ACCOUNT_UI_NAMESPACE, accountUiResources)
  registerNamespace(i18n, TENANCY_UI_NAMESPACE, tenancyUiResources)
  registerNamespace(i18n, NOTIFICATION_UI_NAMESPACE, notificationUiResources)
  registerNamespace(i18n, DEMO_NAMESPACE, demoResources)

  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const api = options.api ?? createHarnessClient()
  const session = options.session ?? createTestSession()
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
