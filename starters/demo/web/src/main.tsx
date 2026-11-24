import { ACCOUNT_UI_NAMESPACE, accountUiResources } from '@speed/account-ui'
import { ADMIN_UI_NAMESPACE, adminUiResources } from '@speed/admin-ui'
import {
  ORG_CREATE_UI_NAMESPACE,
  orgCreateUiResources,
} from '@speed/org-create-ui'
import {
  NOTIFICATION_UI_NAMESPACE,
  notificationUiResources,
} from '@speed/notification-ui'
import {
  bootstrapSpeedApp,
  type SpeedAppBootstrap,
  type SpeedAppThemeOptions,
} from '@speed/product-shell/bootstrap'
import { TENANCY_UI_NAMESPACE, tenancyUiResources } from '@speed/tenancy-ui'
import { themePresets } from '@speed/theme-presets'
import { AppServicesProvider } from './app-services.js'
import { DemoApp } from './app.js'
import {
  getCurrentTenantName,
  initialColorModePreference,
  initialThemePreset,
} from './chrome.js'
import { applyDocumentMetadata } from './document-metadata.js'
import { PlatformPermissions } from './platform-permissions.js'
import { DEMO_NAMESPACE, demoResources } from './resources.js'
import { loadRuntimeConfig } from './runtime-config.js'
import { tenantAddressedFetch } from './tenant-addressed-fetch.js'

export type DemoAppBootstrap = SpeedAppBootstrap

export function demoThemeOptions(themePreset?: string): SpeedAppThemeOptions {
  return {
    defaultMode: initialColorModePreference(),
    projectTokens: {},
    presets: themePresets,
    defaultPreset: initialThemePreset(themePreset),
  }
}

export async function bootstrapDemoApp(
  container: Element,
): Promise<DemoAppBootstrap> {
  const runtime = await loadRuntimeConfig()
  const theme = demoThemeOptions(runtime.themePreset)
  const bootstrap = bootstrapSpeedApp(container, {
    namespaces: [
      { namespace: ADMIN_UI_NAMESPACE, resources: adminUiResources },
      { namespace: ORG_CREATE_UI_NAMESPACE, resources: orgCreateUiResources },
      { namespace: ACCOUNT_UI_NAMESPACE, resources: accountUiResources },
      { namespace: TENANCY_UI_NAMESPACE, resources: tenancyUiResources },
      {
        namespace: NOTIFICATION_UI_NAMESPACE,
        resources: notificationUiResources,
      },
      { namespace: DEMO_NAMESPACE, resources: demoResources },
    ],
    providers: [
      (services, children) => (
        <AppServicesProvider services={services}>
          <PlatformPermissions session={services.session}>
            {children}
          </PlatformPermissions>
        </AppServicesProvider>
      ),
    ],
    view: <DemoApp shellLayout={runtime.shellLayout} />,
    theme,
    baseUrl: runtime.apiOrigin,
    client: {
      fetch: tenantAddressedFetch,
    },
    tenantProvider: getCurrentTenantName,
  })
  applyDocumentMetadata(bootstrap.i18n)
  return bootstrap
}
