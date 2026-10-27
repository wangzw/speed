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

import { useEffect, useMemo, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import IconButton from '@mui/material/IconButton'
import Typography from '@mui/material/Typography'
import {
  TenantProvider,
  useAuthState,
  useFeature,
  useFeatureState,
} from '@speed/auth-core'
import { featurePresence } from '@speed/auth-core'
import type { AuthSnapshot } from '@speed/auth-core'
import { ImpersonationNoticeProvider } from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import { RouteGuard } from '@speed/layout-kit'
import type {
  AppShellNavItem,
  AppShellShellLayout,
  RouteGuardStatus,
} from '@speed/layout-kit'
import {
  NotificationBell,
  NotificationPresenceProvider,
  NotificationStreamProvider,
} from '@speed/notification-ui'
import type { ConsoleRoute } from '@speed/console-ui'
import { ProductShell } from '@speed/product-shell'
import {
  presentDeclarations,
  RouteProvider,
  RouteView,
  useLocationPath,
  useMatch,
  useNavigate,
  useNavItems,
  useRedirect,
  useRouteLinkProps,
} from '@speed/route-kit'
import type { RouteContext, RoutePresence } from '@speed/route-kit'
import type { ReactElement, ReactNode } from 'react'
import {
  contactVerificationPending,
  setupIncomplete,
} from './account-setup.js'
import {
  tenantAdminTenantsList,
  useAuthnProfileGet,
  workspacePersonalEnsure,
} from './app-api/index.js'
import { useAppServices } from './app-services.js'
import {
  AppBrand,
  getCurrentTenantId,
  getRecordedTenantAlias,
  HeaderActions,
  setCurrentTenantId,
  setTenantAlias,
  setTenantAliases,
  TenantMenu,
  UserMenu,
  useCurrentTenantId,
  useTenantSelection,
} from './chrome.js'
import type { DemoTenant } from './chrome.js'
import { ConsoleArea } from './console/console-area.js'
import {
  consoleShellRoutes,
  isConsolePath,
  useConsoleTarget,
} from './console-routes.js'
import {
  IdentityChangeProvider,
  ImpersonationBannerSlot,
  ImpersonationChip,
  useIdentityChange,
  useImpersonationNoticeState,
  useImpersonationStatus,
} from './impersonation.js'
import { ContactVerificationBanner } from './contact-verification-banner.js'
import { DemoNavLabel } from './nav-label.js'
import { ConsoleNavIcon } from './nav-icons.js'
import { PersonalTenantNoticeProvider } from './personal-tenant-notice.js'
import { TenantMembershipNoticeHost } from './tenant-membership-notice.js'
import { AcceptInvitationPage } from './pages/accept-invitation-page.js'
import { AuthCallbackPage } from './pages/auth-callback-page.js'
import {
  NotFoundPage,
  TENANT_HOME_PATH,
} from './pages/not-found-page.js'
import { SignInView } from './pages/sign-in-view.js'
import {
  ACCOUNT_PATH,
  isAccountLevelPath,
  PASSWORD_RESET_PATH,
} from './paths.js'
import { DEMO_NAMESPACE } from './resources.js'
import {
  clearSelfTenants,
  loadSelfTenants,
  PERSONAL_TENANT_ID_PREFIX,
  personalTenantId,
  selfTenantAlias,
  useSelfTenantState,
} from './tenant-self.js'
import type { SelfTenantState } from './tenant-self.js'
import {
  AUTH_CALLBACK_PATH,
  invitationRoutes,
  navIconFor,
  NOTIFICATIONS_PATH,
  routes,
} from './routes.js'

/** The platform-staff permission domain the console routes' codes resolve in:
 * auth-core names 'system' the tenant-independent platform-staff set. */
const CONSOLE_PERMISSION_DOMAIN = 'system'

/**
 * The tenant the host settles when it has none: the first roster row that is
 * active and is not someone's personal tenant. The roster view carries no
 * kind, so the server's identifier prefix tells the personal tenants apart:
 * one belongs to a single account and answers a staff viewer 404, and it also
 * sorts before most deployment identifiers ('p' < 't', 'z'), so without that
 * check the first active row would often be a stranger's personal space. The
 * result is nameable through the roster's alias cache. Null when the roster
 * offers no such row.
 */
function defaultRosterTenant(
  tenants: readonly { readonly id: string; readonly status: string }[],
): string | null {
  return (
    tenants.find(
      (tenant) =>
        tenant.status === 'active' &&
        !tenant.id.startsWith(PERSONAL_TENANT_ID_PREFIX),
    )?.id ?? null
  )
}

/**
 * Whether the host can name a tenant: either the caller tenant list holds the
 * row, or the roster's alias cache does (a platform staff member entering a
 * tenant they are not a member of). A current tenant that no source names
 * would leave the caller in a tenant the bar cannot show and the switcher
 * cannot leave, while every tenant-scoped request is refused.
 */
function tenantNameable(tenantId: string, self: SelfTenantState): boolean {
  return (
    self.tenants.some((tenant) => tenant.id === tenantId) ||
    getRecordedTenantAlias(tenantId) !== null
  )
}

/** The app view root: routing installed once, over the shell switch. The
 * route table is gated (the notification, object and console declarations
 * carry `requires`), so the host hands the route layer its feature-presence
 * answer -- derived from the session's feature state -- plus the views a
 * gated path renders while that answer is pending or failed. The deployment's
 * shell layout (src/runtime-config.ts) rides down to both frames; absent, the
 * frames keep their top-bar default. */
export function DemoApp({
  shellLayout,
}: {
  readonly shellLayout?: AppShellShellLayout
} = {}): ReactElement {
  const presence = featurePresence(useFeatureState())
  const snapshot = useAuthState()
  const authenticated = snapshot.state === 'authenticated'
  const principalId =
    snapshot.state === 'authenticated'
      ? (snapshot.principal?.account_id ?? '')
      : ''
  const impersonating = authenticated && snapshot.impersonating
  // A session that ends leaves no identity for the host's current tenant or
  // its alias map to belong to. Clear both module-level cells here, in the
  // always-mounted root, so the clearing does not depend on which frame is
  // mounted: the console area's session gate unmounts TenantFrame, and a
  // viewer who signs in again from there would otherwise inherit the ended
  // account's tenant -- under a personal tenant, another account's tenant --
  // for the window before the roster or the ensure settles. TenantFrame
  // re-syncs its own state with this clearing below.
  useEffect(() => {
    if (!authenticated) {
      setCurrentTenantId(null)
      setTenantAliases(new Map())
    }
  }, [authenticated])
  // The caller tenant list belongs to an identity, not to the page. Read it
  // once per acting account here in the always-mounted root -- not in the
  // tenant frame, which the console area and the sign-in surface do not mount
  // -- after forgetting the previous account's rows. It is read while
  // impersonating too: the answer is narrowed to the grant's tenant on the
  // server, and it is the alias source the tenant area has after a full page
  // load, when the roster cache the operator read before is gone. The
  // switcher stays unmounted during an impersonation, so the list has no
  // tenant to offer there; the alias it names is what the request surface
  // announces.
  useEffect(() => {
    if (!authenticated) {
      clearSelfTenants()
      return
    }
    clearSelfTenants()
    void loadSelfTenants()
  }, [authenticated, principalId, impersonating])
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
        {/* Above the shell switch: an accepted invitation announces the new
            membership on its way from the landing (outside both shells) to
            the tenant home (inside the tenant frame), so the confirmation has
            to outlive that route change (src/tenant-membership-notice.tsx). */}
        <TenantMembershipNoticeHost>
          <AppFrame presence={presence} shellLayout={shellLayout} />
        </TenantMembershipNoticeHost>
      </IdentityChangeProvider>
    </RouteProvider>
  )
}

/** Renders the area the current URL lands in: the console or the tenant shell. */
function AppFrame({
  presence,
  shellLayout,
}: {
  readonly presence: RoutePresence
  readonly shellLayout?: AppShellShellLayout
}): ReactElement {
  const { path: pathname } = useLocationPath()
  // The declaration the current URL matches exactly on the narrowed surface:
  // used to tell a live invitation landing from a path that merely looks like
  // one. Absent, pending or failed, no declaration matches and the path falls
  // through to the tenant area's own matching.
  const match = useMatch()
  // The host's identity-change action, handed to the console page so a
  // settled start or end re-reads the identity the way the tenant frame's
  // banner does, plus the start seam: the page calls it before it writes, so
  // reads are held and the session's identity-transition guard is open
  // across the start's cookie rotation window.
  const { applyIdentityChange, onImpersonationStarting } = useIdentityChange()
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
            shellLayout={shellLayout}
            sidebarCollapsed={sidebarCollapsed}
            onSidebarCollapsedChange={setSidebarCollapsed}
          >
            <NotFoundPage path={pathname} />
          </TenantFrame>
        }
      />
    )
  }

  // The anonymous password reset renders outside both shells: the viewer
  // arrives without a session, and the surface needs no tenant context. The
  // declaration carries no feature gate, so RouteView renders it on every
  // deployment; a deployment that publishes no channel answers with the
  // surface's own unavailable notice.
  if (pathname === PASSWORD_RESET_PATH) {
    return <RouteView />
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
        {/* The console area is decided by the effective account's platform
            permission set (ConsoleAreaGate). An impersonating identity is the
            target account, which never holds a platform permission, so the area
            turns it away through the same criterion that turns an ordinary
            member away; no separate impersonation gate runs. The explanation
            belongs to the tenant frame's banner, not to this redirect. */}
        <ConsoleFeatureGate presence={presence}>
          <ConsoleAreaGate>
            {consoleRoutes.length === 0 ? (
              // No console page is served: the URL is the tenant area's,
              // where the gated declarations render the same 404 an
              // unregistered path does.
              <TenantFrame
                presence={presence}
                shellLayout={shellLayout}
                sidebarCollapsed={sidebarCollapsed}
                onSidebarCollapsedChange={setSidebarCollapsed}
              >
                <RouteView />
              </TenantFrame>
            ) : (
              <ConsoleArea
                routes={consoleRoutes}
                hasFeature={hasFeature}
                shellLayout={shellLayout}
                onImpersonationChange={applyIdentityChange}
                onImpersonationStarting={onImpersonationStarting}
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
      </SessionGate>
    )
  }

  return (
    <TenantFrame
      presence={presence}
      shellLayout={shellLayout}
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
 * is, the live subscription opens only when notification.stream is (beside
 * the session fact), and the notification surface's preference half and
 * optional channels follow notification.preferences and
 * notification.channel.email/sms. The frame is also the wrapper the auth
 * callback's absent view renders in, so an absent callback and an unregistered
 * path look the same, inside the same shell.
 */
function TenantFrame({
  presence,
  shellLayout,
  sidebarCollapsed,
  onSidebarCollapsedChange,
  children,
}: {
  readonly presence: RoutePresence
  readonly shellLayout?: AppShellShellLayout
  readonly sidebarCollapsed: boolean
  readonly onSidebarCollapsedChange: (collapsed: boolean) => void
  readonly children: ReactNode
}): ReactElement {
  const navigate = useNavigate()
  // The assembled client: its event-stream opener feeds the notification
  // surface's live subscription below.
  const { api } = useAppServices()
  // The page the frame is rendering. The personal-tenant failure placeholder
  // replaces only the pages that read tenant data; the account-level pages
  // read none and stay usable (src/paths.ts).
  const { path: pathname } = useLocationPath()
  const accountLevel = isAccountLevelPath(pathname)
  // The notification surfaces and the roster are feature-gated; the hooks fail
  // closed, so a pending or failed document opens and reads nothing.
  const inboxServed = useFeature('notification.inbox')
  const streamServed = useFeature('notification.stream')
  // The notification surface's presence facts: whether the preference matrix
  // exists and which optional delivery channels the deployment serves. Both
  // fail closed, so a pending or failed document offers neither.
  const notificationPreferencesServed = useFeature('notification.preferences')
  const emailChannelServed = useFeature('notification.channel.email')
  const smsChannelServed = useFeature('notification.channel.sms')
  const notificationChannels = useMemo<readonly ('email' | 'sms')[]>(
    () => [
      ...(emailChannelServed ? (['email'] as const) : []),
      ...(smsChannelServed ? (['sms'] as const) : []),
    ],
    [emailChannelServed, smsChannelServed],
  )
  const rosterServed = useFeature('tenant.admin')
  // The host's personal-tenant policy: when this feature is in force, an
  // account whose own personal tenant is missing from its membership list asks
  // the server to backfill it, and a registration lands in the tenant area with
  // it (src/pages/sign-in-view.tsx).
  const personalServed = useFeature('workspace.personal')
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
  // The notice the shell renders: the banner in the slot below the app bar,
  // the "impersonating" chip in the user-menu slot, and the account menu's
  // end item all read this one state and one shared end attempt.
  const notice = useImpersonationNoticeState(grant)
  // Whether the feature document is settled at all. `pending`/`failed` keep
  // the current tenant unknown (tenant-scoped queries stay disabled); `ready`
  // lets the roster read (when tenant.admin is served) and the caller tenant
  // list settle it.
  const featuresReady = presence.state === 'ready'
  // The current tenant is a host fact (the server resolves it per request
  // from the request's tenant header). chrome.tsx holds the two values that
  // come off it -- the switcher's identity and the alias the request surface
  // carries -- and this state projects the identity for rendering; the setter
  // writes the cell too, so the value a request announces and the value the
  // chrome shows agree. It starts empty and is settled from the caller tenant
  // list (the personal row, then its first row) or, for platform staff, from
  // the roster's staff default first: until that settles, a tenant-scoped
  // request announces no alias and is refused 404.
  // A frame mounting during an impersonation starts unknown too: the
  // module-level cell still holds the operator's tenant from before the
  // console took over, and a first frame carrying it would issue one read
  // (and open one stream) under the impersonated session and the operator's
  // alias before the status read settles the grant's tenant.
  const [tenantId, setTenantId] = useTenantSelection(
    impersonating ? null : getCurrentTenantId(),
  )
  // The caller's own tenant list: the memberships read the root loads, and
  // the fact the current tenant settles from. A read that has not settled
  // (a failure included) is not an empty answer, so neither the settlement
  // nor the personal-tenant backfill may treat it as "the personal tenant is
  // missing".
  const selfState = useSelfTenantState()
  // The tenant the platform roster's staff default names, when the read
  // answered one. The roster read is a platform management read (staff only),
  // so an ordinary member leaves this null and settles from their own list.
  const [rosterDefaultId, setRosterDefaultId] = useState<string | null>(null)
  // Whether this identity's profile still shows setup incomplete. The home
  // page offers the one-time 「完成账户设置」 notice from this fact alone
  // (src/personal-tenant-notice.tsx): an account opened by a registration or by
  // an ordinary SMS sign-in with a new number reaches it the same way, and
  // finishing setup makes it disappear.
  const personalSetupEligible =
    snapshot.state === 'authenticated' &&
    snapshot.principal !== null &&
    setupIncomplete(snapshot.principal)
  // Whether the roster read has settled (with an answer or a failure). The
  // current-tenant settlement waits for it when the read is part of the
  // assembly.
  const [rosterSettled, setRosterSettled] = useState(false)
  // Whether the current tenant is a SETTLED context rather than an unknown
  // one. A tenant-scoped surface that opens a connection (the notification
  // stream) waits for this: a new account would otherwise open the stream
  // before its personal tenant's backfill completes and be refused 404.
  const [tenantSettled, setTenantSettled] = useState(false)
  // The backfill's retryable failure: the tenant-reading pages then render a
  // retry instead of their content, while the account-level pages keep
  // rendering.
  const [personalFailure, setPersonalFailure] = useState(false)
  // A retry tick: the failure view's control bumps it, which re-runs the
  // backfill effect.
  const [personalRetry, setPersonalRetry] = useState(0)
  // The personal tenant this identity's backfill already answered. The
  // membership re-read after a successful answer may lag the write, and the
  // backfill asks once per missing tenant rather than on every list it does not
  // yet reflect; an identity change clears it.
  const [personalBackfilled, setPersonalBackfilled] = useState<string | null>(null)
  // The identity-change gap. Ending an impersonation re-decides the current
  // tenant from the caller tenant list and the roster, and until those reads
  // land the current tenant is unknown -- the settled facts belong to the
  // ended identity. The switcher would then render its no-current-tenant text,
  // a state a host that settles no tenant at all also renders, for a
  // different reason. The switcher therefore stays out of the bar until the
  // reads land, rather than putting that text against a shell that is only
  // still reading. The edge is detected during render, not in an effect: React
  // re-renders this component before committing, so the very first commit
  // after the snapshot flips already withholds the switcher. The tenant effect
  // below closes the gap once the facts have settled. A mount that does not
  // follow an impersonation is untouched, so a viewer who settles no tenant
  // for another reason (no memberships) keeps the switcher and its documented
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
  // Read the platform roster when the assembly serves it. The read needs
  // tenant.read in the platform domain, which the demo's operator holds but an
  // ordinary member does not; a refused read leaves the alias cache empty and
  // the staff default null, and the current tenant then settles from the
  // caller's own list. The roster is also the name source for a staff member
  // entering a tenant they are not a member of, so its aliases are kept in the
  // module cache. It is a platform management read, refused to an impersonated
  // identity, so the impersonation branch reads nothing.
  useEffect(() => {
    if (!authenticated || impersonating || !featuresReady || !rosterServed) {
      setRosterSettled(false)
      setRosterDefaultId(null)
      return undefined
    }
    let cancelled = false
    setRosterSettled(false)
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
        // The staff default is the first active tenant that is not someone's
        // personal tenant: the roster view carries no kind, so the server's
        // identifier prefix tells them apart. A personal tenant belongs to one
        // account and answers a staff viewer 404; it also sorts before most
        // deployment identifiers ('p' < 't', 'z'), so without this the first
        // active row would often be a stranger's personal space.
        setRosterDefaultId(defaultRosterTenant(answer.tenants))
        setRosterSettled(true)
      })
      .catch(() => {
        if (cancelled) {
          return
        }
        // A refusal or failure says nothing about membership: the read settles
        // with no staff default and no aliases, and the current tenant falls
        // back to the caller's own list.
        setRosterDefaultId(null)
        setRosterSettled(true)
      })
    return () => {
      cancelled = true
    }
    // The identity is an input: after an impersonation ends the roster is
    // read again, and after a start the branch above keeps it out.
  }, [authenticated, impersonating, featuresReady, rosterServed, principalId])

  // Settle the current tenant from the caller's own tenant list and the
  // roster's staff default. The list is the membership fact; the roster (staff
  // only) names the first active non-personal tenant. The order: a current
  // tenant the host can name is kept (a switch, a route that named one, or a
  // previous settlement); then the roster's staff default; then the account's
  // personal row; then the list's first row; otherwise the current tenant
  // stays unknown and tenant-scoped reads stay disabled. A list that has not
  // settled -- and left no rows on hand -- is not an empty answer, so nothing
  // is settled from it.
  useEffect(() => {
    if (!authenticated) {
      // The root already cleared the module-level cells; this keeps the
      // frame's own state in step while it stays mounted (the ending
      // session keeps rendering the sign-in surface until it flips).
      setTenantId(null)
      setTenantSettled(false)
      return
    }
    setTenantSettled(false)
    if (impersonating) {
      // The grant names the tenant by identifier; the request surface
      // announces it by ALIAS, and a tenant-scoped read that announces none is
      // refused 404 by the admission layer. The impersonated account's own
      // membership list is that alias source -- the roster is a platform read
      // an impersonated identity cannot make -- and the server narrows it to
      // the grant's tenant. The current tenant therefore settles only once a
      // source names the grant's tenant; until then it stays unknown and
      // tenant-scoped reads stay disabled, rather than going out under a name
      // the request surface cannot announce.
      const granted = grant?.tenant ?? null
      const alias =
        granted === null
          ? null
          : (getRecordedTenantAlias(granted) ?? selfTenantAlias(granted))
      setTenantId(alias === null ? null : granted)
      setTenantSettled(alias !== null)
      return
    }
    if (!featuresReady) {
      // A pending or failed document never stands in for "no tenant": the
      // current tenant stays unknown.
      setTenantId(null)
      setIdentitySettling(false)
      return
    }
    // Keep a current tenant the host can name: a switch, a route that named
    // one, or a previous settlement. It must be nameable -- a row of the list
    // or a roster alias -- or the caller would sit in a tenant the bar cannot
    // show and the switcher cannot leave.
    const current = getCurrentTenantId()
    if (
      !identitySettling &&
      current !== null &&
      tenantNameable(current, selfState)
    ) {
      setTenantId(current)
      setTenantSettled(true)
      return
    }
    if (rosterServed && !rosterSettled) {
      // The staff default is decided by a read still in flight. The
      // identity-change gap stays open too: the switcher stays out of the bar
      // rather than stating "no current tenant" while the host is only still
      // reading.
      setTenantId(null)
      return
    }
    // A failure keeps the rows already settled, so the list stays usable; a
    // failure with no rows on hand is not an empty answer.
    const listUsable = selfState.settled || selfState.tenants.length > 0
    if (!listUsable) {
      setTenantId(null)
      // A read that has not answered yet has not decided the tenant either.
      // The identity-change gap stays open until the caller tenant read has
      // an outcome, or the switcher would state "no current tenant" against a
      // host that is only still reading.
      if (!selfState.failed) {
        return
      }
      setIdentitySettling(false)
      return
    }
    const personalId =
      principalId === '' ? null : personalTenantId(principalId)
    const personalRow =
      personalId !== null &&
      selfState.tenants.some((tenant) => tenant.id === personalId)
        ? personalId
        : null
    const next =
      (rosterServed ? rosterDefaultId : null) ??
      personalRow ??
      selfState.tenants[0]?.id ??
      null
    setTenantId(next)
    setTenantSettled(next !== null)
    setIdentitySettling(false)
  }, [
    authenticated,
    impersonating,
    featuresReady,
    rosterServed,
    rosterSettled,
    rosterDefaultId,
    selfState,
    principalId,
    grant?.tenant,
    identitySettling,
    setTenantId,
  ])

  // Reset the personal-tenant facts the moment the identity they belong to
  // changes: a started or ended impersonation swaps the effective account, and
  // a decision taken for the previous identity must not carry over.
  useEffect(() => {
    setPersonalFailure(false)
    setPersonalBackfilled(null)
  }, [principalId, generation, impersonating])

  // The router's navigate callback changes identity with the location, so an
  // effect that depended on it would re-run on every route change. The effect
  // reads the latest callback through a ref instead.
  const navigateRef = useRef(navigate)
  useEffect(() => {
    navigateRef.current = navigate
  }, [navigate])

  // The personal-tenant backfill. It asks the server only when the caller's own
  // personal tenant is missing from its settled membership list:
  // authenticated, not impersonating, the workspace.personal policy served, the
  // caller tenant list settled, and the identifier derived from the principal
  // absent from that list. A failed list read is not a missing tenant, so it
  // never triggers. The list's other rows are irrelevant: an account in
  // business tenants still owns its personal tenant.
  //
  // The server decides the answer: 200 records the tenant's alias before it is
  // announced (no roster read carries it), makes it the current tenant when
  // none is settled, and re-reads the list so the switcher gets the new row from
  // the membership source; every failure is the tenant area's retryable
  // failure.
  useEffect(() => {
    if (!authenticated || impersonating || !featuresReady || !personalServed) {
      return undefined
    }
    if (principalId === '' || !selfState.settled) {
      return undefined
    }
    const personalId = personalTenantId(principalId)
    if (selfState.tenants.some((tenant) => tenant.id === personalId)) {
      return undefined
    }
    if (personalBackfilled === personalId) {
      return undefined
    }
    // When the assembly reads the roster, let that read settle first: the staff
    // default it names is the current tenant it settles, and the backfill must
    // not claim that place before the read has had its say.
    if (rosterServed && !rosterSettled) {
      return undefined
    }
    let cancelled = false
    setPersonalFailure(false)
    void Promise.resolve()
      .then(() => workspacePersonalEnsure())
      .then((answer) => {
        if (cancelled) {
          return
        }
        setPersonalBackfilled(answer.tenant.id)
        setTenantAlias(answer.tenant.id, answer.tenant.alias)
        // Read the cell, not this render's state: the settlement effect above
        // runs earlier in the same commit and writes it synchronously, so a
        // tenant it just settled (the roster's staff default, for one) is
        // already visible here even though React has not re-rendered.
        if (tenantId === null && getCurrentTenantId() === null) {
          setTenantId(answer.tenant.id)
          setTenantSettled(true)
        }
        // Read the membership list again: the new space is a membership, and
        // the switcher must get its row from that source, not from this
        // answer.
        void loadSelfTenants()
      })
      .catch(() => {
        if (cancelled) {
          return
        }
        setPersonalFailure(true)
      })
    return () => {
      cancelled = true
    }
  }, [
    authenticated,
    impersonating,
    featuresReady,
    personalServed,
    selfState,
    rosterServed,
    rosterSettled,
    tenantId,
    principalId,
    generation,
    personalRetry,
    personalBackfilled,
    setTenantId,
  ])
  // The switcher's rows: every member tenant from the caller tenant read, in
  // its (identifier) order, plus the current tenant when the list does not
  // hold it -- a platform staff member entering a tenant they are not a member
  // of. The added row is nameable through the roster's alias cache; when the
  // cache has no alias either, the row is not added and the trigger shows its
  // no-current-tenant state (the documented limitation).
  const menuTenants: DemoTenant[] = selfState.tenants.map((tenant) => ({
    id: tenant.id,
    alias: tenant.alias,
    name: tenant.alias,
  }))
  if (tenantId !== null && !menuTenants.some((row) => row.id === tenantId)) {
    const alias = getRecordedTenantAlias(tenantId)
    if (alias !== null) {
      menuTenants.push({ id: tenantId, alias, name: alias })
    }
  }
  const callerPersonalTenantId =
    principalId === '' ? null : personalTenantId(principalId)

  const items = useNavItems()
  const consoleRoutes = presentDeclarations(consoleShellRoutes, presence)

  // The tenant navigation is every labelled entry outside the console area
  // (the console's pages are filtered out above, so nothing else points into
  // the console). The console entry is deliberately not a navigation item: it
  // lives in the header's action slot, offered only to platform staff, so a
  // member meets a navigation with no way into the console. The entry's
  // href/onClick pair is route-kit's -- a plain click navigates in place while
  // a modified or new-tab click stays with the browser -- and its icon is the
  // one its declaration carries (src/routes.tsx), looked up by the entry id
  // that declaration path supplies.
  const navItems: readonly AppShellNavItem[] = items
    .filter((item) => !isConsolePath(item.id))
    .map((item): AppShellNavItem => ({
      id: item.id,
      label: item.label,
      icon: navIconFor(item.id),
      href: item.href,
      selected: item.selected,
      onClick: item.onClick,
    }))

  return (
    <TenantProvider tenantId={tenantId}>
      {/* The live notification subscription: it opens the api client's
          event-stream seam (the assembled client's openEventStream) for
          the acting tenant and re-reads the unread count and message list
          on every pushed frame. Mounted here, inside the tenant area,
          because the notification endpoints are tenant-scoped. It opens only
          once the session is authenticated, the process serves
          notification.stream, AND the tenant context has settled: a new
          account's personal tenant arrives through the ensure, and a
          connection opened before it settles would announce no alias and be
          refused. An absent feature would otherwise open a
          connection the server must refuse with a bare 404 on every mount,
          identity change and tab return. */}
      <NotificationStreamProvider
        openStream={api.openEventStream}
        enabled={authenticated && streamServed && tenantSettled}
        identityGeneration={generation}
      >
        <ImpersonationNoticeProvider
          impersonation={notice.impersonation}
          identityKey={notice.identityKey}
          onEnd={notice.onEnd}
          pending={notice.pending}
          failureCode={notice.failureCode}
          successMessage={notice.successMessage}
        >
        <ProductShell
          navItems={navItems}
          shellLayout={shellLayout}
          header={<AppBrand />}
          // The impersonation notice sits below the app bar for the whole
          // tenant frame. It is always mounted: the component tells the
          // shell when there is nothing to show, and its live region keeps
          // the end confirmation after the banner itself goes away.
          banner={
            <>
              <ImpersonationBannerSlot />
              {/* The registration-verification notice, below the app bar for
                  the whole tenant frame. It renders only while the profile's
                  verification state is pending, and stays until the address
                  is proved (no close control). */}
              <ContactVerificationBanner />
            </>
          }
          // The notification bell rides the header actions slot beside the
          // shared chrome; it opens the notification surface. It lives only
          // in the tenant area (the console resolves no tenant, so its bell
          // would have nothing to read) and only when notification.inbox is
          // served: a deployment without it has no destination for the bell.
          // The console entry sits at the same end of the bar, nearest the
          // account controls, and shows only to platform staff (ConsoleEntry
          // gates itself).
          headerActions={(rail) => (
            <>
              {inboxServed ? (
                <NotificationBell
                  onClick={() => navigate(NOTIFICATIONS_PATH)}
                />
              ) : null}
              <HeaderActions />
              <ConsoleEntry routes={consoleRoutes} narrow={rail.narrow} />
            </>
          )}
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
          // the tenant again from the caller tenant list and the roster, and
          // the switcher returns over the settled row; until those reads land
          // the identity-change gap keeps it out of the bar for the same
          // reason -- the host has no tenant to show yet, and its
          // no-current-tenant text belongs to a host that settles none.
          userMenu={(rail) => (
            <>
              {impersonating || identitySettling ? null : (
                <TenantMenu
                  tenants={menuTenants}
                  tenantId={tenantId}
                  personalTenantId={callerPersonalTenantId}
                  onTenantSelect={setTenantId}
                  compact={rail.narrow}
                />
              )}
              {/* The chip sits ahead of the account menu: while an
                  impersonation is in force it is the persistent visible
                  indication, and it toggles the notice (its close control
                  moves focus here). */}
              <ImpersonationChip narrow={rail.narrow} />
              <UserMenu narrow={rail.narrow} />
            </>
          )}
          signIn={<SignInView />}
        >
          <PersonalTenantNoticeProvider
            key={`${principalId}:${generation}`}
            eligible={personalSetupEligible}
          >
            {/* The notification surface's feature-presence facts: the
                preference matrix and the optional delivery channels. They
                wrap the routed pages, where the notification surface reads
                them; an absent fact hides its surface (fail closed). */}
            <NotificationPresenceProvider
              preferences={notificationPreferencesServed}
              channels={notificationChannels}
            >
              <Box sx={{ p: 3 }}>
                {/* The retryable failure replaces only the pages that read
                    tenant data. The account-level pages (the account settings
                    page, the setup page, the binding callback) read none, so
                    they keep rendering while the ensure retries -- otherwise a
                    sustained failure would lock a new account out of adding a
                    username, setting a password, or revoking a session. */}
                {personalFailure && tenantId === null && !accountLevel ? (
                  <TenantProvisionFailure
                    onRetry={() => setPersonalRetry((current) => current + 1)}
                  />
                ) : (
                  children
                )}
              </Box>
            </NotificationPresenceProvider>
          </PersonalTenantNoticeProvider>
        </ProductShell>
        </ImpersonationNoticeProvider>
      </NotificationStreamProvider>
    </TenantProvider>
  )
}

/**
 * The header's console entry: a link into the first console page the principal
 * may open, shown only to a principal that holds a platform permission and
 * only while at least one console declaration is in force.
 *
 * The target comes from useConsoleTarget (src/console-routes.ts): the first
 * labelled declaration, in table order, with no permission code or one the
 * effective account's platform permission set holds. An impersonating identity
 * gets the effective account's answer, which for a real target is null because
 * a target holds no platform permission. The set is not known synchronously --
 * the host attaches it once the caller's platform permissions have been read,
 * and useAuthState reads null until then -- so the entry is hidden until the set
 * lands, which is the safe direction: a member never sees the entry for the
 * moment before the set arrives. The start page's console card calls the same
 * hook, so the two never disagree.
 *
 * It routes in place (the same seam every other entry uses), so entering the
 * console does not reload the page and sign the operator out, and it keeps its
 * href so a middle-click or a copied URL still reaches the console.
 */
function ConsoleEntry({
  routes,
  narrow = false,
}: {
  /** The console declarations the presence predicate left in force. */
  readonly routes: readonly ConsoleRoute[]
  /** Whether the entry renders in the collapsed sidebar rail, where only an
   * icon fits; the accessible name stays the entry's label. */
  readonly narrow?: boolean
}): ReactElement | null {
  // The console entry follows the effective account's platform set alone; an
  // impersonating identity gets the same derivation as any other viewer.
  const target = useConsoleTarget(routes)
  if (target === null) {
    return null
  }
  // The link itself renders in its own component: the href/onClick pair is a
  // hook call, and hooks cannot follow the null branch above.
  return <ConsoleEntryLink target={target} narrow={narrow} />
}

/** The console entry's anchor, with route-kit's basename-aware href and its
 * plain-click-navigates / modified-click-stays-with-the-browser handler. */
function ConsoleEntryLink({
  target,
  narrow,
}: {
  /** The console page the entry points at (a route, not yet a served URL). */
  readonly target: string
  /** Icon form for the collapsed rail. */
  readonly narrow: boolean
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const { href, onClick } = useRouteLinkProps(target)
  if (narrow) {
    return (
      <IconButton
        color="inherit"
        aria-label={t('nav.console')}
        href={href}
        onClick={onClick}
      >
        <ConsoleNavIcon size={20} />
      </IconButton>
    )
  }
  return (
    <Button color="inherit" href={href} onClick={onClick}>
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
    // The console URL is the viewer's destination: a registration completed
    // here stays on it instead of being taken to the tenant home or the
    // setup page (the register mode's landing belongs to the app's own
    // sign-in page).
    return <SignInView keepLanding />
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
 * Settles a current tenant for the console area when none is settled.
 *
 * The caller's permission read (src/platform-permissions.tsx) sits on the API
 * endpoint, which the tenant resolver guards: a request that names no tenant
 * is refused 404 before any handler runs. The tenant frame settles the current
 * tenant for the tenant area, but an operator who reaches a console URL
 * directly -- a reload of a console page, a bookmark -- mounts no tenant
 * frame, and without a name the permission read is refused, the platform set
 * attaches as the fail-closed empty one, and the area gate turns the operator
 * away. The roster read settles the same default the tenant frame would.
 *
 * The number is the request surface's only use here: the console renders no
 * tenant and has no switcher. The roster is part of the assembly only when the
 * feature document serves tenant.admin. A deployment that does not serve it has
 * no staff default to read, so the caller tenant list's first row settles the
 * name -- the only source the request surface can announce -- and the area then
 * decides by the platform permission set. When the roster is served, a refused
 * or failed read during an impersonation falls back to the caller tenant list:
 * the server narrows it to the grant, and without it the target's platform set
 * could never be read. A member's refused roster read settles nothing: the
 * caller list names their tenant, but reading the empty platform set from it
 * would turn a full-page console load into a redirect the invitation journey
 * does not document, and a tenant the request surface cannot name keeps the area
 * waiting.
 */
function useConsoleTenantSettle(
  authenticated: boolean,
  impersonating: boolean,
  rosterServed: boolean,
): void {
  const tenantId = useCurrentTenantId()
  const selfState = useSelfTenantState()
  useEffect(() => {
    if (!authenticated || tenantId !== null) {
      return undefined
    }
    if (!rosterServed) {
      // No roster is assembled, so no read is spent on it. The caller list's
      // first row is the name the permission read can carry; until it lands the
      // tenant stays unknown and the effect runs again.
      const row = selfState.tenants[0]
      if (row !== undefined) {
        setTenantAlias(row.id, row.alias)
        setCurrentTenantId(row.id)
      }
      return undefined
    }
    let cancelled = false
    // Deferred into a promise: the generated operation throws synchronously
    // when no request function is bound (a suite that mounts the area alone).
    void Promise.resolve()
      .then(() => tenantAdminTenantsList())
      .then((answer) => {
        if (cancelled || getCurrentTenantId() !== null) {
          return
        }
        // The selected row's alias is the name the permission read announces:
        // a roster tenant outside the switcher's default list has no other
        // source for it.
        setTenantAliases(
          new Map(answer.tenants.map((tenant) => [tenant.id, tenant.alias])),
        )
        setCurrentTenantId(defaultRosterTenant(answer.tenants))
      })
      .catch(() => {
        // The roster is refused or failed. Only an impersonating identity
        // settles from the caller tenant list, whose answer the server narrows
        // to the grant; a member's list names their tenant but the area stays
        // on the wait, as a member's full-page console load documents. An
        // empty or not-yet-read list leaves the tenant undecided, and the
        // effect runs again when it lands.
        if (!impersonating || cancelled || getCurrentTenantId() !== null) {
          return
        }
        const row = selfState.tenants[0]
        if (row === undefined) {
          return
        }
        setTenantAlias(row.id, row.alias)
        setCurrentTenantId(row.id)
      })
    return () => {
      cancelled = true
    }
  }, [authenticated, impersonating, rosterServed, tenantId, selfState])
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
  // Whether the deployment assembles the roster at all. A deployment without
  // tenant.admin settles the current tenant from the caller list instead; the
  // area still decides by the platform permission set.
  const rosterServed = useFeature('tenant.admin')
  useConsoleTenantSettle(
    snapshot.state === 'authenticated',
    snapshot.impersonating,
    rosterServed,
  )
  // An account whose address is still unproved never settles a tenant: the
  // personal tenant's ensure refuses a pending account and the roster is a
  // staff-only read, so the permission set below is never read and the area
  // would wait forever. The verification page is the one place that lifts the
  // state, so a pending viewer who reaches the console is sent there instead.
  const profile = useAuthnProfileGet({
    query: { enabled: snapshot.state === 'authenticated' },
  })
  const status = consoleAreaStatus(snapshot)
  if (status === 'granted') {
    return <>{children}</>
  }
  if (status === 'denied') {
    return <TenantHomeRedirect />
  }
  if (contactVerificationPending(profile.data ?? {})) {
    return <ContactVerificationRedirect />
  }
  return <ConsoleAccessPending />
}

/**
 * Replaces the current history entry with the tenant home, rendered instead
 * of a surface the current identity may not use. It navigates from an effect
 * (route-kit's useRedirect), which is where the router wants a render-driven
 * move, and renders nothing: the gates that mount it render no console chrome
 * on the way.
 */
function TenantHomeRedirect(): null {
  const redirect = useRedirect()
  useEffect(() => {
    redirect(TENANT_HOME_PATH)
  }, [redirect])
  return null
}

/**
 * Moves a viewer whose address is still unproved to the account page's
 * verification section, which is where a registration already lands and the
 * only place the state can be lifted. Renders nothing on the way, like the
 * redirect above.
 */
function ContactVerificationRedirect(): null {
  const redirect = useRedirect()
  useEffect(() => {
    redirect(`${ACCOUNT_PATH}?verify=1`)
  }, [redirect])
  return null
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

/**
 * The tenant area's retryable failure while the personal tenant could not be
 * backfilled. It takes
 * the place of the pages that read tenant data, because no tenant is current
 * and their tenant-scoped reads cannot answer; retrying the ensure is the one
 * action that can settle them. The account-level pages read no tenant data
 * and are rendered instead of this view (src/app.tsx).
 */
function TenantProvisionFailure({
  onRetry,
}: {
  readonly onRetry: () => void
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Box
      component="section"
      role="alert"
      sx={{ display: 'flex', flexDirection: 'column', gap: 2, maxWidth: 640 }}
    >
      <Typography variant="h5" component="h2">
        {t('workspace.failure.title')}
      </Typography>
      <Typography variant="body1">
        {t('workspace.failure.description')}
      </Typography>
      <Box>
        <Button onClick={onRetry}>{t('workspace.failure.retry')}</Button>
      </Box>
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
