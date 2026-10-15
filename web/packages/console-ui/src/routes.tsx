/**
 * The console's page declarations -- the console surface's contribution
 * to the declarative registration seam.
 *
 * Each administration capability contributes one page declaration: a
 * path pattern, the page view and the permission code the page's data is
 * held to (the console shell derives the platform navigation from the
 * label each declaration carries). The declarations are collected by the
 * application's composition layer and handed to the route layer it chose;
 * this package imports no router and does no path matching, so a
 * declaration is data plus a view and nothing more.
 *
 * A declaration's label is a React element (ConsoleNavLabel) that
 * resolves its text through the console-ui namespace when it renders:
 * the declarations themselves stay plain module-level values -- the shape
 * the registration seam wants -- while their navigation labels still
 * translate reactively with the host's language.
 *
 * The permission codes are the modules' own: the roster's reads are held
 * to tenant.read and the settings reads to settings.read; the audit
 * query is held to audit.read. They are data the host reads to compose
 * its own route guard (this package never decides on them); a page whose
 * writes carry a stronger permission (tenant.manage, settings.write)
 * renders its write controls and lets the server refuse a caller who
 * lacks it, rather than hiding them -- the server is the authority.
 */

import { useConsoleUiTranslation } from './internal/translation.js'
import type { ConsoleRoute, ConsoleRouteTable } from './route.js'
import { TenantRosterPage } from './TenantRosterPage.js'
import { SettingsPage } from './SettingsPage.js'
import { AuditPage } from './AuditPage.js'

/**
 * A navigation label bound to a console-ui namespace key, resolved when
 * the label renders. Used inside the module-level declarations so they
 * stay plain values while their labels translate.
 */
function ConsoleNavLabel({ labelKey }: { readonly labelKey: string }) {
  const { t } = useConsoleUiTranslation()
  return <>{t(labelKey)}</>
}

/** The tenant-roster page's declaration, held to tenant.read. */
export const tenantAdminRoutes: ConsoleRouteTable = [
  {
    path: '/tenants',
    view: () => <TenantRosterPage />,
    label: <ConsoleNavLabel labelKey="tenants.title" />,
    permission: 'tenant.read',
  },
] satisfies readonly ConsoleRoute[]

/** The settings read-write page's declaration, held to settings.read. */
export const settingsAdminRoutes: ConsoleRouteTable = [
  {
    path: '/settings',
    view: () => <SettingsPage />,
    label: <ConsoleNavLabel labelKey="settings.title" />,
    permission: 'settings.read',
  },
] satisfies readonly ConsoleRoute[]

/** The audit-query page's declaration, held to audit.read. */
export const auditAdminRoutes: ConsoleRouteTable = [
  {
    path: '/audit',
    view: () => <AuditPage />,
    label: <ConsoleNavLabel labelKey="audit.title" />,
    permission: 'audit.read',
  },
] satisfies readonly ConsoleRoute[]

/**
 * The whole console page set: the three administration pages in the
 * order they appear in the platform navigation. A host composing the
 * console spreads this into its route table (alongside any pages it adds
 * itself), so the console's own pages are registered in one place.
 */
export const consoleRoutes: ConsoleRouteTable = [
  ...tenantAdminRoutes,
  ...settingsAdminRoutes,
  ...auditAdminRoutes,
]
