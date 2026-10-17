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
 *    the nav derived from every non-console entry and the host's console entry
 *    -- offered only to platform staff -- in the header's action slot, plus the
 *    auth-ui sign-in surface and sign-out action in the host slots. The shell
 *    renders the nav and content only while the session is authenticated; a
 *    fresh visitor meets the sign-in surface, and one whose session ends meets
 *    the ended screen. The routed page sits in a padded content region so it
 *    does not butt against the chrome.
 *
 *  - The console area renders @speed/console-ui's frame instead, with no tenant
 *    frame around it. The area is chosen by the selected navigation entry: the
 *    console's pages are labelled under CONSOLE_PREFIX, so a URL in that path
 *    space selects one and the app's two areas never overlap. It carries the
 *    same session gate the tenant shell does (the console frame is not
 *    ProductShell), an area gate over the platform permission set (a viewer
 *    who holds no platform permission never enters the shell), and a
 *    host-composed route guard over each page's permission code.
 *
 * A URL no declaration matches renders the fallback page inside the tenant area.
 * The auth callback route (a provider's round trip) renders outside both frames,
 * with no session gate: it lands before a session exists and finishes the flow
 * itself.
 */

import { useCallback, useEffect, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Link from '@mui/material/Link'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'
import { TenantProvider, useAuthState, usePermission } from '@speed/auth-core'
import { featurePresence, useFeatureState } from '@speed/auth-core'
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
  presentDeclarations,
  RouteProvider,
  RouteView,
  useNavigate,
  useNavItems,
} from '@speed/route-kit'
import type { RouteContext, RoutePresence } from '@speed/route-kit'
import type { ReactElement, ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router-dom'
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

/** The tenant area's home path: the shell the console area sends a viewer it
 * turns away, so a denied URL leaves the console's path space. */
const TENANT_HOME_PATH = '/'

/** The app view root: routing installed once, over the shell switch. The
 * route table is gated (the notification and console declarations carry
 * `requires`), so the host hands the route layer its feature-presence
 * answer -- derived from the session's feature state -- plus the views a
 * gated path renders while that answer is pending or failed. */
export function DemoApp(): ReactElement {
  const presence = featurePresence(useFeatureState())
  return (
    <RouteProvider
      table={routes}
      fallback={(context: RouteContext) => <NotFoundPage path={context.path} />}
      presence={presence}
      pending={() => <RoutePresencePending />}
      failed={() => <RoutePresenceFailed />}
    >
      <AppFrame presence={presence} />
    </RouteProvider>
  )
}

/** Renders the area the current URL lands in: the console or the tenant shell. */
function AppFrame({
  presence,
}: {
  readonly presence: RoutePresence
}): ReactElement {
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
    // The console shell does not filter its navigation by `requires`: the
    // host hands it the declarations the presence predicate left in force,
    // and its own synchronous feature answer for the pages' in-page
    // controls. A pending or failed predicate leaves every gated page out
    // (fail-closed), which is also what the route layer renders.
    const hasFeature =
      presence.state === 'ready' ? presence.has : (): boolean => false
    return (
      <SessionGate>
        <ConsoleAreaGate>
          <ConsoleArea
            routes={presentDeclarations(consoleShellRoutes, presence)}
            hasFeature={hasFeature}
            sidebarCollapsed={sidebarCollapsed}
            onSidebarCollapsedChange={setSidebarCollapsed}
          >
            <ConsoleRouteGuard>
              <RouteView />
            </ConsoleRouteGuard>
          </ConsoleArea>
        </ConsoleAreaGate>
      </SessionGate>
    )
  }

  // The tenant navigation is every labelled entry outside the console area
  // (the console's pages are filtered out above, so nothing else points into
  // the console). The console entry is deliberately not a navigation item: it
  // lives in the header's action slot, offered only to platform staff, so a
  // member meets a navigation with no way into the console. Each entry keeps
  // its href (for a middle click or a copy) and navigates through the router
  // on a plain click, so the page never reloads.
  const navItems: readonly AppShellNavItem[] = items
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
    }))

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
          // would have nothing to read). The console entry sits at the same
          // end of the bar, nearest the account controls, and shows only to
          // platform staff (ConsoleEntry gates itself).
          headerActions={
            <>
              <NotificationBell onClick={() => navigate(NOTIFICATIONS_PATH)} />
              <HeaderActions />
              <ConsoleEntry />
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
 * The header's console entry: a text button into the platform console, shown
 * only to a principal that holds a platform permission. Every console page's
 * permission code resolves in the platform domain -- auth-core names 'system'
 * that set -- so the entry keys on one of those codes rather than any page's
 * own, and the seed's platform administrator (which holds every permission the
 * process declares) is told apart from an ordinary member by any one of them.
 *
 * The platform permission set is not known synchronously: the host attaches it
 * (setPermissionSet) once the caller's platform permissions have been read, and
 * usePermission answers false until then -- it fails closed, never throwing.
 * The entry is therefore hidden until the set lands and appears once the
 * principal is confirmed to hold the code, which is the safe direction: a
 * member never sees the entry for the moment before the set arrives.
 *
 * It routes in place (the same seam every other entry uses), so entering the
 * console does not reload the page and sign the operator out, and it keeps its
 * href so a middle-click or a copied URL still reaches the console.
 */
function ConsoleEntry(): ReactElement | null {
  const navigate = useNavigate()
  const canEnterConsole = usePermission('system', 'tenant.read')
  if (!canEnterConsole) {
    return null
  }
  return (
    <Button
      color="inherit"
      href={CONSOLE_PREFIX}
      onClick={(event) => {
        event.preventDefault()
        navigate(CONSOLE_PREFIX)
      }}
    >
      <DemoNavLabel labelKey="nav.console" />
    </Button>
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
 * The console area's access gate: the whole area is the platform-staff face,
 * so a principal who holds no platform permission never enters it. The console
 * frame -- its navigation, its banner -- is not rendered at all, rather than
 * rendered with its content swapped for a no-permission surface: the menu names
 * are themselves information a non-staff viewer must not receive.
 *
 * It settles the area tier of the console gate, between the header entry
 * (ConsoleEntry, which hides the way in) and each page's own code (the route
 * guard below, which turns a staff viewer away from a page they lack). The
 * decision is drawn from the platform permission set (auth-core's 'system'
 * domain), read here before the shell renders so a denied viewer never sees
 * even its chrome. Three outcomes:
 *
 *  - granted: the set is known and holds at least one platform permission --
 *    the principal is platform staff, and the console renders.
 *  - denied: the set is known and empty -- an ordinary member. The area renders
 *    nothing and redirects to the tenant home, so the console's path space (and
 *    the names within it) is left behind for a shell the viewer may use.
 *  - pending: the set is unknown (null) -- the host has not yet attached the
 *    caller's platform permissions. This is the fail-closed direction: an
 *    unknown set shows neither the console nor an immediate redirect, only a
 *    shell-less placeholder, and the area settles once the set lands. Rendering
 *    the console on an unknown set would show the shell to a member for the
 *    window before their empty set is attached, which is the only moment the
 *    set is null for a signed-in viewer.
 */
function ConsoleAreaGate({
  children,
}: {
  readonly children: ReactNode
}): ReactElement | null {
  const snapshot = useAuthState()
  const status = consoleAreaStatus(snapshot)
  if (status === 'granted') {
    return <>{children}</>
  }
  if (status === 'denied') {
    return <Navigate to={TENANT_HOME_PATH} replace />
  }
  return <ConsoleAccessPending />
}

/**
 * The area tier's decision for the current session. The criterion is the
 * platform permission set, not any one page's code: the area is staff-only, so
 * holding a single platform permission is what tells staff from an ordinary
 * member. (The header entry keys on one representative code, tenant.read, to
 * decide whether to offer the way in; the two agree on the demo's principals,
 * which hold everything or nothing.) An unknown set is pending, never granted.
 */
function consoleAreaStatus(
  snapshot: AuthSnapshot,
): 'granted' | 'denied' | 'pending' {
  if (snapshot.state !== 'authenticated') {
    // The session gate above turned an anonymous viewer away; pending is
    // fail-closed and never shows the shell.
    return 'pending'
  }
  const set = snapshot.permissionSets[CONSOLE_PERMISSION_DOMAIN]
  if (set === null) {
    return 'pending'
  }
  return set.length > 0 ? 'granted' : 'denied'
}

/**
 * The shell-less placeholder the area gate shows while the platform permission
 * set is unknown: no console chrome, so nothing of the console reaches the
 * viewer before the gate has settled. The label names the wait for assistive
 * technology.
 */
function ConsoleAccessPending(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Box
      sx={{
        display: 'flex',
        justifyContent: 'center',
        alignItems: 'center',
        paddingY: 6,
      }}
    >
      <CircularProgress aria-label={t('console.accessPending')} />
    </Box>
  )
}

/** The view a feature-gated path renders while the host's presence
 * answer is pending (including an anonymous session, which has no
 * feature state). */
function RoutePresencePending(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Box
      sx={{
        display: 'flex',
        justifyContent: 'center',
        alignItems: 'center',
        paddingY: 6,
      }}
    >
      <CircularProgress aria-label={t('route.pending')} />
    </Box>
  )
}

/** The view a feature-gated path renders when the feature document could
 * not be read. A failed read is neither "absent" nor "present", so it gets
 * a retry rather than the 404 a missing feature renders. */
function RoutePresenceFailed(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const { session } = useAppServices()
  return (
    <Box
      component="section"
      role="alert"
      sx={{ display: 'flex', flexDirection: 'column', gap: 2, p: 3 }}
    >
      <Typography variant="body1">{t('route.failed')}</Typography>
      <Box>
        <Button onClick={() => void session.retryFeatures()}>
          {t('route.retry')}
        </Button>
      </Box>
    </Box>
  )
}

/**
 * The console's page-level gate, composed the host way: layout-kit's
 * RouteGuard fed a status the host derives from the matched page's permission
 * code (the console declarations carry tenant.read / settings.read /
 * audit.read). A code held, or a page with no code, is allowed; a code the
 * principal is known not to hold is denied. This is the innermost tier of the
 * console gate -- the header entry (ConsoleEntry) hides the way in, the area
 * gate (ConsoleAreaGate) turns a non-staff viewer away before the shell
 * renders, and this guard settles each page's own code within the area. It
 * runs only inside the area, where the platform set is already known, so its
 * status is a plain set lookup: a staff viewer who lacks one page's code meets
 * the no-permission surface in place of that page, the rest of the console
 * frame intact.
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
  if (set === null) {
    // The area gate renders this guard only once the platform set is known;
    // an unknown set is fail-closed here too rather than passing the page.
    return 'pending'
  }
  return set.includes(permission) ? 'allowed' : 'denied'
}

/**
 * The fallback page for a URL no declaration matches. The address stays on
 * the page in a secondary line: it is what a user copies back to an operator
 * when reporting the dead link.
 */
function NotFoundPage({ path }: { readonly path: string }): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const navigate = useNavigate()
  return (
    <Box
      component="section"
      sx={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'flex-start',
        gap: 2,
      }}
    >
      <Typography component="h1" variant="h4">
        {t('notFound.heading')}
      </Typography>
      <Typography variant="body1">{t('notFound.body')}</Typography>
      <Link
        href={TENANT_HOME_PATH}
        underline="hover"
        onClick={(event) => {
          event.preventDefault()
          navigate(TENANT_HOME_PATH)
        }}
      >
        {t('notFound.home')}
      </Link>
      <Typography
        variant="caption"
        color="text.secondary"
        sx={{ wordBreak: 'break-all' }}
      >
        {t('notFound.path', { path })}
      </Typography>
    </Box>
  )
}
