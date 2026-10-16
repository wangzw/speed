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
 * navigation stays distinguishable from any other shell's.
 *
 * The shell reads no data and holds no operations: the pages it hosts
 * reach their reads and writes through the operations bundle the host
 * mounted (see operations.tsx), and the permissions the declarations
 * carry are data the host reads to compose its own route guard.
 */

import type { ReactNode } from 'react'
import type { SxProps, Theme } from '@mui/material/styles'
import { AppShell } from '@speed/layout-kit'
import type { AppShellNavItem } from '@speed/layout-kit'
import type { ConsoleRoute } from './route.js'
import { ConsoleNavigationProvider } from './internal/navigation.js'
import { useConsoleUiTranslation } from './internal/translation.js'

export interface ConsoleShellProps {
  /**
   * The console's page declarations. The shell derives the platform
   * navigation from the declarations that carry a `label` and renders
   * nothing else of them: path matching and view selection are the
   * route layer's, not the shell's.
   */
  readonly routes: readonly ConsoleRoute[]
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
  /** Extra styling merged onto the root layout box. */
  readonly sx?: SxProps<Theme>
}

export function ConsoleShell({
  routes,
  children,
  title,
  headerActions,
  onNavigate,
  sx,
}: ConsoleShellProps) {
  const { t } = useConsoleUiTranslation()

  // The platform navigation is the labelled declarations, in declaration
  // order: a declaration without a label is a page that is reachable but
  // not listed (a detail route, say), exactly as route-kit's registration
  // seam defines. No entry is selected here -- selection needs path
  // matching, which is the route layer's. A plain click is intercepted
  // for the host (onNavigate) but the entry keeps its href, so it stays a
  // real link.
  const navItems: readonly AppShellNavItem[] = routes
    .filter((route) => route.label !== undefined)
    .map((route) => ({
      id: route.path,
      href: route.path,
      label: route.label,
      onClick:
        onNavigate === undefined
          ? undefined
          : (event) => {
              event.preventDefault()
              onNavigate(route.path)
            },
    }))

  return (
    <AppShell
      navItems={navItems}
      navLabel={t('shell.navLabel')}
      header={title}
      headerActions={headerActions}
      sx={sx}
    >
      {/* The same in-place navigation seam the entries use, published to
          the routed pages so a detail link routes in place rather than
          reloading the page (see internal/navigation). */}
      <ConsoleNavigationProvider onNavigate={onNavigate}>
        {children}
      </ConsoleNavigationProvider>
    </AppShell>
  )
}
