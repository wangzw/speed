/**
 * The demo application's bootstrap: the app's declarative definition handed to
 * the platform's app-entry assembly (@speed/product-shell/bootstrap), the one
 * call a delivered app's main module makes.
 *
 * The module exports a bootstrap function and runs no module-scope side
 * effects: importing it does nothing, so suites and harnesses can import it
 * safely, and the whole composition runs once per page load, wherever the host
 * mounts it. Everything the assembly wires -- the bilingual i18n instance, the
 * session over the generated authn operations, the one client bound into the
 * @speed/api-sdk runtime seam, the query client and the session-end strategy,
 * the provider stack -- is the package's, documented there.
 *
 * What remains here is the app's variation:
 *
 *  - The namespaces its views render beyond the family's automatic four: the
 *    admin-ui area's and the app's own bundle.
 *
 *  - The services provider that hands the assembled session and client down to
 *    the views (src/app-services.tsx).
 *
 *  - The view root, DemoApp, which installs the router and switches the shell
 *    by area (src/app.tsx).
 *
 *  - The deployment configuration: the API origin, the shell layout and the
 *    default theme preset, read from the deployment's runtime-config.json
 *    beside the entry document (src/runtime-config.ts), falling back to the
 *    page's own origin, the top bar and 'default' when a field is absent.
 */

import { ACCOUNT_UI_NAMESPACE, accountUiResources } from '@speed/account-ui'
import { ADMIN_UI_NAMESPACE, adminUiResources } from '@speed/admin-ui'
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
  getCurrentTenantAlias,
  initialColorModePreference,
  initialThemePreset,
} from './chrome.js'
import { applyDocumentMetadata } from './document-metadata.js'
import { PlatformPermissions } from './platform-permissions.js'
import { DEMO_NAMESPACE, demoResources } from './resources.js'
import { loadRuntimeConfig } from './runtime-config.js'
import { tenantAddressedFetch } from './tenant-addressed-fetch.js'

/** What a page's bootstrap produced, for hosts and harnesses that act on it. */
export type DemoAppBootstrap = SpeedAppBootstrap

/**
 * The theme options the bootstrap hands the assembly. The tree starts in the
 * device's remembered colour-mode preference, else 'system' (src/chrome.tsx
 * reads it before the first render, so a reload renders in the preference
 * the user left and an unset device follows the operating system). The
 * preset shelf is the shipped one,
 * starting on the device's remembered preset, else the deployment's, else
 * 'default'; the project-layer tokens are the brand placeholder: an empty
 * override keeps the platform's shipped palette, and a product replaces it
 * with its own (@speed/ui-kit's README, "The theme factory").
 */
export function demoThemeOptions(themePreset?: string): SpeedAppThemeOptions {
  return {
    defaultMode: initialColorModePreference(),
    projectTokens: {},
    presets: themePresets,
    defaultPreset: initialThemePreset(themePreset),
  }
}

/**
 * Builds the whole app composition into the given container. Calling it twice
 * into one container double-mounts; a page bootstraps exactly once.
 */
export async function bootstrapDemoApp(
  container: Element,
): Promise<DemoAppBootstrap> {
  const runtime = await loadRuntimeConfig()
  const theme = demoThemeOptions(runtime.themePreset)
  const bootstrap = bootstrapSpeedApp(container, {
    namespaces: [
      { namespace: ADMIN_UI_NAMESPACE, resources: adminUiResources },
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
          {/* Attaches the caller's platform permission set once signed in, so
              the shell can gate the admin on it (src/app.tsx). */}
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
      // A tenant-addressed path names the tenant the request acts on, and
      // the platform admin's tenant selector is a request argument rather
      // than the host's current tenant (src/tenant-addressed-fetch.ts).
      fetch: tenantAddressedFetch,
    },
    // The tenant every request announces as X-Tenant, except one whose path
    // is tenant-addressed (that request's header is rewritten to the path's
    // tenant identifier by `client.fetch` above): the host's current tenant's
    // ALIAS, read per attempt (src/chrome.tsx holds the one value and
    // resolves its alias). The server's tenant resolution layer looks the
    // name up as an alias first, then as a tenant identifier; a tenant-scoped
    // call that resolves to neither is refused 404.
    tenantProvider: getCurrentTenantAlias,
  })
  // The entry document's title follows the language the assembly resolved,
  // so the tab names the brand in the interface's own language. `<html lang>`
  // is the assembly's: it binds the document language at boot and follows
  // every switch (SpeedAppDefinition.documentLanguage).
  applyDocumentMetadata(bootstrap.i18n)
  return bootstrap
}
