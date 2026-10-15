/**
 * Public entry of @speed/console-ui.
 *
 * The platform-staff console component family -- the second shell. It
 * renders the three backend administration surfaces (the tenant roster
 * over /tenant/admin, the settings read-write surface over
 * /settings/admin, and the audit query over /audit/admin) as one console
 * frame with three pages. Those endpoints do not resolve a tenant, so the
 * console has no tenant switcher and no tenant-scoped query key: it is
 * not, and cannot be, a page of @speed/tenancy-ui or @speed/product-shell.
 *
 * The console reuses the declarative registration seam the tenant-facing
 * shell uses. Each administration capability contributes one page
 * declaration -- a path pattern, a view, an optional navigation label and
 * the permission code the page's data is held to (tenantAdminRoutes,
 * settingsAdminRoutes, auditAdminRoutes, or the whole set as
 * consoleRoutes) -- and the application's composition layer collects the
 * declarations and renders them through the route layer it chose. This
 * package imports no router and does no path matching: ConsoleShell
 * carries the platform frame (a banner, a navigation derived from the
 * labelled declarations, and a `main` landmark) and renders the host's
 * routed content.
 *
 * The pages read and write through the console operations bundle the host
 * mounts in ConsoleOperationsProvider (useConsoleOperations): a plain
 * object of react-query read hooks and write mutations. The package ships
 * that bundle -- `consoleOperations`, bound to the generated @speed/api-sdk
 * surface -- so a host mounts it unchanged and no page builds a
 * QueryClient or writes a query key. The bundle is the seam through which
 * the /tenant/admin, /settings/admin and /audit/admin operations reach
 * the pages; see operations.tsx for its members and their generated
 * bindings.
 *
 * Every built-in string renders from the bilingual console-ui namespace
 * (CONSOLE_UI_NAMESPACE + consoleUiResources, which the host registers
 * alongside ui-kit's); the dialogs and empty/error states render through
 * ui-kit's shared primitives. Helpers shared between the components live
 * in src/internal/ and are deliberately not exported.
 */

export { CONSOLE_UI_NAMESPACE, consoleUiResources } from './resources.js'
export type {
  ConsoleRoute,
  ConsoleRouteContext,
  ConsoleRouteParams,
  ConsoleRouteTable,
} from './route.js'
export { ConsoleShell, type ConsoleShellProps } from './ConsoleShell.js'
export {
  ConsoleOperationsProvider,
  consoleOperations,
  useConsoleOperations,
  settingKeyRest,
  type ConsoleOperations,
  type ConsoleOperationsProviderProps,
  type ConsoleTenant,
  type ConsoleTenantStatus,
  type ConsoleSetting,
  type ConsoleAuditRecord,
  type ConsoleCreateTenantInput,
  type ConsoleSetTenantAliasInput,
  type ConsoleSetTenantStatusInput,
  type ConsoleSetSettingInput,
  type ConsoleRemoveSettingInput,
} from './operations.js'
export {
  tenantAdminRoutes,
  settingsAdminRoutes,
  auditAdminRoutes,
  consoleRoutes,
} from './routes.js'
export { TenantRosterPage } from './TenantRosterPage.js'
export { SettingsPage } from './SettingsPage.js'
export { AuditPage } from './AuditPage.js'
