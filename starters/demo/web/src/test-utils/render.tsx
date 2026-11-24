import { render, type RenderResult } from '@testing-library/react'
import type { ReactElement, ReactNode } from 'react'
import type { RequestFn, SpeedClient } from '@speed/api-client'
import { attachSession } from '@speed/auth-core'
import type { AuthSession } from '@speed/auth-core'
import { ACCOUNT_UI_NAMESPACE, accountUiResources } from '@speed/account-ui'
import { ADMIN_UI_NAMESPACE, adminUiResources } from '@speed/admin-ui'
import {
  ORG_CREATE_UI_NAMESPACE,
  orgCreateUiResources,
} from '@speed/org-create-ui'
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
  readonly language?: string
  readonly session?: AuthSession
  readonly api?: SpeedClient
  readonly wrap?: (app: ReactElement) => ReactNode
  readonly shellLayout?: AppShellShellLayout
  readonly theme?: SpeedAppThemeOptions
}

export function createHarnessClient(): SpeedClient {
  const request = (async () => {
    throw new Error('the render harness client must not be called')
  }) as RequestFn
  return Object.assign(request, {
    openEventStream: () => ({ close: () => undefined }),
  })
}

const APP_NAMESPACES = [
  { namespace: ADMIN_UI_NAMESPACE, resources: adminUiResources },
  { namespace: ORG_CREATE_UI_NAMESPACE, resources: orgCreateUiResources },
  { namespace: ACCOUNT_UI_NAMESPACE, resources: accountUiResources },
  { namespace: TENANCY_UI_NAMESPACE, resources: tenancyUiResources },
  { namespace: NOTIFICATION_UI_NAMESPACE, resources: notificationUiResources },
  { namespace: DEMO_NAMESPACE, resources: demoResources },
] as const

export interface RenderDemoAppResult extends RenderResult {
  readonly i18n: I18nInstance
  readonly queryClient: ReturnType<typeof createQueryClient>
}

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
