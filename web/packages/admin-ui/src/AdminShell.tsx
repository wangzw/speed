/**
 * AdminShell: the platform-staff console frame. It derives navigation from the
 * labelled declarations inside layout-kit's AppShell and does no routing.
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

/** The props of the admin shell frame. */
export interface AdminShellProps extends AppShellPassthroughProps {
  /**
   * The console's page declarations: the navigation is derived from those that
   * carry a `label`, and the table is published so a page can resolve a sibling
   * by its stable `id`.
   */
  readonly routes: readonly AdminRoute[]
  /**
   * The navigation group table: supplying it groups the labelled pages, while
   * omitting it keeps the flat column. A declaration with no matching group
   * renders ungrouped above the groups.
   */
  readonly navGroups?: readonly AdminNavGroup[]
  /**
   * The host's synchronous feature answer, published to the routed pages.
   * Required; the shell itself does not filter `routes` by it.
   */
  readonly hasFeature: (name: FeatureName) => boolean
  /** The host's routed content, rendered inside the `main` landmark. */
  readonly children: ReactNode
  /** The console title, rendered in the banner (host content). */
  readonly title?: ReactNode
  /** Banner content after the title, typically the account menu (host content). */
  readonly headerActions?: AppShellNavSlot
  /** The navigation landmark's accessible name; defaults to `shell.navLabel`. */
  readonly navLabel?: string
  /**
   * Called with a navigation entry's path on a plain click. Supplying it routes
   * in place; absent, the entry is a plain link that reloads the page.
   */
  readonly onNavigate?: (href: string) => void
  /** Whether a path is the current one, computed by the host; absent, none is selected. */
  readonly isSelected?: (path: string) => boolean
  /** Builds the acceptance link for an issued token; absent, the page shows the bare token. */
  readonly invitationLink?: (token: string) => string
  /** The host's account settings page; absent, the step-up dialog renders text only. */
  readonly accountHref?: string
  /** The host's current path, sent as the step-up flow's `return_to`. */
  readonly returnPath?: string
  /** The session a step-up-gated write may raise; absent, writes fire unguarded. */
  readonly session?: AuthSession
  /**
   * The host's action after an impersonation start or end settles: it evicts
   * old-identity cache, re-reads the session and moves the page.
   */
  readonly onImpersonationChange?: (
    change: AdminImpersonationChange,
  ) => void | Promise<void>
  /**
   * The host's hook fired before an impersonation start is written; the returned
   * release runs when the attempt does not land.
   */
  readonly onImpersonationStarting?: AdminImpersonationStarting
  /** Extra styling merged onto the root layout box. */
  readonly sx?: SxProps<Theme>
}

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
            // Only a plain primary click belongs to the host's router.
            if (!isPlainPrimaryClick(event)) {
              return
            }
            event.preventDefault()
            onNavigate(route.path)
          },
  }
}

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

/** Renders the platform-staff console frame around the host's routed content. */
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

  // The labelled declarations form the navigation; selection is the host's
  // answer, and a plain click goes to onNavigate while the href stays.
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
        {/* The same inset the tenant shell's content region applies. */}
        <Box sx={{ p: 3 }}>{children}</Box>
      </AdminProvider>
    </AppShell>
  )
}
