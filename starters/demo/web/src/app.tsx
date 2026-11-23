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
 *    machine, with the nav derived from every non-admin entry and the host's
 *    admin entry -- offered only to platform staff -- in the header's action
 *    slot, plus the auth-ui sign-in surface and sign-out action in the host
 *    slots). The shell renders the nav and content only while the session is
 *    authenticated; a fresh visitor meets the sign-in surface, and one whose
 *    session ends meets the ended screen. The routed page sits in a padded
 *    content region so it does not butt against the chrome.
 *
 *  - The admin area renders @speed/admin-ui's frame instead, with no tenant
 *    frame around it. The area is chosen by the path prefix alone: a URL under
 *    ADMIN_PREFIX enters the admin area whatever the feature document says,
 *    so a deep link or refresh renders the admin's own pending/failed views
 *    rather than the tenant frame. It carries the same session gate the tenant
 *    shell does (the admin frame is not ProductShell), a feature-state gate
 *    (pending and failed render frameless), an area gate over the platform
 *    permission set (a viewer who holds no platform permission never enters the
 *    shell), and a host-composed route guard over each page's permission code.
 *    When no admin declaration is left in force the URL falls through to the
 *    tenant area, where it is a 404 like any other unregistered path.
 *
 * A URL no declaration matches renders the fallback page inside the tenant area.
 * The auth callback route (a provider's round trip) renders outside both frames,
 * with no session gate: it lands before a session exists and finishes the flow
 * itself. The invitation landing, too, renders outside both frames and carries
 * no feature gate, because an invitee arrives before any session exists and the
 * feature document is unreadable to an anonymous caller.
 */

import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'
import {
  TenantProvider,
  useAuthState,
  useFeature,
  useFeatureState,
  usePermission,
} from '@speed/auth-core'
import { featurePresence } from '@speed/auth-core'
import type { AuthSnapshot } from '@speed/auth-core'
import { ACCOUNT_UI_NAMESPACE } from '@speed/account-ui'
import { ImpersonationNoticeProvider } from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import { RouteGuard } from '@speed/layout-kit'
import type {
  AppShellNavItem,
  AppShellShellLayout,
  RouteGuardStatus,
} from '@speed/layout-kit'
import {
  NotificationPresenceProvider,
  NotificationStreamProvider,
} from '@speed/notification-ui'
import {
  OrganizationAdminProvider,
  organizationAdminOperations,
} from '@speed/admin-ui'
import { ProductShell } from '@speed/product-shell'
import {
  ActionButton,
  DisplayTimeZoneProvider,
  focusPageLanding,
  useAppThemeMode,
  WriteFeedbackPageProvider,
} from '@speed/ui-kit'
import {
  presentDeclarations,
  RouteProvider,
  RouteView,
  useLocationPath,
  useMatch,
  useNavigate,
  useNavItems,
  useRedirect,
} from '@speed/route-kit'
import type { RouteContext, RoutePresence } from '@speed/route-kit'
import type { ReactElement, ReactNode } from 'react'
import { contactVerificationPending } from './contact-verification.js'
import {
  focusFellWithPageSwap,
  isPageHeading,
} from './focus-handoff.js'
import {
  tenantAdminTenantsList,
  useAuthnProfileGet,
} from './app-api/index.js'
import { useAppServices } from './app-services.js'
import {
  AppBrand,
  getCurrentTenant,
  getRecordedTenantName,
  getRecordedTenantDisplayName,
  getRecordedTenantKind,
  persistColorModePreference,
  setCurrentTenant,
  setTenantNames,
  TenantRosterFailure,
  UserMenu,
  AccountEntry,
  useCurrentTenant,
  useTenantSelection,
} from './chrome.js'
import type {
  AccountMenuDestination,
  AccountMenuOrganizations,
  DemoTenant,
} from './chrome.js'
import {
  accountEntryInNavFooter,
  accountEntryInUserSlot,
} from './account-entry.js'
import { AdminArea } from './admin/admin-area.js'
import {
  adminShellRoutes,
  isAdminPath,
  useAdminTarget,
} from './admin-routes.js'
import {
  IdentityChangeProvider,
  ImpersonationBannerSlot,
  ImpersonationChip,
  useIdentityChange,
  useImpersonationNoticeState,
  useImpersonationStatus,
} from './impersonation.js'
import { ContactVerificationBanner } from './contact-verification-banner.js'
import { CallbackErrorAlert } from './callback-error-notice.js'
import { usePermissionRead } from './platform-permissions.js'
import {
  useOrganizationAdminAdmission,
  useOrganizationAdminPermission,
  organizationAdminProtectedRoles,
} from './organization-admin.js'
import { clearCallbackFragment, readCallbackError } from './sign-in-landing.js'
import { AdminNavIcon } from './nav-icons.js'
import { AppNoticeHost, useAppNotice } from './app-notice.js'
import { AcceptInvitationPage } from './pages/accept-invitation-page.js'
import { AuthCallbackPage } from './pages/auth-callback-page.js'
import {
  NotFoundPage,
  TENANT_HOME_PATH,
} from './pages/not-found-page.js'
import { SignInView } from './pages/sign-in-view.js'
import {
  ACCOUNT_PATH,
  invitationPath,
  isOrganizationAdminPath,
  ORGANIZATIONS_NEW_PATH,
  PASSWORD_RESET_PATH,
} from './paths.js'
import { DEMO_NAMESPACE } from './resources.js'
import {
  clearSelfTenants,
  loadSelfTenants,
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

/** The platform-staff permission domain the admin routes' codes resolve in:
 * auth-core names 'system' the tenant-independent platform-staff set. */
const ADMIN_PERMISSION_DOMAIN = 'system'

/**
 * The tenant the host settles when it has none: the first listed membership
 * whose kind is not `personal`, else the first listed one. The caller tenant
 * read answers each row's kind from the host's resolver, so the default
 * branches on that value rather than on a name prefix: the account's personal
 * space is a single-person space and a poor default when the account also
 * belongs to a business tenant. The chosen row is nameable from the list
 * itself. Null when the list is empty.
 */
function defaultSelfTenantName(
  tenants: readonly { readonly name: string; readonly kind: string }[],
): string | null {
  return (
    tenants.find((tenant) => tenant.kind !== 'personal')?.name ??
    tenants[0]?.name ??
    null
  )
}

/**
 * Whether the host can name a tenant: either the caller tenant list holds the
 * row, or the roster's name cache does (a platform staff member entering a
 * tenant they are not a member of). A current tenant that no source names
 * would leave the caller in a tenant the bar cannot show and the switcher
 * cannot leave, while every tenant-scoped request is refused.
 */
function tenantNameable(tenantName: string, self: SelfTenantState): boolean {
  return (
    self.tenants.some((tenant) => tenant.name === tenantName) ||
    getRecordedTenantName(tenantName) !== null
  )
}

/** The app view root: routing installed once, over the shell switch. The
 * route table is gated (the notification, object and admin declarations
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
  // its name map to belong to. Clear both module-level cells here, in the
  // always-mounted root, so the clearing does not depend on which frame is
  // mounted: the admin area's session gate unmounts TenantFrame, and a
  // viewer who signs in again from there would otherwise inherit the ended
  // account's tenant -- under a personal tenant, another account's tenant --
  // for the window before the roster or the caller tenant list settles.
  // TenantFrame re-syncs its own state with this clearing below.
  useEffect(() => {
    if (!authenticated) {
      setCurrentTenant(null)
      setTenantNames(new Map())
    }
  }, [authenticated])
  // The caller tenant list belongs to an identity, not to the page. Read it
  // once per acting account here in the always-mounted root -- not in the
  // tenant frame, which the admin area and the sign-in surface do not mount
  // -- after forgetting the previous account's rows. It is read while
  // impersonating too: the answer is narrowed to the grant's tenant on the
  // server, and it is the name source the tenant area has after a full page
  // load, when the roster cache the operator read before is gone. The
  // switcher stays unmounted during an impersonation, so the list has no
  // tenant to offer there; the name it reports is what the request surface
  // announces.
  useEffect(() => {
    if (!authenticated) {
      clearSelfTenants()
      return
    }
    clearSelfTenants()
    void loadSelfTenants()
  }, [authenticated, principalId, impersonating])
  // The account's display time zone: every page renders its instants in it.
  // The read is the profile one the admin gate and ColorModeSync already share
  // -- one query key, one request -- and it is enabled only while the session
  // is authenticated. The public surfaces (the invitation landing, the
  // password reset) run before a session exists, so they must not spend a
  // /me request; they also get no value, which leaves the formatting seam on
  // the browser's own zone. An account that stored no zone (the empty string)
  // takes the same path.
  const profile = useAuthnProfileGet({ query: { enabled: authenticated } })
  const storedTimeZone = authenticated ? (profile.data?.time_zone ?? '') : ''
  const displayTimeZone = storedTimeZone === '' ? undefined : storedTimeZone
  return (
    // The display zone covers both shells: the admin area and the tenant area
    // are two frames over one route table, and a viewer moving between them
    // must not see the clock change.
    <DisplayTimeZoneProvider timeZone={displayTimeZone}>
      {/* The page's one feedback result: every write-feedback scope below
          this point clears its siblings' slots when it starts or settles a
          write, so the sign-in surface, the account panes, the admin pages
          and the impersonation banner never show two results at once. */}
      <WriteFeedbackPageProvider>
        <RouteProvider
          table={routes}
          fallback={(context: RouteContext) => (
            <NotFoundPage path={context.path} />
          )}
          presence={presence}
          pending={() => <RoutePresencePending />}
          failed={() => <RoutePresenceFailed />}
        >
          {/* The identity-change action both areas report to: a start or end
              swaps whose identity every cached row, the session, the attached
              permission set, the current tenant and the live subscription
              belong to (src/impersonation). */}
          <IdentityChangeProvider>
            {/* Above the shell switch: an accepted invitation and a departure
                from an organization both announce a confirmation on their way
                to the tenant home (inside the tenant frame) from a page that
                unmounts on the way, so the confirmation has to outlive that
                route change (src/app-notice.tsx). */}
            <AppNoticeHost>
              <AppFrame presence={presence} shellLayout={shellLayout} />
            </AppNoticeHost>
          </IdentityChangeProvider>
        </RouteProvider>
      </WriteFeedbackPageProvider>
    </DisplayTimeZoneProvider>
  )
}

/** Renders the area the current URL lands in: the admin or the tenant shell. */
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
  // The organization surface's admission for this render (see
  // src/organization-admin.ts). It is computed unconditionally so the hooks it
  // reads keep a stable order whichever area the URL selects; only the
  // organization branch consumes the result.
  const organizationAdmission = useOrganizationAdminAdmission()
  const navigate = useNavigate()
  // The one-shot confirmation the tenant home shows: an accepted invitation
  // and a landed ownership change both announce through it before leaving
  // the page that performed the write.
  const notice = useAppNotice()
  const { session } = useAppServices()
  // The host's identity-change action, handed to the admin page so a
  // settled start or end re-reads the identity the way the tenant frame's
  // banner does, plus the start seam: the page calls it before it writes, so
  // reads are held and the session's identity-transition guard is open
  // across the start's cookie rotation window.
  const { applyIdentityChange, onImpersonationStarting } = useIdentityChange()
  // The desktop sidebar's collapse state, host-held and shared by both areas:
  // each frame renders the toggle (the collapse props are supplied) and reports
  // each flip here, so the collapse carries across the tenant/admin switch.
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  // The organization surface's crumb names the organization whose pages these
  // are, from the same caller-tenant list the account menu renders. It is not
  // the navigation word 「Home」 and it is not a link: the crumb answers
  // "which organization", and leaving the surface is the sidebar's and the
  // account menu's business.
  const selfTenants = useSelfTenantState()
  // The organization failure page's retry. The session publishes `pending`
  // synchronously when a retry starts, so the failure page gives way to the
  // waiting view before the reads settle; the retry state lives here, above
  // that swap, so the waiting view can take the focus and hand it back to the
  // control that started the retry if the reads fail again.
  const permissionRead = usePermissionRead()
  const [organizationRetryInFlight, setOrganizationRetryInFlight] =
    useState(false)
  // Set when a retry starts and an outcome may still need the focus handed
  // back: consumed by the failure page's retry control, or by the page that
  // replaces the waiting view.
  const organizationRetryReturn = useRef(false)
  // Whether the failure page (and so its retry control) is on screen. When
  // the retry settles with the page still mounted, focus never fell with a
  // swap and there is nothing to hand back.
  const organizationRetryControlMounted = useRef(false)
  const retryOrganizationAccess = useCallback((): void => {
    if (organizationRetryInFlight) {
      return
    }
    organizationRetryReturn.current = true
    setOrganizationRetryInFlight(true)
    // The three reads the admission consumes, each reporting its own outcome
    // to the state that rendered the failure page.
    void Promise.allSettled([
      loadSelfTenants(),
      session.retryFeatures(),
      Promise.resolve(permissionRead.retry()),
    ]).finally(() => {
      setOrganizationRetryInFlight(false)
      if (organizationRetryControlMounted.current) {
        organizationRetryReturn.current = false
      }
    })
  }, [organizationRetryInFlight, permissionRead, session])
  // The retry's waiting view took the focus. When the retry admits the page,
  // that view unmounts and the new page gets the landing a whole-page swap
  // gets; a focus the operator moved in the meantime keeps its place.
  useLayoutEffect(() => {
    if (
      organizationAdmission.status !== 'allowed' ||
      !organizationRetryReturn.current
    ) {
      return
    }
    organizationRetryReturn.current = false
    if (document.activeElement !== document.body) {
      return
    }
    if (focusPageLanding(document.body) === null) {
      document.body.focus()
    }
  }, [organizationAdmission.status])

  // The invitation landing renders outside both shells: an invitee arrives
  // before any session exists. The declaration is ungated, so an anonymous
  // visitor matches it; a deployment that does not mount the invitation
  // module answers the page's public read with its invalid-link state.
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

  if (isAdminPath(pathname)) {
    // The admin declarations the presence predicate left in force. Pending
    // or failed leaves the set empty (fail-closed); a ready document may still
    // leave nothing when no admin page's feature is served.
    const adminRoutes = presentDeclarations(adminShellRoutes, presence)
    // The admin shell's own synchronous feature answer for the pages'
    // in-page controls.
    const hasFeature =
      presence.state === 'ready' ? presence.has : (): boolean => false
    return (
      <SessionGate>
        {/* The admin area is decided by the effective account's platform
            permission set (AdminAreaGate). An impersonating identity is the
            target account, which never holds a platform permission, so the area
            turns it away through the same criterion that turns an ordinary
            member away; no separate impersonation gate runs. The explanation
            belongs to the tenant frame's banner, not to this redirect. */}
        <AdminFeatureGate presence={presence}>
          <AdminAreaGate>
            {adminRoutes.length === 0 ? (
              // No admin page is served: the URL is the tenant area's,
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
              <AdminArea
                routes={adminRoutes}
                hasFeature={hasFeature}
                shellLayout={shellLayout}
                onImpersonationChange={applyIdentityChange}
                onImpersonationStarting={onImpersonationStarting}
                sidebarCollapsed={sidebarCollapsed}
                onSidebarCollapsedChange={setSidebarCollapsed}
              >
                <AdminRouteGuard>
                  <RouteView />
                </AdminRouteGuard>
              </AdminArea>
            )}
          </AdminAreaGate>
        </AdminFeatureGate>
      </SessionGate>
    )
  }

  // The organization admin partition. It renders inside the tenant frame --
  // the tenant shell already provides the AppShell -- and its subtree is
  // keyed by the current tenant: a switch remounts it, so the new tenant's
  // pages start with their own state and reads and nothing of the previous
  // tenant's survives. The tenant switch effect in TenantFrame also takes
  // the viewer to the new tenant's home, so the remounted frame is not
  // generally seen for a tenant the viewer may not manage; the key covers
  // the window before that navigation runs and any in-place tenant move.
  if (isOrganizationAdminPath(pathname)) {
    const hasFeature =
      presence.state === 'ready' ? presence.has : (): boolean => false
    const matchedLabel = match?.declaration.label
    // The organization's display name, the crumb's first step. The admission
    // having settled `allowed` guarantees the tenant is known; a row the
    // caller tenant list holds names it by display name, falling back to the
    // name, and the recorded cache covers a list that has not answered yet.
    // The id is the last resort so the crumb never goes blank.
    const organizationRow =
      organizationAdmission.status === 'allowed'
        ? selfTenants.tenants.find(
            (tenant) => tenant.name === organizationAdmission.tenantName,
          )
        : undefined
    const organizationName =
      organizationAdmission.status === 'allowed'
        ? (organizationRow === undefined
            ? null
            : (organizationRow.displayName.trim() === ''
                ? organizationRow.name
                : organizationRow.displayName)) ??
          getRecordedTenantDisplayName(organizationAdmission.tenantName) ??
          getRecordedTenantName(organizationAdmission.tenantName) ??
          organizationAdmission.tenantName
        : ''
    // The first step names the organization and is not a destination: the
    // organization surface has no root page of its own, so a link here could
    // only go to the tenant home while wearing the organization's name. The
    // crumb states which organization these pages belong to; leaving it is
    // the sidebar's and the account menu's business. The page follows it
    // directly: there is no middle grouping step to name, because the
    // organization admin surface is these pages themselves.
    const breadcrumbs = [
      { label: organizationName },
      ...(matchedLabel === undefined ? [] : [{ label: matchedLabel }]),
    ]
    return (
      <TenantFrame
        presence={presence}
        shellLayout={shellLayout}
        sidebarCollapsed={sidebarCollapsed}
        onSidebarCollapsedChange={setSidebarCollapsed}
      >
        {organizationAdmission.status === 'allowed' ? (
          <OrganizationAdminProvider
            key={`organization-admin:${organizationAdmission.tenantName}`}
            tenant={organizationAdmission.tenantName}
            operations={organizationAdminOperations}
            hasFeature={hasFeature}
            session={session}
            routes={organizationAdmission.routes}
            onNavigate={navigate}
            breadcrumbs={breadcrumbs}
            invitationLink={invitationPath}
            protectedRoles={organizationAdminProtectedRoles}
            onTenantUpdated={loadSelfTenants}
            onOrganizationLeft={async (message) => {
              // The transfer or deletion ended the caller's membership. The
              // confirmation is announced above the shell switch so it
              // survives the move; the caller tenant list is reloaded (the
              // menu and breadcrumb read it, and the tenant frame settles a
              // new default from it) and the organization surface is left
              // for the tenant home.
              notice.announce(message)
              await loadSelfTenants()
              navigate(TENANT_HOME_PATH)
            }}
          >
            <OrganizationRouteGuard target={organizationAdmission.target}>
              <RouteView />
            </OrganizationRouteGuard>
          </OrganizationAdminProvider>
        ) : organizationAdmission.status === 'pending' ? (
          // A fact the decision needs is still in flight (the tenant, its
          // kind, the feature document or the tenant permission read). Wait
          // inside the tenant frame rather than admitting a surface the
          // viewer may not have, or bouncing a viewer who does. A retry
          // started from the failure page lands here: the view takes the
          // focus the retry control held, rather than dropping it on the
          // body.
          <RouteGuard
            status="pending"
            pendingFallback={
              <OrganizationAccessPending
                takeFocus={organizationRetryInFlight}
              />
            }
          />
        ) : organizationAdmission.status === 'failed' ? (
          // A read the decision needs failed: the answer is unknown, not a
          // denial. The retryable page replaces the redirect, so a viewer
          // who may manage the tenant is not sent home as if they could not.
          <OrganizationAccessFailed
            busy={organizationRetryInFlight}
            onRetry={retryOrganizationAccess}
            returnFocus={organizationRetryReturn}
            controlMounted={organizationRetryControlMounted}
          />
        ) : (
          // A settled fact rules the surface out -- impersonating, the
          // system or a personal tenant, no served page, or no tenant
          // permission for this tenant. The tenant home is the fallback.
          <TenantHomeRedirect />
        )}
      </TenantFrame>
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
 * Mirrors the account's saved color-mode preference onto the theme provider.
 *
 * The account page writes the preference to the server and the device; this
 * component is the read side, so a viewer who signs in on a second device
 * -- or whose account choice differs from the device mirror -- sees the
 * account's own mode without visiting the preferences pane. It renders
 * nothing and issues no write of its own: the profile read the frame already
 * shares supplies the value, and only a non-empty `color_mode` is applied.
 *
 * The applied value is tracked in a ref: the effect re-runs whenever the
 * provider's preference changes, and re-applying the account's stored value
 * after the viewer moved the preference themselves would undo their change
 * while its own write is still in flight. One distinct stored value is
 * applied at most once.
 */
function ColorModeSync(): null {
  const { data } = useAuthnProfileGet()
  const { preference, setPreference } = useAppThemeMode()
  const applied = useRef<string | null>(null)
  const storedMode = data?.color_mode ?? ''
  useEffect(() => {
    // The server constrains color_mode to the three-value set; an empty or
    // foreign value is not a preference and is left alone.
    if (
      storedMode !== 'light' &&
      storedMode !== 'dark' &&
      storedMode !== 'system'
    ) {
      return
    }
    if (applied.current === storedMode) {
      return
    }
    applied.current = storedMode
    if (storedMode !== preference) {
      setPreference(storedMode)
      persistColorModePreference(storedMode)
    }
  }, [storedMode, preference, setPreference])
  return null
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
  // The page the frame is rendering.
  const { path: pathname } = useLocationPath()
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
  // The self-service organization creation's feature: absent means the
  // route is a 404 and neither entry is offered (fail closed while the
  // document is unsettled).
  const orgCreateServed = useFeature('org.create')
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
  // A refused redirect round trip that no landing surface consumed. The
  // surfaces that can consume the fragment (the sign-in surface, the binding
  // callback, the account and admin pages) read and clear it in their own
  // mount effects, and those run before this frame's: whatever is still here
  // was not consumed, and dropping it would swallow the reason. It is shown
  // once and cleared, so a reload does not repeat it.
  const [callbackError, setCallbackError] = useState<string | null>(null)
  // The roster read's outcome where it matters to the chrome: a failed read
  // is a fault, not a membership fact, so the switcher shows a non-blocking
  // notice and a retry instead of losing what it cannot name. The attempt
  // re-issues the read.
  const [rosterFailed, setRosterFailed] = useState(false)
  const [rosterAttempt, setRosterAttempt] = useState(0)
  useEffect(() => {
    const remaining = readCallbackError()
    if (remaining === undefined) {
      return
    }
    setCallbackError(remaining.code)
    clearCallbackFragment()
  }, [])
  // Whether the feature document is settled at all. `pending`/`failed` keep
  // the current tenant unknown (tenant-scoped queries stay disabled); `ready`
  // lets the roster read (when tenant.admin is served) and the caller tenant
  // list settle it.
  const featuresReady = presence.state === 'ready'
  // The current tenant is a host fact (the server resolves it per request
  // from the request's tenant header). chrome.tsx holds the two values that
  // come off it -- the switcher's identity and the name the request surface
  // carries -- and this state projects the identity for rendering; the setter
  // writes the cell too, so the value a request announces and the value the
  // chrome shows agree. It starts empty and is settled from the caller tenant
  // list (the personal row, then its first row) or, for platform staff, from
  // the roster's staff default first: until that settles, a tenant-scoped
  // request announces no name and is refused 404.
  // A frame mounting during an impersonation starts unknown too: the
  // module-level cell still holds the operator's tenant from before the
  // admin took over, and a first frame carrying it would issue one read
  // (and open one stream) under the impersonated session and the operator's
  // name before the status read settles the grant's tenant.
  const [tenantName, selectTenantName] = useTenantSelection(
    impersonating ? null : getCurrentTenant(),
  )
  // A switch made while the viewer is inside the organization admin area takes
  // them to the new tenant's home. The new tenant does not necessarily grant
  // the same administrative permissions, so staying on the page the previous
  // tenant offered would render its surface against the new tenant; the home
  // is where the new tenant's entry decides afresh whether that surface is
  // offered at all. The first settlement (null to a tenant) and the null
  // that follows a session end are not switches.
  const previousTenantForSwitch = useRef<string | null>(tenantName)
  useEffect(() => {
    const previous = previousTenantForSwitch.current
    previousTenantForSwitch.current = tenantName
    if (
      previous !== null &&
      previous !== tenantName &&
      isOrganizationAdminPath(pathname) &&
      !(
        // An authenticated viewer whose tenant went unknown because the
        // feature document is still pending or failed is waiting, not
        // switching: the organization admission renders its pending or retryable
        // failure view in place. Redirecting here would replace that view
        // with the tenant home and hide the retry.
        tenantName === null &&
        authenticated &&
        !featuresReady
      )
    ) {
      navigate(TENANT_HOME_PATH)
    }
  }, [tenantName, pathname, navigate, authenticated, featuresReady])
  // The caller's own tenant list: the memberships read the root loads, and
  // the fact the current tenant settles from. A read that has not settled
  // (a failure included) is not an empty answer, so the settlement never
  // treats it as "no tenants".
  const selfState = useSelfTenantState()
  // The platform roster is a platform management read: only a caller whose
  // platform permission set holds tenant.read may issue it. The set is
  // unknown until the host has read it, and an unknown set is not a
  // permission -- the read waits for the answer.
  const canReadRoster = usePermission(
    ADMIN_PERMISSION_DOMAIN,
    'tenant.read',
  )
  // Whether the current tenant is a SETTLED context rather than an unknown
  // one. A tenant-scoped surface that opens a connection (the notification
  // stream) waits for this: a new account would otherwise open the stream
  // before its caller tenant list settles and be refused 404.
  const [tenantSettled, setTenantSettled] = useState(false)
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
  // Read the platform roster for the names of tenants outside the caller's
  // own memberships (a staff member entering a tenant they do not belong to).
  // It is a platform management read: it is issued only when the platform
  // permission set is known and holds tenant.read, so an ordinary member
  // never spends a request that can only be refused. The read decides no
  // tenant and infers no membership: the current tenant is settled from the
  // caller's own list, and a failed read is a fault the switcher explains and
  // retries -- the name cache stands as it was.
  useEffect(() => {
    if (
      !authenticated ||
      impersonating ||
      !featuresReady ||
      !rosterServed ||
      !canReadRoster
    ) {
      setRosterFailed(false)
      return undefined
    }
    let cancelled = false
    setRosterFailed(false)
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
        setTenantNames(
          new Map(
            answer.tenants.map((tenant) => [tenant.name, tenant.display_name]),
          ),
        )
        setRosterFailed(false)
      })
      .catch(() => {
        // A refused or failed roster read is a fault, not a membership test:
        // it decides no tenant and says nothing about the caller. The
        // switcher shows it and offers the retry.
        if (cancelled) {
          return
        }
        setRosterFailed(true)
      })
    return () => {
      cancelled = true
    }
    // The identity is an input: after an impersonation ends the roster is
    // read again, and after a start the branch above keeps it out.
  }, [
    authenticated,
    impersonating,
    featuresReady,
    rosterServed,
    canReadRoster,
    principalId,
    rosterAttempt,
  ])

  // Settle the current tenant from the caller's own tenant list. The list is
  // the membership fact and readable by any authenticated caller without a
  // tenant name, so this settle never spends a request that can only be
  // refused. The order: a current tenant the host can name is kept (a switch,
  // a route that named one, or a previous settlement); then the first listed
  // membership that is not someone's personal tenant; then the list's first
  // row; otherwise the current tenant stays unknown and tenant-scoped reads
  // stay disabled. The roster plays no part: it is a staff name source, and
  // a failed read decides nothing. A list that has not settled -- and left no
  // rows on hand -- is not an empty answer, so nothing is settled from it.
  useEffect(() => {
    if (!authenticated) {
      // The root already cleared the module-level cells; this keeps the
      // frame's own state in step while it stays mounted (the ending
      // session keeps rendering the sign-in surface until it flips).
      selectTenantName(null)
      setTenantSettled(false)
      return
    }
    setTenantSettled(false)
    if (impersonating) {
      // The grant supplies the tenant's name, the name the request surface
      // announces. The impersonated account's own membership list is narrowed
      // to the grant's tenant on the server; until a source names it, the
      // current tenant stays unknown and tenant-scoped reads stay disabled,
      // rather than going out under a name no source holds.
      const granted = grant?.tenant ?? null
      selectTenantName(granted)
      setTenantSettled(granted !== null)
      return
    }
    if (!featuresReady) {
      // A pending or failed document never stands in for "no tenant": the
      // current tenant stays unknown.
      selectTenantName(null)
      setIdentitySettling(false)
      return
    }
    // Keep a current tenant the host can name: a switch, a route that named
    // one, or a previous settlement. It must be nameable -- a row of the list
    // or a roster name -- or the caller would sit in a tenant the bar cannot
    // show and the switcher cannot leave.
    const current = getCurrentTenant()
    if (
      !identitySettling &&
      current !== null &&
      tenantNameable(current, selfState)
    ) {
      selectTenantName(current)
      setTenantSettled(true)
      return
    }
    // A failure keeps the rows already settled, so the list stays usable; a
    // failure with no rows on hand is not an empty answer.
    const listUsable = selfState.settled || selfState.tenants.length > 0
    if (!listUsable) {
      selectTenantName(null)
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
    const next = defaultSelfTenantName(selfState.tenants)
    selectTenantName(next)
    setTenantSettled(next !== null)
    setIdentitySettling(false)
  }, [
    authenticated,
    impersonating,
    featuresReady,
    selfState,
    identitySettling,
    // The grant's tenant is the impersonated session's only name source: the
    // effect must re-run when the status read answers it.
    grant?.tenant,
    selectTenantName,
  ])

  // The router's navigate callback changes identity with the location, so an
  // effect that depended on it would re-run on every route change. The effect
  // reads the latest callback through a ref instead.
  const navigateRef = useRef(navigate)
  useEffect(() => {
    navigateRef.current = navigate
  }, [navigate])

  // The switcher's rows: every member tenant from the caller tenant read, in
  // its name order, plus the current tenant when the list does not
  // hold it -- a platform staff member entering a tenant they are not a member
  // of. The added row is nameable through the roster's name cache; when the
  // cache has no name either, the row is not added and the trigger shows its
  // no-current-tenant state (the documented limitation).
  const menuTenants: DemoTenant[] = selfState.tenants.map((tenant) => ({
    // TenantOption.id is the tenant's name in this host.
    id: tenant.name,
    // `name` is the row's display label: the profile's display name when it
    // names one, the request-surface name otherwise. The menu's own helper
    // applies the same fallback (and keeps a personal row on the account's
    // name).
    name: tenant.displayName.trim() === '' ? tenant.name : tenant.displayName,
    displayName: tenant.displayName,
    kind: tenant.kind,
  }))
  if (
    tenantName !== null &&
    !menuTenants.some((row) => row.id === tenantName)
  ) {
    const recordedDisplayName = getRecordedTenantDisplayName(tenantName)
    if (getRecordedTenantName(tenantName) !== null) {
      // The kind an operation's answer recorded lets the switcher label a row
      // the caller tenant read does not list yet; a row with none renders its
      // name.
      menuTenants.push({
        id: tenantName,
        name: recordedDisplayName ?? tenantName,
        displayName: recordedDisplayName ?? undefined,
        kind: getRecordedTenantKind(tenantName) ?? undefined,
      })
    }
  }

  const items = useNavItems()
  const adminRoutes = presentDeclarations(adminShellRoutes, presence)
  // The organization admin entry: the account menu's organization group and the
  // home card read this one derivation (src/organization-admin.ts), so they
  // never disagree about whether the surface is offered or where it starts.
  // The group's children are route-kit's own entries for the organization
  // declarations the caller may open, ordered as the declarations are, and
  // only while the admission is `allowed`.
  const organizationAdmission = useOrganizationAdminAdmission()
  const organizationItems: readonly AppShellNavItem[] | undefined =
    organizationAdmission.status === 'allowed'
      ? organizationAdmission.children
      : undefined
  // The organization list the account menu offers, and the roster read's
  // failure notice under its rows. Both the header menu and the sidebar entry
  // receive this one value, so the two surfaces cannot drift. The list is
  // withheld while an impersonation is in force and during the identity-change
  // gap after one ends: the impersonation is confined to one tenant, the
  // roster read is unreachable and the replacement list is the grant's tenant
  // alone, so the section would have nothing to offer. Withholding it also
  // keeps those rows out of the accessibility tree and the tab order.
  const { t } = useTranslation(DEMO_NAMESPACE)
  const organizations: AccountMenuOrganizations | undefined =
    impersonating || identitySettling
      ? undefined
      : {
          tenants: menuTenants,
          tenantName,
          onSelect: selectTenantName,
          // The self-service entry is offered only while the feature is
          // served and the session is not impersonating; the whole section
          // is withheld during an impersonation anyway, which keeps the
          // condition explicit for the row that renders it.
          createHref:
            orgCreateServed && !impersonating ? ORGANIZATIONS_NEW_PATH : undefined,
          notice: rosterFailed ? (
            <TenantRosterFailure
              onRetry={() => setRosterAttempt((current) => current + 1)}
            />
          ) : undefined,
        }
  // The account menu's destination into the platform admin: the first admin
  // page the principal may open (useAdminTarget, src/admin-routes.ts), shown
  // only while the current organization is the platform's own `system`
  // tenant. Platform administration is not a tenant member's surface, and
  // offering the way in from every organization put a staff-only console in
  // front of ordinary members' menus. An impersonating identity gets the
  // effective account's answer, which for a real target is null because a
  // target holds no platform permission. The set is not known synchronously
  // -- the host attaches it once the caller's platform permissions have been
  // read -- so the entry is absent until the set lands, which is the safe
  // direction. The start page's admin card calls the same hook, so the two
  // never disagree.
  const adminTarget = useAdminTarget(adminRoutes)
  const adminDestination: AccountMenuDestination | undefined =
    adminTarget === null || tenantName !== 'system'
      ? undefined
      : {
          id: 'admin',
          label: t('nav.admin'),
          target: adminTarget,
          icon: <AdminNavIcon size={24} />,
        }

  // The tenant navigation is the home entry alone. The admin's pages are
  // filtered out (the admin entry lives in the header's action slot, offered
  // only to platform staff), the organization admin's pages are destinations of
  // the account menu's organization group rather than navigation entries, and
  // the notification centre and the account settings are destinations of the
  // account entry at the bottom of the column, not navigation entries. The
  // entry's href/onClick pair is route-kit's -- a plain click navigates in
  // place while a modified or new-tab click stays with the browser -- and its
  // icon is the one its declaration carries (src/routes.tsx), looked up by
  // the entry id that declaration path supplies.
  const navItems: readonly AppShellNavItem[] = items
    .filter((item) => !isAdminPath(item.id))
    .filter((item) => !isOrganizationAdminPath(item.id))
    .filter(
      (item) => item.id !== NOTIFICATIONS_PATH && item.id !== ACCOUNT_PATH,
    )
    .map(
      (item): AppShellNavItem => ({
        id: item.id,
        label: item.label,
        icon: navIconFor(item.id),
        href: item.href,
        selected: item.selected,
        onClick: item.onClick,
      }),
    )

  return (
    <TenantProvider tenantId={tenantName}>
      {/* The account's saved color-mode preference, mirrored onto the theme
          provider while the tenant frame is the authenticated surface. It
          renders nothing: it only reconciles the two once the profile read
          lands. */}
      {authenticated ? <ColorModeSync /> : null}
      {/* The live notification subscription: it opens the api client's
          event-stream seam (the assembled client's openEventStream) for
          the acting tenant and re-reads the unread count and message list
          on every pushed frame. Mounted here, inside the tenant area,
          because the notification endpoints are tenant-scoped. It opens only
          once the session is authenticated, the process serves
          notification.stream, AND the tenant context has settled: the
          caller tenant list settles the account's personal tenant, and a
          connection opened before it settles would announce no name and be
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
              {callbackError === null ? null : (
                <CallbackErrorAlert
                  code={callbackError}
                  namespace={ACCOUNT_UI_NAMESPACE}
                  onClose={() => setCallbackError(null)}
                />
              )}
              {/* The registration-verification notice, below the app bar for
                  the whole tenant frame. It renders only while the profile's
                  verification state is pending, and stays until the address
                  is proved (no close control). */}
              <ContactVerificationBanner />
            </>
          }
          // The bar carries no controls of its own any more: the unread count
          // rides the account avatar (`notifications` below) and the inbox is
          // one entry of the account menu. No header-actions slot is passed at
          // all -- a slot that renders nothing still makes the sidebar shape
          // reserve its bottom row.
          sidebarCollapsed={sidebarCollapsed}
          onSidebarCollapsedChange={onSidebarCollapsedChange}
          // The account entry, its organization list and the admin entry are
          // one menu: both placements receive the same values, so the header
          // dropdown and the sidebar row cannot drift into two menus.
          navFooter={(rail) =>
            !accountEntryInNavFooter(shellLayout) ? null : (
              <AccountEntry
                narrow={rail.narrow}
                closeDrawer={rail.closeDrawer}
                organizationItems={organizationItems}
                organizations={organizations}
                destination={adminDestination}
                notifications={inboxServed}
              />
            )
          }
          // The slot is passed only when it has something to hold: the
          // sidebar shape has no user menu (its account row carries the
          // entry) and keeps the chip alone while an impersonation is in
          // force. A slot that renders nothing still makes that shape reserve
          // the row, which reads as padding under the account row.
          userMenu={
            accountEntryInUserSlot(shellLayout) || impersonating
              ? (rail) => (
                  <>
                    {/* The chip sits ahead of the account menu: while an
                        impersonation is in force it is the persistent visible
                        indication, and it toggles the notice (its close
                        control moves focus here). */}
                    <ImpersonationChip narrow={rail.narrow} />
                    {accountEntryInUserSlot(shellLayout) ? (
                      <UserMenu
                        narrow={rail.narrow}
                        closeDrawer={rail.closeDrawer}
                        organizationItems={organizationItems}
                        organizations={organizations}
                        destination={adminDestination}
                        notifications={inboxServed}
                      />
                    ) : null}
                  </>
                )
              : undefined
          }
          signIn={<SignInView />}
        >
          {/* The notification surface's feature-presence facts: the
              preference matrix and the optional delivery channels. They
              wrap the routed pages, where the notification surface reads
              them; an absent fact hides its surface (fail closed).
              The key is the identity (principal) and the generation its
              change bumps: mounting a new identity starts this subtree
              fresh, so state a page held for the previous identity is not
              carried into the next one. */}
          <NotificationPresenceProvider
            key={`${principalId}:${generation}`}
            preferences={notificationPreferencesServed}
            channels={notificationChannels}
          >
            <Box sx={{ p: 3 }}>
              {children}
            </Box>
          </NotificationPresenceProvider>
        </ProductShell>
        </ImpersonationNoticeProvider>
      </NotificationStreamProvider>
    </TenantProvider>
  )
}

/**
 * The admin area's session gate. The tenant area is gated by ProductShell
 * itself, but the admin renders its own frame, so it needs the same branch:
 * an anonymous viewer -- a fresh visitor, or one whose authenticated session
 * turned anonymous mid-use (the same snapshot flip) -- meets the host's
 * sign-in surface instead of an admin whose requests can never answer.
 *
 * The opposite flip is a whole-page swap in place, at the same URL: the
 * sign-in surface unmounts and the admin takes its place. The gate hands the
 * new page the focus the vanished surface held, the same duty ProductShell
 * performs for its branches, and lands on the page's own heading rather than
 * the shell's wrapper so the reader arrives at the content.
 */
function SessionGate({
  children,
}: {
  readonly children: ReactNode
}): ReactElement {
  const snapshot = useAuthState()
  const authenticated = snapshot.state === 'authenticated'
  // The rendered area's container, and the state of the previous render: the
  // flip out of the sign-in surface is the only transition this gate lands
  // focus for (the first render's focus belongs to the page load).
  const gate = useRef<HTMLDivElement | null>(null)
  const previousAuthenticated = useRef(authenticated)

  useLayoutEffect(() => {
    const previous = previousAuthenticated.current
    previousAuthenticated.current = authenticated
    if (previous || !authenticated) {
      return
    }
    // Only when the flip took the focus: the sign-in surface unmounted with
    // the control the operator activated, or focus fell to the body. A place
    // the operator moved to keeps it.
    if (!focusFellWithPageSwap(null)) {
      return
    }
    const container = gate.current
    if (container === null) {
      return
    }
    // A completed sign-in clears the permission domains, so the admin area is
    // still waiting when the flip commits: the gate holds no page entry yet.
    // Land on the wrapper for now, and hand the focus on once the page's own
    // entry arrives.
    let current = focusPageLanding(container)
    if (current === null) {
      container.focus()
      current = container
    }
    if (isPageHeading(current)) {
      return
    }
    const handOff = (): boolean => {
      // Only when the flip's landing still holds the focus: a place the
      // operator moved to keeps it.
      const active = document.activeElement
      if (
        active !== current &&
        active !== container &&
        active !== document.body
      ) {
        return true
      }
      // The page's landmark may arrive before its heading; the rule keeps the
      // focus on whichever landing exists and hands on again when the heading
      // itself appears.
      const target = focusPageLanding(container)
      if (target === null) {
        container.focus()
        current = container
        return false
      }
      current = target
      return isPageHeading(target)
    }
    if (handOff()) {
      return
    }
    // The area settles through its own child state, which does not re-render
    // this gate: the DOM is what says the page has arrived.
    const observer = new MutationObserver(() => {
      if (handOff()) {
        observer.disconnect()
      }
    })
    observer.observe(container, { childList: true, subtree: true })
    return () => observer.disconnect()
  }, [authenticated])

  if (!authenticated) {
    // The admin URL is the viewer's destination: a registration completed
    // here stays on it instead of being taken to the tenant home or the
    // account page (the register mode's landing belongs to the app's own
    // sign-in page).
    return <SignInView keepLanding />
  }
  return (
    <Box ref={gate} tabIndex={-1}>
      {children}
    </Box>
  )
}

/**
 * The admin area's feature-state gate, frameless: while the document is
 * pending (a retry is in flight) or failed, the admin renders the same
 * waiting/failure view the route layer would, with no shell around it. It runs
 * before the permission gate: a failed read keeps the platform permission set
 * unknown, and an operator whose read failed must see a retry, never a
 * redirect that reads as "not staff".
 */
function AdminFeatureGate({
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
 * Settles a current tenant for the admin area when none is settled.
 *
 * The caller's permission read (src/platform-permissions.tsx) sits on the API
 * endpoint, which the tenant resolver guards: a request that names no tenant
 * is refused 404 before any handler runs. The tenant frame settles the current
 * tenant for the tenant area, but an operator who reaches an admin URL
 * directly -- a reload of an admin page, a bookmark -- mounts no tenant
 * frame, and without a name the permission read is refused, the platform set
 * attaches as the fail-closed empty one, and the area gate turns the operator
 * away.
 *
 * The settle reads the caller's own tenant list -- readable by any
 * authenticated caller and needing no tenant name -- and takes the same
 * default the tenant frame would: the first membership that is not the
 * caller's personal tenant, else the first row. No platform roster read is
 * spent here: the roster needs a platform permission that this settle exists
 * to let the permission read answer. An impersonated identity settles from the
 * same list, whose answer the server narrows to the grant. Until the list
 * answers the tenant stays unknown and the effect runs again when it lands; a
 * list that settles empty leaves the area on its access wait, and a list read
 * that fails is a fault, not a membership test.
 */
function useAdminTenantSettle(authenticated: boolean): void {
  const tenantName = useCurrentTenant()
  const selfState = useSelfTenantState()
  useEffect(() => {
    if (!authenticated || tenantName !== null) {
      return
    }
    const next = defaultSelfTenantName(selfState.tenants)
    if (next === null) {
      return
    }
    setCurrentTenant(next)
  }, [authenticated, tenantName, selfState])
}

/**
 * The admin area's access gate: the whole area is the platform-staff face,
 * so a principal who holds no platform permission never enters it. The admin
 * frame -- its navigation, its banner -- is not rendered at all, rather than
 * rendered with its content swapped for a no-permission surface: the menu names
 * are themselves information a non-staff viewer must not receive.
 *
 * It settles the area tier of the admin gate, between the account menu's
 * admin entry (which hides the way in outside the `system` organization) and
 * each page's own code (the route guard below, which turns a staff viewer
 * away from a page they lack). The
 * decision is drawn from the platform permission set (auth-core's 'system'
 * domain), read here before the shell renders so a denied viewer never sees
 * even its chrome. The snapshot's `impersonating` fact is decided first,
 * before any permission read is consulted: the impersonation alone closes
 * the admin, whatever the target's permission read answers. The outcomes:
 *
 *  - impersonating: the signed-in identity is acting as someone else. The
 *    area renders nothing and redirects to the tenant home, with no roster
 *    read and no dependence on the permission set -- a refused target read
 *    never turns into a retryable failure the impersonator cannot resolve.
 *  - granted: the set is known and holds at least one platform permission --
 *    the principal is platform staff, and the admin renders.
 *  - denied: the set is known and empty -- an ordinary member. The area renders
 *    nothing and redirects to the tenant home, so the admin's path space (and
 *    the names within it) is left behind for a shell the viewer may use.
 *  - pending: the set is unknown (null) -- the host has not yet attached the
 *    caller's platform permissions. This is the fail-closed direction: an
 *    unknown set shows neither the admin nor an immediate redirect, only a
 *    shell-less placeholder, and the area settles once the set lands. Rendering
 *    the admin on an unknown set would show the shell to a member for the
 *    window before their empty set is attached, which is the only moment the
 *    set is null for a signed-in viewer.
 *  - failed: a read the decision depends on (the permission set, or the
 *    profile whose verification state can send an unproved account away)
 *    failed. The unknown value is not read as a conclusion: the area shows a
 *    shell-less failure with a retry, never the member redirect.
 */
function AdminAreaGate({
  children,
}: {
  readonly children: ReactNode
}): ReactElement | null {
  const snapshot = useAuthState()
  useAdminTenantSettle(snapshot.state === 'authenticated')
  const permissionRead = usePermissionRead()
  // An account whose address is still unproved is sent to the verification
  // page before the area's permission read: proof of the address is the gate
  // the admin area requires, and the verification page is the one place that
  // lifts the state.
  // Only a READ state counts: a failed profile read leaves the verification
  // state unknown, and answering "not pending" to a failure is the same
  // mistake as reading a failed permission read as an empty set.
  const profile = useAuthnProfileGet({
    query: { enabled: snapshot.state === 'authenticated' },
  })
  // The impersonation is the identity fact that closes the admin: it is
  // decided before the permission set is consulted, so a target's permission
  // read -- refused, empty or even non-empty -- cannot turn this into the
  // access wait, the member redirect reached by a read, or the retryable
  // failure. This branch reads no roster.
  if (snapshot.impersonating) {
    return <TenantHomeRedirect />
  }
  const status = adminAreaStatus(snapshot)
  if (status === 'granted') {
    return <>{children}</>
  }
  if (status === 'denied') {
    return <TenantHomeRedirect />
  }
  if (
    profile.data !== undefined &&
    contactVerificationPending(profile.data)
  ) {
    return <ContactVerificationRedirect />
  }
  if (permissionRead.failed || profile.isError) {
    // The set (or the verification fact) is unknown because a read failed:
    // neither a member's empty set nor an unproved account is a conclusion
    // the failure supports. The failure view retries the reads instead of
    // redirecting.
    return (
      <AdminAccessFailed
        onRetry={() => {
          permissionRead.retry()
          void profile.refetch()
        }}
      />
    )
  }
  return <AdminAccessPending />
}

/**
 * Replaces the current history entry with the tenant home, rendered instead
 * of a surface the current identity may not use. It navigates from an effect
 * (route-kit's useRedirect), which is where the router wants a render-driven
 * move, and renders nothing: the gates that mount it render no admin chrome
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
 * The organization page's route guard. It reads the permission code of the
 * declaration the URL matched exactly and checks it against the tenant
 * permission set scoped to the current tenant. A code the caller does not
 * hold sends the viewer to `target`, the first organization page it may open
 * (the same entry the account menu's organization group and home card point at);
 * no permitted page at all is a `denied` admission, handled above.
 */
function OrganizationRouteGuard({
  target,
  children,
}: {
  readonly target: string
  readonly children: ReactNode
}): ReactElement {
  const match = useMatch()
  const status = useOrganizationAdminPermission(match?.declaration.permission)
  return (
    <RouteGuard
      status={status}
      deniedFallback={<OrganizationTargetRedirect target={target} />}
    >
      {children}
    </RouteGuard>
  )
}

/** Replaces the current history entry with the given organization page,
 * rendered instead of a page the current tenant's permissions do not admit. */
function OrganizationTargetRedirect({
  target,
}: {
  readonly target: string
}): null {
  const redirect = useRedirect()
  useEffect(() => {
    redirect(target)
  }, [redirect, target])
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
 * member. (The account menu's entry keys on the first page whose code is
 * held; the two agree on the demo's principals, which hold everything or
 * nothing.) An
 * unknown set is pending, never granted.
 */
function adminAreaStatus(
  snapshot: AuthSnapshot,
): 'granted' | 'denied' | 'pending' {
  if (snapshot.state !== 'authenticated') {
    // The session gate above turned an anonymous viewer away; pending is
    // fail-closed and never shows the shell.
    return 'pending'
  }
  const set = snapshot.permissionSets[ADMIN_PERMISSION_DOMAIN]
  if (set === null) {
    return 'pending'
  }
  return set.length > 0 ? 'granted' : 'denied'
}

/**
 * The shell-less placeholder the area gate shows while the platform permission
 * set is unknown: no admin chrome, so nothing of the admin reaches the
 * viewer before the gate has settled. The label names the wait for assistive
 * technology.
 */
function AdminAccessPending(): ReactElement {
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
      <CircularProgress aria-label={t('admin.accessPending')} />
    </Box>
  )
}

/**
 * The shell-less failure the area gate shows when one of its reads failed: the
 * platform permission set or the profile's verification fact is unknown, so
 * neither the admin nor a redirect is supported. It is not the "no
 * permission" face -- a failure says nothing about who the caller is -- and
 * its retry re-issues the reads.
 */
function AdminAccessFailed({
  onRetry,
}: {
  readonly onRetry: () => void
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Box
      component="section"
      role="alert"
      sx={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'flex-start',
        gap: 2,
        p: 3,
      }}
    >
      <Typography variant="body1">{t('admin.accessFailed')}</Typography>
      <Button onClick={onRetry}>{t('admin.accessRetry')}</Button>
    </Box>
  )
}

/**
 * The organization surface's retryable failure, rendered inside the tenant
 * frame. A read the admission depends on -- the caller tenant list, the
 * feature document, or the tenant permission read -- failed, so the answer is
 * unknown and neither the surface nor the tenant-home redirect is supported.
 * Its shape follows AdminAccessFailed (one explanation, one retry), with the
 * level-one heading the tenant frame's main landmark needs and the failed
 * admin page deliberately omits (it renders without a frame).
 *
 * The retry re-issues every read the admission consumes: the caller tenant
 * list, the feature document and the tenant permission set. Absent any one
 * of them, the admission would simply settle back into this state. The retry
 * itself is owned by AppFrame, above the swap it causes; this page only
 * reports the click and takes the focus back when the reads fail again -- see
 * `organizationRetryReturn` there.
 */
function OrganizationAccessFailed({
  busy,
  onRetry,
  returnFocus,
  controlMounted,
}: {
  readonly busy: boolean
  readonly onRetry: () => void
  /** Consumed once: set when a retry is in flight and its outcome may need the focus back. */
  readonly returnFocus: { current: boolean }
  /** Reports whether this page (and so its retry control) is on screen. */
  readonly controlMounted: { current: boolean }
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const retryControl = useRef<HTMLButtonElement | null>(null)
  useLayoutEffect(() => {
    controlMounted.current = true
    return () => {
      controlMounted.current = false
    }
  }, [controlMounted])
  useLayoutEffect(() => {
    if (!returnFocus.current) {
      return
    }
    returnFocus.current = false
    // The waiting view took the focus when it replaced this page; once it is
    // removed, the body is what is left. If the operator moved elsewhere,
    // that place keeps the focus.
    if (document.activeElement !== document.body) {
      return
    }
    retryControl.current?.focus()
  }, [returnFocus])
  return (
    <Box
      component="section"
      role="alert"
      sx={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'flex-start',
        gap: 2,
        maxWidth: 640,
      }}
    >
      <Typography component="h1" variant="h5">
        {t('organization.accessFailed.title')}
      </Typography>
      <Typography variant="body1">
        {t('organization.accessFailed.description')}
      </Typography>
      <ActionButton ref={retryControl} onClick={onRetry} busy={busy}>
        {t('organization.accessFailed.retry')}
      </ActionButton>
    </Box>
  )
}

/**
 * The organization surface's waiting view, rendered inside the tenant frame
 * while the admission is undecided. It is the landing a retry started from
 * the failure page hands its focus to: the retry control unmounts with that
 * page, and without a landing the focus would fall to the body. The region
 * is programmatically focusable and carries the loading label as its
 * accessible name; it takes focus only when a retry put it there.
 */
function OrganizationAccessPending({
  takeFocus,
}: {
  readonly takeFocus: boolean
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const landing = useRef<HTMLDivElement | null>(null)
  useLayoutEffect(() => {
    if (takeFocus) {
      landing.current?.focus()
    }
  }, [takeFocus])
  return (
    <Box
      ref={landing}
      tabIndex={-1}
      role="status"
      aria-label={t('organization.accessPending')}
      sx={{
        display: 'flex',
        justifyContent: 'center',
        alignItems: 'center',
        paddingY: 6,
      }}
    >
      <CircularProgress aria-hidden="true" />
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
 * The admin's page-level gate, composed the host way: layout-kit's
 * RouteGuard fed a status the host derives from the matched page's permission
 * code (the admin declarations carry tenant.read / settings.read /
 * audit.read). A code held, or a page with no code, is allowed; a code the
 * principal is known not to hold is denied. This is the innermost tier of the
 * admin gate -- the account menu's admin entry hides the way in, the area
 * gate (AdminAreaGate) turns a non-staff viewer away before the shell
 * renders, and this guard settles each page's own code within the area. It
 * runs only inside the area, where the platform set is already known, so its
 * status is a plain set lookup: a staff viewer who lacks one page's code meets
 * the no-permission surface in place of that page, the rest of the admin
 * frame intact.
 */
function AdminRouteGuard({
  children,
}: {
  readonly children: ReactNode
}): ReactElement {
  const snapshot = useAuthState()
  // The exact declaration the URL matched, labelled or not: a page reached
  // from a row or a header link (a detail page, the registration page) is
  // held to its own permission code too, and nav collection alone would
  // leave it unguarded.
  const matched = useMatch()
  return (
    <RouteGuard
      status={adminGuardStatus(snapshot, matched?.declaration.permission)}
    >
      {children}
    </RouteGuard>
  )
}

/** The RouteGuard status for one admin page under the current session. */
function adminGuardStatus(
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
  const set = snapshot.permissionSets[ADMIN_PERMISSION_DOMAIN]
  if (set === null) {
    // The area gate renders this guard only once the platform set is known;
    // an unknown set is fail-closed here too rather than passing the page.
    return 'pending'
  }
  return set.includes(permission) ? 'allowed' : 'denied'
}
