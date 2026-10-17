/**
 * ConsoleShell: the platform-staff console frame -- the second shell.
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
 * entry in place without this package importing a router.
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
 * (`shell.navLabel`) through the frame's `navLabel` so the platform
 * navigation stays distinguishable from any other shell's. Beside the
 * navigation it passes the frame's desktop sidebar-collapse pair
 * (`sidebarCollapsed` / `onSidebarCollapsedChange`) straight through, so a
 * host that holds the collapse state gives the operator the same
 * host-controlled collapse in the console area that the tenant shell
 * carries.
 *
 * The navigation is flat by default -- one entry per labelled declaration
 * -- but a host may pass `navGroups` (the console's own group table,
 * `consoleNavGroups`) to group the pages by capability domain: the shell
 * folds each declaration into the group its `group` field names, groups
 * in table order and entries in declaration order, giving the frame's
 * `AppShell` the grouped navigation (`AppShellNavGroup`). Without the
 * table the flat column is unchanged.
 *
 * The shell reads no data and holds no operations: the pages it hosts
 * reach their reads and writes through the operations bundle the host
 * mounted (see operations.tsx), and the permissions the declarations
 * carry are data the host reads to compose its own route guard.
 */

import type { ReactNode } from 'react'
import { useMemo } from 'react'
import Box from '@mui/material/Box'
import type { SxProps, Theme } from '@mui/material/styles'
import type { AuthSession } from '@speed/auth-core'
import { AppShell } from '@speed/layout-kit'
import type {
  AppShellNavEntry,
  AppShellNavGroup,
  AppShellNavItem,
  AppShellProps,
} from '@speed/layout-kit'
import type { ConsoleNavGroup, ConsoleRoute } from './route.js'
import { ConsoleAccountHrefProvider } from './internal/account-href.js'
import { ConsoleNavigationProvider } from './internal/navigation.js'
import { ConsoleInvitationLinkProvider } from './internal/invitation-link.js'
import { ConsoleSessionProvider } from './internal/session.js'
import { useConsoleUiTranslation } from './internal/translation.js'

/** The AppShell chrome the console opts into beside its own: the desktop
 * sidebar-collapse pair, re-declared by Pick so a chrome change lands in
 * AppShellProps only (single source of truth). The console carries the pair
 * so its frame has the same host-controlled collapse the tenant shell does --
 * the state is the host's, and the frame renders the toggle and reports each
 * flip. */
type AppShellCollapseProps = Pick<
  AppShellProps,
  'sidebarCollapsed' | 'onSidebarCollapsedChange'
>

export interface ConsoleShellProps extends AppShellCollapseProps {
  /**
   * The console's page declarations. The shell derives the platform
   * navigation from the declarations that carry a `label` and renders
   * nothing else of them: path matching and view selection are the
   * route layer's, not the shell's.
   */
  readonly routes: readonly ConsoleRoute[]
  /**
   * The console's navigation group table: the order and headings the
   * sidebar groups the labelled pages into, each declaration naming its
   * group through its `group` field. Supplying it renders a grouped
   * sidebar; omitting it keeps the flat single column the console has
   * always shown, so an existing host is unchanged. A declaration whose
   * `group` is absent, or names no group here, renders ungrouped (above
   * the groups).
   */
  readonly navGroups?: readonly ConsoleNavGroup[]
  /** The host's routed content, rendered inside the `main` landmark. */
  readonly children: ReactNode
  /** The console title, rendered in the banner (host content). */
  readonly title?: ReactNode
  /** Banner content after the title, typically the account menu (host content). */
  readonly headerActions?: ReactNode
  /**
   * Called with a navigation entry's path when that entry is activated by
   * a plain click. Supplying it routes the entry in place through the
   * host's router: the shell still renders the entry as a link (a
   * middle-click or a copied URL stays a real navigation), but a plain
   * click is intercepted and handed here instead of reloading the page.
   * Absent, an entry is a plain link that navigates the browser -- a full
   * page load, which a host whose session lives in memory loses.
   */
  readonly onNavigate?: (href: string) => void
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
   * The assembled session, published to the routed pages so a
   * step-up-gated write can raise it. Absent, a page's step-up-gated
   * writes fire unguarded and a refused one renders its error code.
   */
  readonly session?: AuthSession
  /** Extra styling merged onto the root layout box. */
  readonly sx?: SxProps<Theme>
}

/** One plain navigation entry from a labelled declaration. */
function navItemFor(
  route: ConsoleRoute,
  onNavigate: ((href: string) => void) | undefined,
): AppShellNavItem {
  return {
    id: route.path,
    href: route.path,
    label: route.label,
    icon: route.icon,
    onClick:
      onNavigate === undefined
        ? undefined
        : (event) => {
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
 * declaration is dropped, so an empty heading never renders.
 */
function buildNavEntries(
  routes: readonly ConsoleRoute[],
  navGroups: readonly ConsoleNavGroup[] | undefined,
  onNavigate: ((href: string) => void) | undefined,
): readonly AppShellNavEntry[] {
  const labelled = routes.filter((route) => route.label !== undefined)
  if (navGroups === undefined) {
    return labelled.map((route) => navItemFor(route, onNavigate))
  }
  const groupIds = new Set(navGroups.map((group) => group.id))
  const ungrouped = labelled.filter(
    (route) => route.group === undefined || !groupIds.has(route.group),
  )
  const groups: AppShellNavGroup[] = navGroups
    .map((group) => ({
      id: group.id,
      label: group.label,
      icon: group.icon,
      defaultCollapsed: group.defaultCollapsed,
      items: labelled
        .filter((route) => route.group === group.id)
        .map((route) => navItemFor(route, onNavigate)),
    }))
    .filter((group) => group.items.length > 0)
  return [...ungrouped.map((route) => navItemFor(route, onNavigate)), ...groups]
}

export function ConsoleShell({
  routes,
  navGroups,
  children,
  title,
  headerActions,
  onNavigate,
  invitationLink,
  accountHref,
  session,
  sidebarCollapsed,
  onSidebarCollapsedChange,
  sx,
}: ConsoleShellProps) {
  const { t } = useConsoleUiTranslation()

  // The platform navigation is the labelled declarations: a declaration
  // without a label is a page that is reachable but not listed (a detail
  // route, say), exactly as route-kit's registration seam defines. When a
  // group table is supplied they gather into their groups (see
  // buildNavEntries); without one they stay the flat single column. No
  // entry is selected here -- selection needs path matching, which is the
  // route layer's. A plain click is intercepted for the host (onNavigate)
  // but the entry keeps its href, so it stays a real link.
  const navEntries = useMemo(
    () => buildNavEntries(routes, navGroups, onNavigate),
    [routes, navGroups, onNavigate],
  )

  return (
    <AppShell
      navItems={navEntries}
      navLabel={t('shell.navLabel')}
      header={title}
      headerActions={headerActions}
      sidebarCollapsed={sidebarCollapsed}
      onSidebarCollapsedChange={onSidebarCollapsedChange}
      sx={sx}
    >
      {/* The same in-place navigation seam the entries use, published to
          the routed pages so a detail link routes in place rather than
          reloading the page (see internal/navigation), and the host's
          invitation-link builder, published so a page can show an issued
          acceptance link the host knows how to build (see
          internal/invitation-link). */}
      <ConsoleNavigationProvider onNavigate={onNavigate}>
        {/* The content area carries the same padding the tenant shell's own
            content region applies (theme spacing 3 = 24px all round), so a
            page's heading, breadcrumb, table and form sit clear of the
            sidebar and the banner rather than against them. The session is
            published to the routed pages so a step-up-gated write can raise
            it, and the host's account settings destination so the step-up
            dialog can link to it (see internal/account-href). */}
        <Box sx={{ p: 3 }}>
          <ConsoleSessionProvider session={session ?? null}>
            <ConsoleAccountHrefProvider accountHref={accountHref}>
              <ConsoleInvitationLinkProvider invitationLink={invitationLink}>
                {children}
              </ConsoleInvitationLinkProvider>
            </ConsoleAccountHrefProvider>
          </ConsoleSessionProvider>
        </Box>
      </ConsoleNavigationProvider>
    </AppShell>
  )
}
