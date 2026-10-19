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
 *    console-ui area's and the app's own bundle.
 *
 *  - The services provider that hands the assembled session and client down to
 *    the views (src/app-services.tsx).
 *
 *  - The view root, DemoApp, which installs the router and switches the shell
 *    by area (src/app.tsx).
 *
 *  - The API origin: read from the deployment's runtime-config.json beside the
 *    entry document (src/runtime-config.ts), falling back to the page's own
 *    origin when the file is absent. Reading it before the assembly keeps one
 *    dist usable across deployment topologies without a rebuild.
 */

import { ACCOUNT_UI_NAMESPACE, accountUiResources } from '@speed/account-ui'
import { CONSOLE_UI_NAMESPACE, consoleUiResources } from '@speed/console-ui'
import {
  OBJECTSTORE_UI_NAMESPACE,
  objectstoreUiResources,
} from '@speed/objectstore-ui'
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
import { AppServicesProvider } from './app-services.js'
import { DemoApp } from './app.js'
import { getCurrentTenantAlias, initialColorMode } from './chrome.js'
import { applyDocumentMetadata } from './document-metadata.js'
import { PlatformPermissions } from './platform-permissions.js'
import { DEMO_NAMESPACE, demoResources } from './resources.js'
import { loadRuntimeConfig } from './runtime-config.js'

/** What a page's bootstrap produced, for hosts and harnesses that act on it. */
export type DemoAppBootstrap = SpeedAppBootstrap

/**
 * The theme options the bootstrap hands the assembly. The tree starts in the
 * device's remembered colour mode, else the operating system's preference,
 * else light (src/chrome.tsx reads it before the first render, so a reload
 * renders in the mode the user left). The project-layer tokens are the brand
 * placeholder: an empty override keeps the platform's shipped palette, and a
 * product replaces it with its own (@speed/ui-kit's README, "The theme
 * factory").
 */
export function demoThemeOptions(): SpeedAppThemeOptions {
  return {
    defaultMode: initialColorMode(),
    projectTokens: {},
  }
}

/**
 * Builds the whole app composition into the given container. Calling it twice
 * into one container double-mounts; a page bootstraps exactly once.
 */
export async function bootstrapDemoApp(
  container: Element,
): Promise<DemoAppBootstrap> {
  const { apiOrigin } = await loadRuntimeConfig()
  const theme = demoThemeOptions()
  const bootstrap = bootstrapSpeedApp(container, {
    namespaces: [
      { namespace: CONSOLE_UI_NAMESPACE, resources: consoleUiResources },
      { namespace: ACCOUNT_UI_NAMESPACE, resources: accountUiResources },
      {
        namespace: OBJECTSTORE_UI_NAMESPACE,
        resources: objectstoreUiResources,
      },
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
              the shell can gate the console on it (src/app.tsx). */}
          <PlatformPermissions session={services.session}>
            {children}
          </PlatformPermissions>
        </AppServicesProvider>
      ),
    ],
    view: <DemoApp />,
    theme,
    baseUrl: apiOrigin,
    // The tenant every request announces as X-Tenant: the host's current
    // tenant's ALIAS, read per attempt (src/chrome.tsx holds the one value
    // and resolves its alias). The server's tenant resolution layer looks the
    // name up in the roster's alias column, so the call must carry the alias
    // -- not the tenant id, which a rename leaves behind -- and a
    // tenant-scoped call without it is refused 404.
    tenantProvider: getCurrentTenantAlias,
  })
  // The entry document's title follows the language the assembly resolved,
  // so the tab names the brand in the interface's own language. `<html lang>`
  // is the assembly's: it binds the document language at boot and follows
  // every switch (SpeedAppDefinition.documentLanguage).
  applyDocumentMetadata(bootstrap.i18n)
  return bootstrap
}
