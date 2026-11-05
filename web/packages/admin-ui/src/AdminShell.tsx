/**
 * AdminShell: the platform-staff console frame -- the second shell.
 *
 * It is the platform-side sibling of @speed/product-shell, and the
 * difference is the whole point of the package: the administration
 * surfaces it renders are mounted on endpoints that do not resolve a
 * tenant, so the console has no tenant switcher and no tenant-scoped
 * query key. It carries the platform navigation derived from the page
 * declarations it is given, and renders the host's routed content. It
 * does no routing: it never matches a URL, never builds a link from a
 * path and never decides selection -- the declarations are collected and
 * rendered by the application's route layer, and the console's
 * navigation entries are links to each labelled declaration's path. The
 * entries stay real links (so a middle-click or a copied URL reaches the
 * path), but when the host supplies `onNavigate` a plain click is handed
 * to it instead of reloading the page, which is how a host routes an
 * entry in place without this package importing a router. A click with a
 * modifier, a non-primary button, a link that asks for a new tab and an
 * activation another handler already claimed all keep the browser's own
 * behavior (layout-kit's `isPlainPrimaryClick`).
 *
 * The frame is @speed/layout-kit's AppShell, the same frame the
 * tenant-facing shell builds on: a fixed `header` banner carrying the
 * console title and any host header actions, a responsive `nav` drawer
 * over the platform navigation (a permanent drawer at md and up, a
 * temporary overlay drawer with a toggle below it), and a `main`
 * landmark holding the host content. Reusing AppShell is what keeps the
 * two shells one shape -- a sidebar that stays put while the content
 * scrolls, a hamburger drawer on a narrow viewport, and the content
 * region as the single scroll layer -- so this package derives the
 * navigation and hands it to the shared frame rather than re-implementing
 * the chrome. The console names its own navigation landmark
 * (`shell.navLabel`, unless the host supplies another name) through the
 * frame's `navLabel` so the platform navigation stays distinguishable
 * from any other shell's. Beside the navigation it passes the frame's
 * desktop sidebar-collapse pair (`sidebarCollapsed` /
 * `onSidebarCollapsedChange`) straight through, so a host that holds the
 * collapse state gives the operator the same host-controlled collapse in
 * the console area that the tenant shell carries. The frame's other
 * slots -- `userMenu`, `banner`, the controlled mobile-drawer pair,
 * `sidebarWidth`, `navHeader` and `navFooter` -- are passed through the
 * same way: the console adds no chrome of its own to them.
 *
 * The navigation is flat by default -- one entry per labelled declaration
 * -- but a host may pass `navGroups` (the console's own group table,
 * `adminNavGroups`) to group the pages by capability domain: the shell
 * folds each declaration into the group its `group` field names, groups
 * in table order and entries in declaration order, giving the frame's
 * `AppShell` the grouped navigation (`AppShellNavGroup`). A group left
 * with no entry is dropped, and one holding a single entry renders that
 * entry alone, at the group's position, rather than a heading over one
 * link. Without the table the flat column is unchanged.
 *
 * Selection is the host's too: the shell does not know which path is
 * current, so a host that wants the current entry marked supplies
 * `isSelected(path)`, which each entry's `selected` flag is read from.
 * Absent, no entry is selected -- the shell's long-standing behavior.
 *
 * The shell reads no data and holds no operations: the pages it hosts
 * reach their reads and writes through the operations bundle the host
 * mounted (see operations.tsx), and the permissions the declarations
 * carry are data the host reads to compose its own route guard. The
 * shell's package-level seams live in AdminProvider, which this
 * component mounts around its content; a host that needs those seams
 * without this frame mounts the provider itself.
 */

import type { ReactNode } from 'react'
import { useMemo } from 'react'
import Box from '@mui/material/Box'
import type { SxProps, Theme } from '@mui/material/styles'
import type { AuthSession } from '@speed/auth-core'
import type { FeatureName } from '@speed/api-sdk/features'
import { AppShell, isPlainPrimaryClick } from '@speed/layout-kit'
import type {
  AppShellNavEntry,
  AppShellNavItem,
  AppShellNavSlot,
  AppShellProps,
} from '@speed/layout-kit'
import type { AdminNavGroup, AdminRoute } from './route.js'
import type { AdminImpersonationChange } from './internal/impersonation-change.js'
import type { AdminImpersonationStarting } from './internal/impersonation-starting.js'
import { AdminProvider } from './AdminProvider.js'
import { useAdminUiTranslation } from './internal/translation.js'

/** The AppShell chrome the console opts into beside its own, re-declared
 * by Pick so a chrome change lands in AppShellProps only (single source of
 * truth). The collapse pair gives the console frame the same
 * host-controlled collapse the tenant shell has; the remaining slots are
 * the frame's own host-content slots, passed through untouched. */
type AppShellPassthroughProps = Pick<
  AppShellProps,
  | 'sidebarCollapsed'
  | 'onSidebarCollapsedChange'
  | 'sidebarWidth'
  | 'shellLayout'
  | 'userMenu'
  | 'banner'
  | 'mobileOpen'
  | 'onMobileOpenChange'
  | 'navHeader'
  | 'navFooter'
>

export interface AdminShellProps extends AppShellPassthroughProps {
  /**
   * The console's page declarations. The shell derives the platform
   * navigation from the declarations that carry a `label` and renders
   * nothing else of them: path matching and view selection are the
   * route layer's, not the shell's. The table is also published to the
   * routed content unchanged, so a page can resolve a sibling declaration
   * by its stable `id`.
   */
  readonly routes: readonly AdminRoute[]
  /**
   * The console's navigation group table: the order and headings the
   * sidebar groups the labelled pages into, each declaration naming its
   * group through its `group` field. Supplying it renders a grouped
   * sidebar; omitting it keeps the flat single column the console has
   * always shown, so an existing host is unchanged. A declaration whose
   * `group` is absent, or names no group here, renders ungrouped (above
   * the groups).
   */
  readonly navGroups?: readonly AdminNavGroup[]
  /**
   * The host's synchronous feature answer: whether the process serves
   * the named feature. Required; the shell publishes it to the routed
   * pages through the package's feature context, where a feature-gated
   * control asks it. The shell itself never filters `routes` by it --
   * the host hands over the declarations its presence predicate left in
   * force.
   */
  readonly hasFeature: (name: FeatureName) => boolean
  /** The host's routed content, rendered inside the `main` landmark. */
  readonly children: ReactNode
  /** The console title, rendered in the banner (host content). */
  readonly title?: ReactNode
  /** Banner content after the title, typically the account menu (host
   * content). Takes the shell's slot form, so a control too wide for the
   * collapsed sidebar rail can render its icon form there. */
  readonly headerActions?: AppShellNavSlot
  /**
   * The navigation landmark's accessible name. Absent, the console names
   * it from its own namespace (`shell.navLabel`), keeping the platform
   * navigation distinguishable from any other shell's.
   */
  readonly navLabel?: string
  /**
   * Called with a navigation entry's path when that entry is activated by
   * a plain click. Supplying it routes the entry in place through the
   * host's router: the shell still renders the entry as a link (a
   * middle-click or a copied URL stays a real navigation), but a plain
   * click is intercepted and handed here instead of reloading the page.
   * A click with a modifier, a non-primary button or one the entry's own
   * target reserves for the browser is left alone. Absent, an entry is a
   * plain link that navigates the browser -- a full page load, which a
   * host whose session lives in memory loses.
   */
  readonly onNavigate?: (href: string) => void
  /**
   * Whether a navigation entry's path is the current one, computed by the
   * host (the shell does no path matching). Each entry's `selected` flag
   * is read from it. Absent, no entry is selected.
   */
  readonly isSelected?: (path: string) => boolean
  /**
   * Builds the acceptance link for an issued invitation's token -- a
   * host-relative path or an absolute URL. The console's invitation page
   * shows it after a successful issue so the operator can hand the invitee
   * a working link, which needs a path only the host knows (the landing
   * route). Absent, the page shows the bare token instead.
   */
  readonly invitationLink?: (token: string) => string
  /**
   * The host's account settings page (a host-relative path or an absolute
   * URL), where an operator registers two-step verification. The step-up
   * dialog renders it as a link when the caller's account has no active
   * factor; absent, the dialog renders text only. The path is the host's
   * route, so this package never holds one.
   */
  readonly accountHref?: string
  /**
   * The host's current path, sent as the step-up flow's `return_to`: a
   * redirect method's round trip lands back on the page that started it, so
   * that page reads a refusal's problem code from the URL fragment. Absent,
   * the deployment's default return target applies.
   */
  readonly returnPath?: string
  /**
   * The assembled session, published to the routed pages so a
   * step-up-gated write can raise it. Absent, a page's step-up-gated
   * writes fire unguarded and a refused one renders its error code.
   */
  readonly session?: AuthSession
  /**
   * The host's action after an impersonation start or end settles: the
   * host evicts the cache rows taken under the old identity, re-reads the
   * session and the identity-dependent host state, and moves the page.
   * The impersonation page invokes it once after each settled write,
   * outside the attempt, does not await it, and wraps its rejection --
   * the settled write is not overturned. Absent, the page refreshes only
   * its own status and record reads.
   */
  readonly onImpersonationChange?: (
    change: AdminImpersonationChange,
  ) => void | Promise<void>
  /**
   * The host's hook fired synchronously before an impersonation start is
   * written, so a host frame that speaks about the operator can enter its
   * "starting" state before the request lands. The returned release runs
   * when the write is refused, blocked by step-up, or throws; a write
   * that lands is the host's to release after its
   * `onImpersonationChange('started')` action settles.
   */
  readonly onImpersonationStarting?: AdminImpersonationStarting
  /** Extra styling merged onto the root layout box. */
  readonly sx?: SxProps<Theme>
}

/** One plain navigation entry from a labelled declaration. */
function navItemFor(
  route: AdminRoute,
  onNavigate: ((href: string) => void) | undefined,
  isSelected: ((path: string) => boolean) | undefined,
): AppShellNavItem {
  return {
    id: route.id ?? route.path,
    href: route.path,
    label: route.label,
    icon: route.icon,
    selected: isSelected?.(route.path),
    onClick:
      onNavigate === undefined
        ? undefined
        : (event) => {
            // Only a plain primary click is the host's to route in place.
            // A modifier or middle click, a link asking for a new tab and
            // an activation another handler claimed keep the browser's
            // own behavior -- and the href is always there for them.
            if (!isPlainPrimaryClick(event)) {
              return
            }
            event.preventDefault()
            onNavigate(route.path)
          },
  }
}

/**
 * The console's navigation entries. With no group table they are the
 * labelled declarations as plain entries, in declaration order -- the
 * flat single column. With one, those declarations gather into their
 * groups: groups in table order, each group's entries in declaration
 * order, and a declaration that names no group (or one the table omits)
 * stays a plain entry above the groups. A group with no matching
 * declaration is dropped, and a group holding exactly one entry becomes
 * that entry, at the position the group would have taken -- a heading
 * over a lone link is noise, and which groups hold one entry depends on
 * the features the deployment serves.
 */
function buildNavEntries(
  routes: readonly AdminRoute[],
  navGroups: readonly AdminNavGroup[] | undefined,
  onNavigate: ((href: string) => void) | undefined,
  isSelected: ((path: string) => boolean) | undefined,
): readonly AppShellNavEntry[] {
  const labelled = routes.filter((route) => route.label !== undefined)
  if (navGroups === undefined) {
    return labelled.map((route) => navItemFor(route, onNavigate, isSelected))
  }
  const groupIds = new Set(navGroups.map((group) => group.id))
  const ungrouped = labelled.filter(
    (route) => route.group === undefined || !groupIds.has(route.group),
  )
  const grouped: AppShellNavEntry[] = []
  for (const group of navGroups) {
    const items = labelled
      .filter((route) => route.group === group.id)
      .map((route) => navItemFor(route, onNavigate, isSelected))
    if (items.length === 0) {
      continue
    }
    if (items.length === 1) {
      const [item] = items
      if (item !== undefined) {
        grouped.push(item)
      }
      continue
    }
    grouped.push({
      id: group.id,
      label: group.label,
      icon: group.icon,
      defaultCollapsed: group.defaultCollapsed,
      items,
    })
  }
  return [
    ...ungrouped.map((route) => navItemFor(route, onNavigate, isSelected)),
    ...grouped,
  ]
}

export function AdminShell({
  routes,
  navGroups,
  hasFeature,
  children,
  title,
  headerActions,
  navLabel,
  onNavigate,
  isSelected,
  invitationLink,
  accountHref,
  returnPath,
  session,
  onImpersonationChange,
  onImpersonationStarting,
  userMenu,
  banner,
  mobileOpen,
  onMobileOpenChange,
  sidebarWidth,
  navHeader,
  navFooter,
  sidebarCollapsed,
  onSidebarCollapsedChange,
  shellLayout,
  sx,
}: AdminShellProps) {
  const { t } = useAdminUiTranslation()

  // The platform navigation is the labelled declarations: a declaration
  // without a label is a page that is reachable but not listed (a detail
  // route, say), exactly as route-kit's registration seam defines. When a
  // group table is supplied they gather into their groups (see
  // buildNavEntries); without one they stay the flat single column. Which
  // entry is selected is the host's answer (isSelected); this package does
  // no path matching. A plain click is intercepted for the host
  // (onNavigate) but the entry keeps its href, so it stays a real link.
  const navEntries = useMemo(
    () => buildNavEntries(routes, navGroups, onNavigate, isSelected),
    [routes, navGroups, onNavigate, isSelected],
  )

  return (
    <AppShell
      navItems={navEntries}
      navLabel={navLabel ?? t('shell.navLabel')}
      header={title}
      headerActions={headerActions}
      userMenu={userMenu}
      banner={banner}
      mobileOpen={mobileOpen}
      onMobileOpenChange={onMobileOpenChange}
      sidebarWidth={sidebarWidth}
      navHeader={navHeader}
      navFooter={navFooter}
      sidebarCollapsed={sidebarCollapsed}
      onSidebarCollapsedChange={onSidebarCollapsedChange}
      shellLayout={shellLayout}
      sx={sx}
    >
      {/* The package-level seams: navigation, route table, session,
          account destination, invitation link, impersonation change and
          start, and feature presence. The content area
          carries the same padding the tenant shell's own content region
          applies (theme spacing 3 = 24px all round), so a page's heading,
          breadcrumb, table and form sit clear of the sidebar and the
          banner rather than against them. */}
      <AdminProvider
        hasFeature={hasFeature}
        onNavigate={onNavigate}
        session={session}
        invitationLink={invitationLink}
        accountHref={accountHref}
        returnPath={returnPath}
        onImpersonationChange={onImpersonationChange}
        onImpersonationStarting={onImpersonationStarting}
        routes={routes}
      >
        {/* One inset on every side: the admin's content starts on the same
            line as the tenant area's pages, whose frame uses the same step. */}
        <Box sx={{ p: 3 }}>{children}</Box>
      </AdminProvider>
    </AppShell>
  )
}
