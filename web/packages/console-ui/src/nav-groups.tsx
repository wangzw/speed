/**
 * The console's navigation group table -- the order and headings the
 * console's sidebar groups its pages into.
 *
 * With eleven labelled pages the flat single column no longer reads, so
 * the console declares which capability domain each page belongs to (the
 * `group` field on its declaration) and this table gives the groups their
 * order and translated headings. A host passes it to ConsoleShell's
 * `navGroups` prop -- directly or, as the demo does, through the area
 * that mounts the shell -- to render the grouped sidebar; a host that
 * passes nothing keeps the flat column, so this table is opt-in.
 *
 * The headings are React elements that resolve through the console-ui
 * namespace (ConsoleNavLabel), so the module-level table stays a plain
 * value while its headings translate reactively, exactly like the page
 * declarations.
 */

import { ConsoleNavLabel } from './internal/nav-label.js'
import type { ConsoleNavGroup } from './route.js'

/**
 * The console sidebar's groups in display order: the overview, identity
 * and access, tenant management, platform configuration, and audit.
 */
export const consoleNavGroups: readonly ConsoleNavGroup[] = [
  {
    id: 'overview',
    label: <ConsoleNavLabel labelKey="nav.groups.overview" />,
  },
  {
    id: 'identity',
    label: <ConsoleNavLabel labelKey="nav.groups.identity" />,
  },
  {
    id: 'tenants',
    label: <ConsoleNavLabel labelKey="nav.groups.tenants" />,
  },
  {
    id: 'platform',
    label: <ConsoleNavLabel labelKey="nav.groups.platform" />,
  },
  {
    id: 'audit',
    label: <ConsoleNavLabel labelKey="nav.groups.audit" />,
  },
] satisfies readonly ConsoleNavGroup[]
