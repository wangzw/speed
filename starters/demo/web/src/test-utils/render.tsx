/**
 * The demo host's render harness: the app's production provider stack and
 * instance, with a scripted session and client in place of the assembly's.
 *
 * It mounts DemoApp -- the routing composition and the shell switch -- under
 * the same two constructions production renders: `createSpeedI18n` (the
 * framework namespaces, the platform error bundle and the app's own bundles,
 * registered through the one call that does it) and `SpeedAppProviders` (the
 * provider stack from StrictMode down to the query client). A bench that
 * rebuilt either would let a suite pass on a tree the app never renders.
 *
 * What the bench adds is the test seam the assembly fills at runtime: the
 * app-services provider, carrying a session attached to the auth hooks and a
 * client that must not be called. The session defaults to the test session
 * (test-utils/session) -- an anonymous snapshot whose discovery reads
 * resolve, so the sign-in surface renders without a network -- and a suite
 * that wants the authenticated frame (or one whose state it drives) passes
 * its own. The client defaults to the inert harness client; a suite that
 * observes a client seam passes its own.
 */

import { render, type RenderResult } from '@testing-library/react'
import type { ReactElement, ReactNode } from 'react'
import type { RequestFn, SpeedClient } from '@speed/api-client'
import { attachSession } from '@speed/auth-core'
import type { AuthSession } from '@speed/auth-core'
import { ACCOUNT_UI_NAMESPACE, accountUiResources } from '@speed/account-ui'
import { ADMIN_UI_NAMESPACE, adminUiResources } from '@speed/admin-ui'
import type { I18nInstance } from '@speed/i18n'
import {
  NOTIFICATION_UI_NAMESPACE,
  notificationUiResources,
} from '@speed/notification-ui'
import {
  createQueryClient,
  createSpeedI18n,
  SpeedAppProviders,
} from '@speed/product-shell/bootstrap'
import type {
  SpeedAppServices,
  SpeedAppThemeOptions,
} from '@speed/product-shell/bootstrap'
import type { AppShellShellLayout } from '@speed/layout-kit'
import { TENANCY_UI_NAMESPACE, tenancyUiResources } from '@speed/tenancy-ui'
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
  /**
   * Wraps the app inside the provider tree, for a suite that mounts one of
   * the production assembly's own providers around it (PlatformPermissions,
   * for example). The wrapper renders inside the query client and the app
   * services, so it reads the same session and client the app does.
   */
  readonly wrap?: (app: ReactElement) => ReactNode
  /**
   * The shell layout the app is composed with, as the deployment config
   * supplies it (src/main.tsx). Absent leaves the frames' top-bar default.
   */
  readonly shellLayout?: AppShellShellLayout
  /**
   * The theme options the provider stack renders with, forwarded to
   * SpeedAppProviders. The production assembly passes the preset shelf and
   * the boot preset defaults (src/main.tsx); a suite that asserts themed
   * behaviour passes the same shape.
   */
  readonly theme?: SpeedAppThemeOptions
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

/** The app bundles the production assembly registers beyond the framework
 * four (src/main.tsx names the same list). */
const APP_NAMESPACES = [
  { namespace: ADMIN_UI_NAMESPACE, resources: adminUiResources },
  { namespace: ACCOUNT_UI_NAMESPACE, resources: accountUiResources },
  { namespace: TENANCY_UI_NAMESPACE, resources: tenancyUiResources },
  { namespace: NOTIFICATION_UI_NAMESPACE, resources: notificationUiResources },
  { namespace: DEMO_NAMESPACE, resources: demoResources },
] as const

export interface RenderDemoAppResult extends RenderResult {
  /** The instance the tree renders with: the production construction, in the
   * requested language. */
  readonly i18n: I18nInstance
  /** The query client the tree renders with: the assembly's own client. */
  readonly queryClient: ReturnType<typeof createQueryClient>
}

/** Renders DemoApp under the app's provider tree. */
export function renderDemoApp(
  options: RenderDemoAppOptions = {},
): RenderDemoAppResult {
  const i18n = createSpeedI18n({
    supportedLanguages: ['zh-CN', 'en-US'],
    defaultLanguage: options.language ?? 'en-US',
    storage: null,
    urlParameterName: null,
    navigatorLanguages: [],
    namespaces: APP_NAMESPACES,
  })

  const queryClient = createQueryClient()
  const api = options.api ?? createHarnessClient()
  const session = options.session ?? createTestSession()
  attachSession(session)
  const services: SpeedAppServices = { session, api, queryClient }
  const app = <DemoApp shellLayout={options.shellLayout} />

  const result = render(
    <SpeedAppProviders
      i18n={i18n}
      queryClient={queryClient}
      theme={options.theme}
    >
      <AppServicesProvider services={services}>
        {options.wrap === undefined ? app : options.wrap(app)}
      </AppServicesProvider>
    </SpeedAppProviders>,
  )
  return { ...result, i18n, queryClient }
}
