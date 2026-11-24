/** The console's navigation group table: each group's order, heading and icon. */

import { AdminNavLabel } from './internal/nav-label.js'
import {
  AccountsIcon,
  AuditIcon,
  DashboardIcon,
  SettingsIcon,
  TenantsIcon,
} from './internal/icons.js'
import type { AdminNavGroup } from './route.js'

/** The console sidebar's groups in display order, each with its collapse-rail icon. */
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
