/**
 * The app's view root: the route provider around the shell the current URL
 * lands in.
 *
 * The routing composition is route-kit's: a RouteProvider installs the router
 * and holds the page table, RouteView renders the matched declaration, and
 * useNavItems derives the navigation. The app adds the shell choice -- two
 * frames over one table:
 *
 *  - The tenant area renders ProductShell, the three-branch view machine, with
 *    the nav derived from every non-console entry plus the host's own console
 *    entry, and the auth-ui sign-in surface and sign-out action in the host
 *    slots. The shell renders the nav and content only while the session is
 *    authenticated; a fresh visitor meets the sign-in surface, and one whose
 *    session ends meets the ended screen. The routed page sits in a padded
 *    content region so it does not butt against the chrome.
 *
 *  - The console area renders @speed/console-ui's frame instead, with no tenant
 *    frame around it. The area is chosen by the selected navigation entry: the
 *    console's pages are labelled under CONSOLE_PREFIX, so a URL in that path
 *    space selects one and the app's two areas never overlap. It carries the
 *    same session gate the tenant shell does (the console frame is not
 *    ProductShell) and a host-composed route guard over each page's permission
 *    code.
 *
 * A URL no declaration matches renders the fallback page inside the tenant area.
 * The auth callback route (a provider's round trip) renders outside both frames,
 * with no session gate: it lands before a session exists and finishes the flow
 * itself.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'
import { TenantProvider, useAuthState } from '@speed/auth-core'
import type { AuthSnapshot } from '@speed/auth-core'
import { useTranslation } from '@speed/i18n'
import { RouteGuard } from '@speed/layout-kit'
import type { AppShellNavItem, RouteGuardStatus } from '@speed/layout-kit'
import { ProductShell } from '@speed/product-shell'
import {
  RouteProvider,
  RouteView,
  useNavigate,
  useNavItems,
} from '@speed/route-kit'
import type { RouteContext } from '@speed/route-kit'
import type { ReactElement, ReactNode } from 'react'
import { useLocation } from 'react-router-dom'
import {
  AppBrand,
  getCurrentTenantId,
  HeaderActions,
  setCurrentTenantId,
  TenantMenu,
  UserMenu,
} from './chrome.js'
import { ConsoleArea } from './console/console-area.js'
import { DemoNavLabel } from './nav-label.js'
import { AcceptInvitationPage } from './pages/accept-invitation-page.js'
import { AuthCallbackPage } from './pages/auth-callback-page.js'
import { SignInView } from './pages/sign-in-view.js'
import { matchInvitationToken } from './paths.js'
import { DEMO_NAMESPACE } from './resources.js'
import {
  AUTH_CALLBACK_PATH,
  CONSOLE_PREFIX,
  consoleShellRoutes,
  isConsolePath,
  routes,
} from './routes.js'

/** The platform-staff permission domain the console routes' codes resolve in:
 * auth-core names 'system' the tenant-independent platform-staff set. */
const CONSOLE_PERMISSION_DOMAIN = 'system'

/** The app view root: routing installed once, over the shell switch. */
export function DemoApp(): ReactElement {
  return (
    <RouteProvider
      table={routes}
      fallback={(context: RouteContext) => <NotFoundPage path={context.path} />}
    >
      <AppFrame />
    </RouteProvider>
  )
}

/** Renders the area the current URL lands in: the console or the tenant shell. */
function AppFrame(): ReactElement {
  const items = useNavItems()
  const navigate = useNavigate()
  // The current tenant is a host fact (the server resolves it per request
  // from the request's tenant header). chrome.tsx holds the one value --
  // the client's tenantProvider reads it per request -- and this state
  // projects it for rendering; the setter writes the cell too, so the
  // value a request announces and the value the chrome shows agree. The
  // tenant area mounts it on TenantProvider; a switch would update this
  // (and, in a product, the route and the departing tenant's cache).
  const [tenantId, setTenantIdState] = useState<string | null>(getCurrentTenantId())
  const setTenantId = (id: string | null): void => {
    setCurrentTenantId(id)
    setTenantIdState(id)
  }
  // The desktop sidebar's collapse state, host-held: the shell renders the
  // toggle (the collapse props are supplied) and reports each flip back.
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const { pathname } = useLocation()

  // The auth callback route renders outside both shells: a provider's round
  // trip lands here before a session exists, and the callback handler
  // finishes the flow itself.
  if (pathname === AUTH_CALLBACK_PATH) {
    return <AuthCallbackPage />
  }

  // The invitation landing renders outside both shells too: an invitee
  // arrives before any session exists, and the accept screen gates itself on
  // the host's sign-in surface.
  const invitationToken = matchInvitationToken(pathname)
  if (invitationToken !== null) {
    return <AcceptInvitationPage token={invitationToken} />
  }

  const consoleSelected = items.some(
    (item) => item.selected && isConsolePath(item.id),
  )
  if (consoleSelected) {
    return (
      <SessionGate>
        <ConsoleArea routes={consoleShellRoutes}>
          <ConsoleRouteGuard>
            <RouteView />
          </ConsoleRouteGuard>
        </ConsoleArea>
      </SessionGate>
    )
  }

  // The tenant navigation is every labelled entry outside the console area,
  // plus the host's own console entry (the console's pages are filtered out
  // above, so nothing else points into the console). Each entry keeps its href
  // (for a middle click or a copy) and navigates through the router on a plain
  // click, so the page never reloads.
  const consoleEntry: AppShellNavItem = {
    id: 'console',
    label: <DemoNavLabel labelKey="nav.console" />,
    href: CONSOLE_PREFIX,
    selected: false,
    onClick: (event) => {
      event.preventDefault()
      navigate(CONSOLE_PREFIX)
    },
  }
  const navItems: readonly AppShellNavItem[] = [
    ...items
      .filter((item) => !isConsolePath(item.id))
      .map((item): AppShellNavItem => ({
        id: item.id,
        label: item.label,
        href: item.href,
        selected: item.selected,
        onClick: (event) => {
          event.preventDefault()
          navigate(item.href)
        },
      })),
    consoleEntry,
  ]

  return (
    <TenantProvider tenantId={tenantId}>
      <ProductShell
        navItems={navItems}
        header={<AppBrand />}
        headerActions={<HeaderActions />}
        sidebarCollapsed={sidebarCollapsed}
        onSidebarCollapsedChange={setSidebarCollapsed}
        // The tenant switcher sits in the userMenu slot (the design's
        // placement for it) ahead of the account menu.
        userMenu={
          <>
            <TenantMenu tenantId={tenantId} onTenantSelect={setTenantId} />
            <UserMenu />
          </>
        }
        signIn={<SignInView />}
      >
        <Box sx={{ p: 3 }}>
          <RouteView />
        </Box>
      </ProductShell>
    </TenantProvider>
  )
}

/**
 * The console area's session gate. The tenant area is gated by ProductShell
 * itself, but the console renders its own frame, so it needs the same branch:
 * an anonymous viewer -- a fresh visitor, or one whose authenticated session
 * turned anonymous mid-use (the same snapshot flip) -- meets the host's
 * sign-in surface instead of a console whose requests can never answer.
 */
function SessionGate({
  children,
}: {
  readonly children: ReactNode
}): ReactElement {
  const snapshot = useAuthState()
  if (snapshot.state !== 'authenticated') {
    return <SignInView />
  }
  return <>{children}</>
}

/**
 * The console's route-level gate, composed the host way: layout-kit's
 * RouteGuard fed a status the host derives from the matched page's permission
 * code (the console declarations carry tenant.read / settings.read /
 * audit.read). A code the principal is known not to hold is denied; a code
 * held, a page with no code, or -- the skeleton's case, which attaches no
 * platform permission list -- an unknown set passes, since the server stays
 * the authority on the console's administration surfaces (the console-ui
 * design: a caller lacking a permission is refused server-side rather than
 * hidden client-side).
 */
function ConsoleRouteGuard({
  children,
}: {
  readonly children: ReactNode
}): ReactElement {
  const snapshot = useAuthState()
  const items = useNavItems()
  const selected = items.find((item) => item.selected && isConsolePath(item.id))
  const permission = consoleShellRoutes.find(
    (route) => route.path === selected?.id,
  )?.permission
  return (
    <RouteGuard status={consoleGuardStatus(snapshot, permission)}>
      {children}
    </RouteGuard>
  )
}

/** The RouteGuard status for one console page under the current session. */
function consoleGuardStatus(
  snapshot: AuthSnapshot,
  permission: string | undefined,
): RouteGuardStatus {
  if (permission === undefined) {
    return 'allowed'
  }
  if (snapshot.state !== 'authenticated') {
    // No session yet: the session gate above turned this viewer away, so the
    // guard only ever settles once authenticated; pending is fail-closed.
    return 'pending'
  }
  const set = snapshot.permissionSets[CONSOLE_PERMISSION_DOMAIN]
  return set === null || set.includes(permission) ? 'allowed' : 'denied'
}

/** The fallback page for a URL no declaration matches. */
function NotFoundPage({ path }: { readonly path: string }): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Box
      component="section"
      sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}
    >
      <Typography component="h1" variant="h4">
        {t('notFound.heading')}
      </Typography>
      <Typography variant="body1">{t('notFound.body', { path })}</Typography>
    </Box>
  )
}
