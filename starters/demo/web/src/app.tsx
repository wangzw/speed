/**
 * The app's view root: the route provider around the shell the current URL
 * lands in.
 *
 * The routing composition is route-kit's: a RouteProvider installs the router
 * and holds the page table, RouteView renders the matched declaration, and
 * useNavItems derives the navigation. The app adds the shell choice -- two
 * frames over one table:
 *
 *  - The tenant area renders `TenantFrame` (ProductShell, the three-branch view
 *    machine, with the nav derived from every non-console entry and the host's
 *    console entry -- offered only to platform staff -- in the header's action
 *    slot, plus the auth-ui sign-in surface and sign-out action in the host
 *    slots). The shell renders the nav and content only while the session is
 *    authenticated; a fresh visitor meets the sign-in surface, and one whose
 *    session ends meets the ended screen. The routed page sits in a padded
 *    content region so it does not butt against the chrome.
 *
 *  - The console area renders @speed/console-ui's frame instead, with no tenant
 *    frame around it. The area is chosen by the path prefix alone: a URL under
 *    CONSOLE_PREFIX enters the console area whatever the feature document says,
 *    so a deep link or refresh renders the console's own pending/failed views
 *    rather than the tenant frame. It carries the same session gate the tenant
 *    shell does (the console frame is not ProductShell), a feature-state gate
 *    (pending and failed render frameless), an area gate over the platform
 *    permission set (a viewer who holds no platform permission never enters the
 *    shell), and a host-composed route guard over each page's permission code.
 *    When no console declaration is left in force the URL falls through to the
 *    tenant area, where it is a 404 like any other unregistered path.
 *
 * A URL no declaration matches renders the fallback page inside the tenant area.
 * The auth callback route (a provider's round trip) renders outside both frames,
 * with no session gate: it lands before a session exists and finishes the flow
 * itself. The invitation landing, too, renders outside both frames -- but only
 * when the route layer matched its (feature-gated) declaration; otherwise it
 * falls through to the tenant area like any other gated path.
 */

import { useCallback, useEffect, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'
import {
  TenantProvider,
  useAuthState,
  useFeature,
  useFeatureState,
} from '@speed/auth-core'
import { featurePresence } from '@speed/auth-core'
import type { AuthSnapshot } from '@speed/auth-core'
import { useTranslation } from '@speed/i18n'
import { RouteGuard } from '@speed/layout-kit'
import type { AppShellNavItem, RouteGuardStatus } from '@speed/layout-kit'
import {
  NotificationBell,
  NotificationStreamProvider,
} from '@speed/notification-ui'
import type { ConsoleRoute } from '@speed/console-ui'
import { ProductShell } from '@speed/product-shell'
import {
  presentDeclarations,
  RouteProvider,
  RouteView,
  useMatch,
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
import {
  consoleShellRoutes,
  isConsolePath,
  useConsoleTarget,
} from './console-routes.js'
import {
  IdentityChangeProvider,
  ImpersonationBannerSlot,
  useIdentityChange,
  useImpersonationStatus,
} from './impersonation.js'
import { DemoNavLabel } from './nav-label.js'
import { AcceptInvitationPage } from './pages/accept-invitation-page.js'
import { AuthCallbackPage } from './pages/auth-callback-page.js'
import {
  NotFoundPage,
  TENANT_HOME_PATH,
} from './pages/not-found-page.js'
import { SignInView } from './pages/sign-in-view.js'
import { DEMO_NAMESPACE } from './resources.js'
import {
  AUTH_CALLBACK_PATH,
  invitationRoutes,
  NOTIFICATIONS_PATH,
  routes,
} from './routes.js'

/** The platform-staff permission domain the console routes' codes resolve in:
 * auth-core names 'system' the tenant-independent platform-staff set. */
const CONSOLE_PERMISSION_DOMAIN = 'system'

/** The app view root: routing installed once, over the shell switch. The
 * route table is gated (the notification, object and console declarations
 * carry `requires`), so the host hands the route layer its feature-presence
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
      {/* The identity-change action both areas report to: a start or end
          swaps whose identity every cached row, the session, the attached
          permission set, the current tenant and the live subscription
          belong to (src/impersonation). */}
      <IdentityChangeProvider>
        <AppFrame presence={presence} />
      </IdentityChangeProvider>
    </RouteProvider>
  )
}

/** Renders the area the current URL lands in: the console or the tenant shell. */
function AppFrame({
  presence,
}: {
  readonly presence: RoutePresence
}): ReactElement {
  const { pathname } = useLocation()
  // The declaration the current URL matches exactly on the narrowed surface:
  // used to tell a live invitation landing from a path that merely looks like
  // one. Absent, pending or failed, no declaration matches and the path falls
  // through to the tenant area's own matching.
  const match = useMatch()
  // The host's identity-change action, handed to the console page so a
  // settled start or end re-reads the identity the way the tenant frame's
  // banner does.
  const { applyIdentityChange } = useIdentityChange()
  // The desktop sidebar's collapse state, host-held and shared by both areas:
  // each frame renders the toggle (the collapse props are supplied) and reports
  // each flip here, so the collapse carries across the tenant/console switch.
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)

  // The invitation landing renders outside both shells: an invitee arrives
  // before any session exists. Only a matched (present, ready) declaration
  // reaches it; a deployment that does not serve authn.invitations falls
  // through to the tenant area, where the gated declaration is a 404.
  if (match !== undefined && match.declaration === invitationRoutes[0]) {
    return <AcceptInvitationPage token={match.context.params.token ?? ''} />
  }

  // The auth callback route renders outside both shells: a provider's round
  // trip lands here before a session exists, and the callback handler
  // finishes the flow itself. The handler's absent slot -- the deployment
  // offers no redirect or enterprise method -- is the tenant frame's 404.
  if (pathname === AUTH_CALLBACK_PATH) {
    return (
      <AuthCallbackPage
        absent={
          <TenantFrame
            presence={presence}
            sidebarCollapsed={sidebarCollapsed}
            onSidebarCollapsedChange={setSidebarCollapsed}
          >
            <NotFoundPage path={pathname} />
          </TenantFrame>
        }
      />
    )
  }

  if (isConsolePath(pathname)) {
    // The console declarations the presence predicate left in force. Pending
    // or failed leaves the set empty (fail-closed); a ready document may still
    // leave nothing when no console page's feature is served.
    const consoleRoutes = presentDeclarations(consoleShellRoutes, presence)
    // The console shell's own synchronous feature answer for the pages'
    // in-page controls.
    const hasFeature =
      presence.state === 'ready' ? presence.has : (): boolean => false
    return (
      <SessionGate>
        {/* An impersonated session never enters the console: its platform
            management reads are refused by the gateway whatever the
            impersonated account holds, so the whole area redirects to the
            tenant home, silently, before the feature state is even
            consulted. The explanation belongs to the tenant frame's
            banner, not to this redirect. */}
        <ConsoleImpersonationGate>
          <ConsoleFeatureGate presence={presence}>
            <ConsoleAreaGate>
              {consoleRoutes.length === 0 ? (
                // No console page is served: the URL is the tenant area's,
                // where the gated declarations render the same 404 an
                // unregistered path does.
                <TenantFrame
                  presence={presence}
                  sidebarCollapsed={sidebarCollapsed}
                  onSidebarCollapsedChange={setSidebarCollapsed}
                >
                  <RouteView />
                </TenantFrame>
              ) : (
                <ConsoleArea
                  routes={consoleRoutes}
                  hasFeature={hasFeature}
                  onImpersonationChange={applyIdentityChange}
                  sidebarCollapsed={sidebarCollapsed}
                  onSidebarCollapsedChange={setSidebarCollapsed}
                >
                  <ConsoleRouteGuard>
                    <RouteView />
                  </ConsoleRouteGuard>
                </ConsoleArea>
              )}
            </ConsoleAreaGate>
          </ConsoleFeatureGate>
        </ConsoleImpersonationGate>
      </SessionGate>
    )
  }

  return (
    <TenantFrame
      presence={presence}
      sidebarCollapsed={sidebarCollapsed}
      onSidebarCollapsedChange={setSidebarCollapsed}
    >
      <RouteView />
    </TenantFrame>
  )
}

/**
 * The tenant area's frame: the tenant context, the live notification
 * subscription, and ProductShell with the host's chrome in each slot.
 *
 * The feature document decides which of those surfaces exist: the roster read
 * runs only when tenant.admin is served, the bell only when notification.inbox
 * is, and the live subscription opens only when notification.stream is (beside
 * the session fact). The frame is also the wrapper the auth callback's absent
 * view renders in, so an absent callback and an unregistered path look the
 * same, inside the same shell.
 */
function TenantFrame({
  presence,
  sidebarCollapsed,
  onSidebarCollapsedChange,
  children,
}: {
  readonly presence: RoutePresence
  readonly sidebarCollapsed: boolean
  readonly onSidebarCollapsedChange: (collapsed: boolean) => void
  readonly children: ReactNode
}): ReactElement {
  const navigate = useNavigate()
  // The assembled client: its event-stream opener feeds the notification
  // surface's live subscription below.
  const { api } = useAppServices()
  // The notification surfaces and the roster are feature-gated; the hooks fail
  // closed, so a pending or failed document opens and reads nothing.
  const inboxServed = useFeature('notification.inbox')
  const streamServed = useFeature('notification.stream')
  const rosterServed = useFeature('tenant.admin')
  const snapshot = useAuthState()
  const authenticated = snapshot.state === 'authenticated'
  // Whose identity this session acts as right now. A start or end swaps it,
  // and the identity-keyed effects below re-read what was read under the
  // previous one.
  const impersonating = authenticated && snapshot.impersonating
  const principalId =
    snapshot.state === 'authenticated'
      ? (snapshot.principal?.account_id ?? '')
      : ''
  // The live subscription registers per (tenant, effective account); the
  // generation bumps after each identity change so it re-registers under the
  // new account even when the tenant stays the same.
  const { generation } = useIdentityChange()
  // The impersonation status read: enabled only while the snapshot says one
  // is in force, so a visitor who is not impersonating issues no request. It
  // owns the bare-404 ended path and the expiry timer.
  const grant = useImpersonationStatus(impersonating)
  // Whether the feature document is settled at all. The roster read treats the
  // three states differently: `pending`/`failed` keep the current tenant
  // unknown (tenant-scoped queries stay disabled), `ready` without tenant.admin
  // falls back to the placeholder, `ready` with it reads the roster.
  const featuresReady = presence.state === 'ready'
  // The current tenant is a host fact (the server resolves it per request
  // from the request's tenant header). chrome.tsx holds the two values that
  // come off it -- the switcher's identity and the alias the request surface
  // carries -- and this state projects the identity for rendering; the setter
  // writes the cell too, so the value a request announces and the value the
  // chrome shows agree. It starts empty and is settled once the roster below
  // is read: a tenant-scoped request fired before then would announce the
  // placeholder's alias, which a rename has left behind, and be refused 404.
  // A frame mounting during an impersonation starts unknown too: the
  // module-level cell still holds the operator's tenant from before the
  // console took over, and a first frame carrying it would issue one read
  // (and open one stream) under the impersonated session and the operator's
  // alias before the status read settles the grant's tenant.
  const [tenantId, setTenantIdState] = useState<string | null>(() =>
    impersonating ? null : getCurrentTenantId(),
  )
  const setTenantId = useCallback((id: string | null): void => {
    setCurrentTenantId(id)
    setTenantIdState(id)
  }, [])
  // The identity-change gap. Ending an impersonation re-decides the current
  // tenant from the roster, but until that roster read lands the current
  // tenant keeps the ended grant's value (when the status read answered a
  // bare 404 it is already unknown, because the grant is gone); only the
  // roster's outcome re-decides it. In that gap the roster list has no row
  // matching the current tenant, so the switcher would render its
  // no-current-tenant text -- a state a host that settles no tenant at all
  // also renders, for a different reason. The switcher therefore stays out
  // of the bar until the roster lands, rather than putting that text
  // against a shell that is only still reading. The edge is detected during
  // render, not in an effect: React re-renders this component before
  // committing, so the very first commit after the snapshot flips already
  // withholds the switcher. The tenant effect below closes the gap when the
  // roster outcome lands. A mount that does not follow an impersonation is
  // untouched, so a viewer who settles no tenant for another reason (no
  // roster-read access) keeps the switcher and its documented
  // no-current-tenant state.
  const [identitySettling, setIdentitySettling] = useState(false)
  const [previousImpersonating, setPreviousImpersonating] =
    useState(impersonating)
  if (previousImpersonating !== impersonating) {
    setPreviousImpersonating(impersonating)
    if (previousImpersonating) {
      setIdentitySettling(true)
    }
  }
  // Settle the current tenant from the feature state and the roster. The read
  // is the app's own generated operation on the platform's management surface;
  // it needs tenant.read in the platform domain, which the demo's operator
  // holds but an ordinary member does not. A viewer answered 403 keeps the
  // placeholder's aliases, which answer only while the roster's alias still
  // equals them: this read is the only route to a renamed alias and no
  // member-facing endpoint carries one, so after a rename such a viewer is
  // refused 404 on every tenant-scoped call. The demo's own seed signs in the
  // platform administrator alone, so the seeded deployment always answers. The
  // tenant-scoped surfaces stay disabled until this settles, because their
  // queries gate on the tenant being known (TenantProvider below).
  useEffect(() => {
    if (!authenticated) {
      return
    }
    if (impersonating) {
      // The roster read is a platform management read, refused to an
      // impersonated identity. The current tenant is the grant's tenant
      // identifier instead -- no roster read, no placeholder fallback --
      // and stays unknown until the status read settles, which keeps
      // tenant-scoped queries disabled. The request surface's alias comes
      // from the previously read roster cache when it holds this id, and
      // from the placeholder otherwise (the known limitation this shares
      // with a member who cannot read the roster).
      setTenantId(grant?.tenant ?? null)
      return
    }
    if (!featuresReady) {
      // A pending or failed document never stands in for "tenant.admin
      // absent": the current tenant stays unknown.
      setTenantId(null)
      setIdentitySettling(false)
      return
    }
    if (!rosterServed) {
      setTenantId(DEFAULT_TENANT_ID)
      setIdentitySettling(false)
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
        setIdentitySettling(false)
      })
      .catch(() => {
        if (cancelled) {
          return
        }
        setTenantId(DEFAULT_TENANT_ID)
        setIdentitySettling(false)
      })
    return () => {
      cancelled = true
    }
    // The identity is an input: after an impersonation ends the roster
    // decides the tenant again, and after a start the branch above keeps
    // the roster out of it.
  }, [
    authenticated,
    featuresReady,
    rosterServed,
    impersonating,
    principalId,
    grant?.tenant,
    setTenantId,
  ])
  const items = useNavItems()
  const consoleRoutes = presentDeclarations(consoleShellRoutes, presence)

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
          because the notification endpoints are tenant-scoped. It opens only
          once the session is authenticated AND the process serves
          notification.stream: an absent stream would otherwise reconnect
          through its backoff against an unmounted endpoint. */}
      <NotificationStreamProvider
        openStream={api.openEventStream}
        enabled={authenticated && streamServed}
        identityGeneration={generation}
      >
        <ProductShell
          navItems={navItems}
          header={<AppBrand />}
          // The impersonation banner sits above the app bar for the whole
          // tenant frame. It is always mounted: the component tells the
          // shell when there is nothing to show, and its live region keeps
          // the end confirmation after the banner itself goes away.
          banner={<ImpersonationBannerSlot grant={grant} />}
          // The notification bell rides the header actions slot beside the
          // shared chrome; it opens the notification surface. It lives only
          // in the tenant area (the console resolves no tenant, so its bell
          // would have nothing to read) and only when notification.inbox is
          // served: a deployment without it has no destination for the bell.
          // The console entry sits at the same end of the bar, nearest the
          // account controls, and shows only to platform staff (ConsoleEntry
          // gates itself).
          headerActions={
            <>
              {inboxServed ? (
                <NotificationBell
                  onClick={() => navigate(NOTIFICATIONS_PATH)}
                />
              ) : null}
              <HeaderActions />
              <ConsoleEntry routes={consoleRoutes} />
            </>
          }
          sidebarCollapsed={sidebarCollapsed}
          onSidebarCollapsedChange={onSidebarCollapsedChange}
          // The tenant switcher sits in the userMenu slot (the design's
          // placement for it) ahead of the account menu. It is not mounted
          // while impersonating: the impersonation is confined to one tenant,
          // the roster read is unreachable and its replacement list is the
          // grant's tenant alone, so the switcher has nothing to offer and
          // would show "no current tenant" against the banner that states the
          // tenant. Not mounting it also keeps it out of the accessibility
          // tree and the tab order, and the bar closes over the missing item
          // (the slot is a flex row, so the remaining controls simply sit
          // together). Once the impersonation ends the effect above settles
          // the tenant from the roster again, and the switcher returns over
          // that row; until the read lands the identity-change gap keeps it
          // out of the bar for the same reason -- the host has no tenant to
          // show yet, and its no-current-tenant text belongs to a host that
          // settles none.
          userMenu={
            <>
              {impersonating || identitySettling ? null : (
                <TenantMenu tenantId={tenantId} onTenantSelect={setTenantId} />
              )}
              <UserMenu />
            </>
          }
          signIn={<SignInView />}
        >
          <Box sx={{ p: 3 }}>{children}</Box>
        </ProductShell>
      </NotificationStreamProvider>
    </TenantProvider>
  )
}

/**
 * The header's console entry: a link into the first console page the principal
 * may open, shown only to a principal that holds a platform permission and
 * only while at least one console declaration is in force.
 *
 * The target comes from useConsoleTarget (src/console-routes.ts): null for an
 * impersonated session, otherwise the first labelled declaration, in table
 * order, with no permission code or one the platform permission set holds.
 * The set is not known synchronously -- the host attaches it once the
 * caller's platform permissions have been read, and useAuthState reads null
 * until then -- so the entry is hidden until the set lands, which is the safe
 * direction: a member never sees the entry for the moment before the set
 * arrives. The start page's console card calls the same hook, so the two
 * never disagree.
 *
 * It routes in place (the same seam every other entry uses), so entering the
 * console does not reload the page and sign the operator out, and it keeps its
 * href so a middle-click or a copied URL still reaches the console.
 */
function ConsoleEntry({
  routes,
}: {
  /** The console declarations the presence predicate left in force. */
  readonly routes: readonly ConsoleRoute[]
}): ReactElement | null {
  const navigate = useNavigate()
  // An impersonated session has no console to enter; the shared derivation
  // carries that rule for the header entry and the start page's card alike.
  const target = useConsoleTarget(routes)
  if (target === null) {
    return null
  }
  return (
    <Button
      color="inherit"
      href={target}
      onClick={(event) => {
        event.preventDefault()
        navigate(target)
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
 * The console area's impersonation gate: an impersonated session has no
 * console. The gateway refuses every platform management read to an
 * impersonated identity, whatever the impersonated account holds, so the
 * whole area leaves the console path space for the tenant home. The
 * redirect is silent, like the empty-permission redirect below it: the
 * visitor's explanation is the tenant frame's persistent banner, which the
 * tenant home now renders. The session gate runs first (an anonymous
 * viewer sees the sign-in surface); this gate runs before the feature
 * state, because no feature document makes the console available during an
 * impersonation.
 */
function ConsoleImpersonationGate({
  children,
}: {
  readonly children: ReactNode
}): ReactElement {
  const snapshot = useAuthState()
  if (snapshot.state === 'authenticated' && snapshot.impersonating) {
    return <Navigate to={TENANT_HOME_PATH} replace />
  }
  return <>{children}</>
}

/**
 * The console area's feature-state gate, frameless: while the document is
 * pending (a retry is in flight) or failed, the console renders the same
 * waiting/failure view the route layer would, with no shell around it. It runs
 * before the permission gate: a failed read keeps the platform permission set
 * unknown, and an operator whose read failed must see a retry, never a
 * redirect that reads as "not staff".
 */
function ConsoleFeatureGate({
  presence,
  children,
}: {
  readonly presence: RoutePresence
  readonly children: ReactNode
}): ReactElement {
  if (presence.state === 'pending') {
    return <RoutePresencePending />
  }
  if (presence.state === 'failed') {
    return <RoutePresenceFailed />
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
 * member. (The header entry keys on the first page whose code is held; the two
 * agree on the demo's principals, which hold everything or nothing.) An
 * unknown set is pending, never granted.
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
