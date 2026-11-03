/**
 * The console's navigation group table -- the order and headings the
 * console's sidebar groups its pages into.
 *
 * With eleven labelled pages the flat single column no longer reads, so
 * the console declares which capability domain each page belongs to (the
 * `group` field on its declaration) and this table gives the groups their
 * order and translated headings. A host passes it to AdminShell's
 * `navGroups` prop -- directly or, as the demo does, through the area
 * that mounts the shell -- to render the grouped sidebar; a host that
 * passes nothing keeps the flat column, so this table is opt-in.
 *
 * The headings are React elements that resolve through the admin-ui
 * namespace (AdminNavLabel), so the module-level table stays a plain
 * value while its headings translate reactively, exactly like the page
 * declarations.
 */

import { AdminNavLabel } from './internal/nav-label.js'
import {
  AccountsIcon,
  AuditIcon,
  DashboardIcon,
  SettingsIcon,
  TenantsIcon,
} from './internal/icons.js'
import type { AdminNavGroup } from './route.js'

/**
 * The console sidebar's groups in display order: the overview, identity
 * and access, tenant management, platform configuration, and audit. Each
 * group carries the decorative icon its collapsed heading shows in the
 * sidebar's icon rail; the page entries beneath it carry their own.
 */
export const adminNavGroups: readonly AdminNavGroup[] = [
  {
    id: 'overview',
    label: <AdminNavLabel labelKey="nav.groups.overview" />,
    icon: <DashboardIcon />,
  },
  {
    id: 'identity',
    label: <AdminNavLabel labelKey="nav.groups.identity" />,
    icon: <AccountsIcon />,
  },
  {
    id: 'tenants',
    label: <AdminNavLabel labelKey="nav.groups.tenants" />,
    icon: <TenantsIcon />,
  },
  {
    id: 'platform',
    label: <AdminNavLabel labelKey="nav.groups.platform" />,
    icon: <SettingsIcon />,
  },
  {
    id: 'audit',
    label: <AdminNavLabel labelKey="nav.groups.audit" />,
    icon: <AuditIcon />,
  },
] satisfies readonly AdminNavGroup[]
