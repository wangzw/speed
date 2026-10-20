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

import { useEffect, useMemo, useState } from 'react'
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
import { ImpersonationNoticeProvider } from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import { RouteGuard } from '@speed/layout-kit'
import type { AppShellNavItem, RouteGuardStatus } from '@speed/layout-kit'
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
import { httpStatusOf } from '@speed/ui-kit'
import type { ReactElement, ReactNode } from 'react'
import { setupIncomplete } from './account-setup.js'
import { tenantAdminTenantsList, workspacePersonalEnsure } from './app-api/index.js'
import { useAppServices } from './app-services.js'
import {
  AppBrand,
  DEFAULT_TENANT_ID,
  DEMO_TENANTS,
  getCurrentTenantId,
  HeaderActions,
  setCurrentTenantId,
  setTenantAlias,
  setTenantAliases,
  TenantMenu,
  UserMenu,
  useTenantSelection,
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
  ImpersonationChip,
  useIdentityChange,
  useImpersonationNoticeState,
  useImpersonationStatus,
} from './impersonation.js'
import { DemoNavLabel } from './nav-label.js'
import { PersonalTenantNoticeProvider } from './personal-tenant-notice.js'
import { AcceptInvitationPage } from './pages/accept-invitation-page.js'
import { AuthCallbackPage } from './pages/auth-callback-page.js'
import {
  NotFoundPage,
  TENANT_HOME_PATH,
} from './pages/not-found-page.js'
import { SignInView } from './pages/sign-in-view.js'
import { isAccountLevelPath } from './paths.js'
import { DEMO_NAMESPACE } from './resources.js'
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
 * The server's personal-tenant identifier prefix (starters/demo/server's
 * personal.go, personalIDPrefix). The roster's tenant view carries no kind,
 * so the host tells a personal tenant from the identifier the server
 * derives. The two spellings must stay in sync by hand.
 */
const PERSONAL_TENANT_ID_PREFIX = 'personal-'

/** The app view root: routing installed once, over the shell switch. The
 * route table is gated (the notification, object and console declarations
 * carry `requires`), so the host hands the route layer its feature-presence
 * answer -- derived from the session's feature state -- plus the views a
 * gated path renders while that answer is pending or failed. */
export function DemoApp(): ReactElement {
  const presence = featurePresence(useFeatureState())
  const authenticated = useAuthState().state === 'authenticated'
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
 * is, the live subscription opens only when notification.stream is (beside
 * the session fact), and the notification surface's preference half and
 * optional channels follow notification.preferences and
 * notification.channel.email/sms. The frame is also the wrapper the auth
 * callback's absent view renders in, so an absent callback and an unregistered
 * path look the same, inside the same shell.
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
  // surface's live subscription below. The session is read at the ensure
  // answer, where its authenticated snapshot is the GET /api/v1/auth/me read that
  // says whether setup is still incomplete.
  const { api, session } = useAppServices()
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
  // account with no current tenant is given one by the server's ensure, and a
  // registration lands in the tenant area with it (src/pages/sign-in-view.tsx).
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
  const [tenantId, setTenantId] = useTenantSelection(
    impersonating ? null : getCurrentTenantId(),
  )
  // The personal tenant the server ensured, held for the switcher's label: the
  // tenant record carries no display name, so the host renders the bilingual
  // 「个人空间 / Personal workspace」 text from it (src/chrome.tsx).
  const [personalTenant, setPersonalTenant] = useState<{
    readonly id: string
    readonly alias: string
  } | null>(null)
  // Whether this identity's ensure answer reported that it had just created
  // the account's personal tenant while the profile still shows setup
  // incomplete. The home page turns that fact into its one-time
  // 「完成账户设置」 notice (src/personal-tenant-notice.tsx); an ordinary SMS
  // sign-in with a new number reaches it exactly like a registration does.
  const [personalSetupEligible, setPersonalSetupEligible] = useState(false)
  // Whether the roster read has settled, so the ensure below never races it:
  // an account that already has a current tenant must not trigger.
  const [rosterSettled, setRosterSettled] = useState(false)
  // Whether the current tenant is a SETTLED context rather than the
  // placeholder fallback. A tenant-scoped surface that opens a connection
  // (the notification stream) waits for this: the placeholder's alias names
  // no tenant the caller belongs to, and a new account would otherwise open
  // the stream before its personal tenant's ensure completes and be refused
  // 404.
  const [tenantSettled, setTenantSettled] = useState(false)
  // The ensure's retryable failure (a 503, or any failure that is not the
  // 409 already-member): the tenant-reading pages then render a retry
  // instead of their content, while the account-level pages keep rendering.
  const [personalFailure, setPersonalFailure] = useState(false)
  // A retry tick: the failure view's control bumps it, which re-runs the
  // ensure effect.
  const [personalRetry, setPersonalRetry] = useState(0)
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
      // The root already cleared the module-level cells; this keeps the
      // frame's own state in step while it stays mounted (the ending
      // session keeps rendering the sign-in surface until it flips).
      setTenantId(null)
      setRosterSettled(false)
      setTenantSettled(false)
      return
    }
    // The tenant is unknown until this read settles it; only the branches
    // below that produce a real context raise the flag.
    setTenantSettled(false)
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
      setRosterSettled(true)
      setTenantSettled(grant?.tenant != null)
      return
    }
    if (!featuresReady) {
      // A pending or failed document never stands in for "tenant.admin
      // absent": the current tenant stays unknown.
      setTenantId(null)
      setRosterSettled(false)
      setIdentitySettling(false)
      return
    }
    if (!rosterServed) {
      // No roster to read: the frame falls back to the placeholder, which
      // names no tenant the caller is known to belong to. It is what the
      // frame renders, not a settled tenant context.
      setTenantId(DEFAULT_TENANT_ID)
      setRosterSettled(true)
      setIdentitySettling(false)
      return
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
        // identifier prefix tells them apart. A personal tenant belongs to
        // one account and answers a staff viewer 404; it also sorts before
        // most deployment identifiers ('p' < 't', 'z'), so without this the
        // first active row would often be a stranger's personal space. The
        // default must also be a tenant the switcher offers: the switcher
        // lists the placeholder's tenants alone, so a row outside that list
        // would leave the caller in a tenant the bar cannot name and the
        // switcher cannot leave, with every tenant-scoped request refused.
        const first = answer.tenants.find(
          (tenant) =>
            tenant.status === 'active' &&
            !tenant.id.startsWith(PERSONAL_TENANT_ID_PREFIX) &&
            DEMO_TENANTS.some((option) => option.id === tenant.id),
        )
        setTenantId(first?.id ?? DEFAULT_TENANT_ID)
        setRosterSettled(true)
        setTenantSettled(true)
        setIdentitySettling(false)
      })
      .catch((error: unknown) => {
        if (cancelled) {
          return
        }
        // Only a 403 means the ordinary member the design names: the roster
        // is a platform management read, so a member without tenant.read is
        // refused it. Their lack of a current tenant is what the ensure
        // below fills. Every other failure -- a 5xx, a network error, a
        // timeout, a 401 -- says nothing about membership and must not
        // create a personal tenant: it falls back to the placeholder, as the
        // frame does when the feature is off.
        setTenantId(
          personalServed && httpStatusOf(error) === 403
            ? null
            : DEFAULT_TENANT_ID,
        )
        setRosterSettled(true)
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
    personalServed,
    impersonating,
    principalId,
    grant?.tenant,
    setTenantId,
  ])

  // Reset the personal tenant the moment the identity it belongs to changes:
  // a started or ended impersonation swaps the effective account, and a
  // tenant the previous identity ensured must not label the new one.
  useEffect(() => {
    setPersonalTenant(null)
    setPersonalFailure(false)
    setPersonalSetupEligible(false)
  }, [principalId, generation, impersonating])

  // The personal ensure. It runs only when every eligibility fact the server
  // will check holds here too: an authenticated session that is not
  // impersonating, a settled feature document that serves workspace.personal,
  // a settled roster read, and no current tenant. An account that already has
  // one -- platform staff whose roster read answered, or a viewer who picked
  // one -- never calls it.
  //
  // The server decides the answer: 200 sets the personal tenant as the
  // current one (the alias is recorded before it is announced, because no
  // roster read carries it); 409 already-member leaves the account with no
  // personal tenant and the frame falls back to the placeholder, whose
  // tenant-scoped reads the admission layer refuses with the 「无法访问当前
  // 组织」 state; every other failure is the tenant area's retryable failure.
  useEffect(() => {
    if (!authenticated || impersonating || !featuresReady || !personalServed) {
      return undefined
    }
    if (!rosterSettled || tenantId !== null) {
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
        setTenantAlias(answer.tenant.id, answer.tenant.alias)
        setPersonalTenant({ id: answer.tenant.id, alias: answer.tenant.alias })
        setTenantId(answer.tenant.id)
        // The personal tenant is a real context the caller is a member of.
        setTenantSettled(true)
        // `created` says this call registered the tenant, so the account is
        // new to password sign-in until it adds a login name and a password.
        // The profile is the GET /api/v1/auth/me read the session committed with
        // the login; it is what makes the home page offer the one-time
        // 「完成账户设置」 notice, whether the account came from the register
        // entry or from an ordinary SMS sign-in with a new number.
        const current = session.getSnapshot()
        if (
          answer.created &&
          current.state === 'authenticated' &&
          current.principal !== null &&
          setupIncomplete(current.principal)
        ) {
          setPersonalSetupEligible(true)
        }
      })
      .catch((error: unknown) => {
        if (cancelled) {
          return
        }
        if (httpStatusOf(error) === 409) {
          // already-member: the account belongs to another tenant. It gets no
          // personal one, and the placeholder tenant is what makes its reads
          // fire and render the admission refusal.
          setPersonalTenant(null)
          setTenantId(DEFAULT_TENANT_ID)
          setTenantSettled(false)
          setIdentitySettling(false)
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
    rosterSettled,
    tenantId,
    principalId,
    generation,
    personalRetry,
    session,
    setTenantId,
  ])
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
          connection opened before it settles would carry the placeholder's
          alias and be refused. An absent feature would otherwise open a
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
          header={<AppBrand />}
          // The impersonation notice sits below the app bar for the whole
          // tenant frame. It is always mounted: the component tells the
          // shell when there is nothing to show, and its live region keeps
          // the end confirmation after the banner itself goes away.
          banner={<ImpersonationBannerSlot />}
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
                <TenantMenu
                  tenantId={tenantId}
                  personalTenant={personalTenant}
                  onTenantSelect={setTenantId}
                />
              )}
              {/* The chip sits ahead of the account menu: while an
                  impersonation is in force it is the persistent visible
                  indication, and it toggles the notice (its close control
                  moves focus here). */}
              <ImpersonationChip />
              <UserMenu />
            </>
          }
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
  // An impersonated session has no console to enter; the shared derivation
  // carries that rule for the header entry and the start page's card alike.
  const target = useConsoleTarget(routes)
  if (target === null) {
    return null
  }
  // The link itself renders in its own component: the href/onClick pair is a
  // hook call, and hooks cannot follow the null branch above.
  return <ConsoleEntryLink target={target} />
}

/** The console entry's anchor, with route-kit's basename-aware href and its
 * plain-click-navigates / modified-click-stays-with-the-browser handler. */
function ConsoleEntryLink({
  target,
}: {
  /** The console page the entry points at (a route, not yet a served URL). */
  readonly target: string
}): ReactElement {
  const { href, onClick } = useRouteLinkProps(target)
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
    return <TenantHomeRedirect />
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
    return <TenantHomeRedirect />
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
 * ensured (a 503, or any answer that is not the 409 already-member). It takes
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
