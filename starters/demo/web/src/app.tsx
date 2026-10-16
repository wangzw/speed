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

import { useCallback, useEffect, useState } from 'react'
import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'
import { TenantProvider, useAuthState } from '@speed/auth-core'
import type { AuthSnapshot } from '@speed/auth-core'
import { useTranslation } from '@speed/i18n'
import { RouteGuard } from '@speed/layout-kit'
import type { AppShellNavItem, RouteGuardStatus } from '@speed/layout-kit'
import {
  NotificationBell,
  NotificationStreamProvider,
} from '@speed/notification-ui'
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
import { tenantAdminTenantsList } from './app-api/index.js'
import { useAppServices } from './app-services.js'
import {
  AppBrand,
  DEFAULT_TENANT_ID,
  getCurrentTenantId,
  HeaderActions,
  setCurrentTenantId,
  setTenantAliases,
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
  NOTIFICATIONS_PATH,
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
  // The assembled client: its event-stream opener feeds the notification
  // surface's live subscription below.
  const { api } = useAppServices()
  // The notification stream is authenticated; hold it open only once the
  // session is, so an anonymous viewer on the sign-in surface never opens
  // (and reconnects) a connection that can only 401.
  const authenticated = useAuthState().state === 'authenticated'
  // The current tenant is a host fact (the server resolves it per request
  // from the request's tenant header). chrome.tsx holds the two values that
  // come off it -- the switcher's identity and the alias the request surface
  // carries -- and this state projects the identity for rendering; the setter
  // writes the cell too, so the value a request announces and the value the
  // chrome shows agree. It starts empty and is settled once the roster below
  // is read: a tenant-scoped request fired before then would announce the
  // placeholder's alias, which a rename has left behind, and be refused 404.
  const [tenantId, setTenantIdState] = useState<string | null>(getCurrentTenantId())
  const setTenantId = useCallback((id: string | null): void => {
    setCurrentTenantId(id)
    setTenantIdState(id)
  }, [])
  // Read the roster once a viewer is signed in, so the value announced as
  // X-Tenant is each tenant's CURRENT alias, not a name the placeholder fixed
  // and a rename has since moved. The read is the app's own generated
  // operation on the platform's management surface; it needs tenant.read in
  // the platform domain, which the demo's operator holds but an ordinary member
  // does not. A viewer answered 403 keeps the placeholder's aliases, which
  // answer only while the roster's alias still equals them: this read is the
  // only route to a renamed alias and no member-facing endpoint carries one, so
  // after a rename such a viewer is refused 404 on every tenant-scoped call.
  // The demo's own seed signs in the platform administrator alone, so the
  // seeded deployment always answers. The tenant-scoped surfaces stay disabled
  // until this settles, because their queries gate on the tenant being known
  // (TenantProvider below).
  useEffect(() => {
    if (!authenticated) {
      return
    }
    let cancelled = false
    // Deferred into a promise: the generated operation reaches the api-sdk
    // request seam, which throws synchronously when no request function is
    // bound (a suite that mounts the app without the assembly). Deferring
    // turns that into a rejection the catch below handles like any other
    // failed read.
    void Promise.resolve()
      .then(() => tenantAdminTenantsList())
      .then((answer) => {
        if (cancelled) {
          return
        }
        setTenantAliases(
          new Map(answer.tenants.map((tenant) => [tenant.id, tenant.alias])),
        )
        const first = answer.tenants.find((tenant) => tenant.status === 'active')
        setTenantId(first?.id ?? DEFAULT_TENANT_ID)
      })
      .catch(() => {
        if (cancelled) {
          return
        }
        setTenantId(DEFAULT_TENANT_ID)
      })
    return () => {
      cancelled = true
    }
  }, [authenticated, setTenantId])
  // The desktop sidebar's collapse state, host-held and shared by both areas:
  // each frame renders the toggle (the collapse props are supplied) and reports
  // each flip here, so the collapse carries across the tenant/console switch.
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
        <ConsoleArea
          routes={consoleShellRoutes}
          sidebarCollapsed={sidebarCollapsed}
          onSidebarCollapsedChange={setSidebarCollapsed}
        >
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
      {/* The live notification subscription: it opens the api client's
          event-stream seam (the assembled client's openEventStream) for
          the acting tenant and re-reads the unread count and message list
          on every pushed frame. Mounted here, inside the tenant area,
          because the notification endpoints are tenant-scoped. */}
      <NotificationStreamProvider
        openStream={api.openEventStream}
        enabled={authenticated}
      >
        <ProductShell
          navItems={navItems}
          header={<AppBrand />}
          // The notification bell rides the header actions slot beside the
          // shared chrome; it opens the notification surface. It lives only
          // in the tenant area (the console resolves no tenant, so its bell
          // would have nothing to read).
          headerActions={
            <>
              <NotificationBell onClick={() => navigate(NOTIFICATIONS_PATH)} />
              <HeaderActions />
            </>
          }
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
      </NotificationStreamProvider>
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
