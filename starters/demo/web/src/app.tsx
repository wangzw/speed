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

/** The platform-staff permission domain; auth-core names it 'system'. */
const ADMIN_PERMISSION_DOMAIN = 'system'

function defaultSelfTenantName(
  tenants: readonly { readonly name: string; readonly kind: string }[],
): string | null {
  return (
    tenants.find((tenant) => tenant.kind !== 'personal')?.name ??
    tenants[0]?.name ??
    null
  )
}

function tenantNameable(tenantName: string, self: SelfTenantState): boolean {
  return (
    self.tenants.some((tenant) => tenant.name === tenantName) ||
    getRecordedTenantName(tenantName) !== null
  )
}

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
  useEffect(() => {
    if (!authenticated) {
      // Clear in the always-mounted root: the admin gate unmounts TenantFrame, so a sign-in there would otherwise inherit the ended account's tenant.
      setCurrentTenant(null)
      setTenantNames(new Map())
    }
  }, [authenticated])
  useEffect(() => {
    if (!authenticated) {
      clearSelfTenants()
      return
    }
    clearSelfTenants()
    void loadSelfTenants()
  }, [authenticated, principalId, impersonating])
  const profile = useAuthnProfileGet({ query: { enabled: authenticated } })
  const storedTimeZone = authenticated ? (profile.data?.time_zone ?? '') : ''
  const displayTimeZone = storedTimeZone === '' ? undefined : storedTimeZone
  return (
    <DisplayTimeZoneProvider timeZone={displayTimeZone}>
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
          <IdentityChangeProvider>
            <AppNoticeHost>
              <AppFrame presence={presence} shellLayout={shellLayout} />
            </AppNoticeHost>
          </IdentityChangeProvider>
        </RouteProvider>
      </WriteFeedbackPageProvider>
    </DisplayTimeZoneProvider>
  )
}

function AppFrame({
  presence,
  shellLayout,
}: {
  readonly presence: RoutePresence
  readonly shellLayout?: AppShellShellLayout
}): ReactElement {
  const { path: pathname } = useLocationPath()
  const match = useMatch()
  const organizationAdmission = useOrganizationAdminAdmission()
  const navigate = useNavigate()
  const notice = useAppNotice()
  const { session } = useAppServices()
  const { applyIdentityChange, onImpersonationStarting } = useIdentityChange()
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const selfTenants = useSelfTenantState()
  const permissionRead = usePermissionRead()
  const [organizationRetryInFlight, setOrganizationRetryInFlight] =
    useState(false)
  const organizationRetryReturn = useRef(false)
  const organizationRetryControlMounted = useRef(false)
  const retryOrganizationAccess = useCallback((): void => {
    if (organizationRetryInFlight) {
      return
    }
    organizationRetryReturn.current = true
    setOrganizationRetryInFlight(true)
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

  if (match !== undefined && match.declaration === invitationRoutes[0]) {
    return <AcceptInvitationPage token={match.context.params.token ?? ''} />
  }

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

  if (pathname === PASSWORD_RESET_PATH) {
    return <RouteView />
  }

  if (isAdminPath(pathname)) {
    const adminRoutes = presentDeclarations(adminShellRoutes, presence)
    const hasFeature =
      presence.state === 'ready' ? presence.has : (): boolean => false
    return (
      <SessionGate>
        <AdminFeatureGate presence={presence}>
          <AdminAreaGate>
            {adminRoutes.length === 0 ? (
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

  if (isOrganizationAdminPath(pathname)) {
    const hasFeature =
      presence.state === 'ready' ? presence.has : (): boolean => false
    const matchedLabel = match?.declaration.label
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
          <RouteGuard
            status="pending"
            pendingFallback={
              <OrganizationAccessPending
                takeFocus={organizationRetryInFlight}
              />
            }
          />
        ) : organizationAdmission.status === 'failed' ? (
          <OrganizationAccessFailed
            busy={organizationRetryInFlight}
            onRetry={retryOrganizationAccess}
            returnFocus={organizationRetryReturn}
            controlMounted={organizationRetryControlMounted}
          />
        ) : (
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

function ColorModeSync(): null {
  const { data } = useAuthnProfileGet()
  const { preference, setPreference } = useAppThemeMode()
  const applied = useRef<string | null>(null)
  const storedMode = data?.color_mode ?? ''
  useEffect(() => {
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
  const { api } = useAppServices()
  const { path: pathname } = useLocationPath()
  const inboxServed = useFeature('notification.inbox')
  const streamServed = useFeature('notification.stream')
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
  const orgCreateServed = useFeature('org.create')
  const snapshot = useAuthState()
  const authenticated = snapshot.state === 'authenticated'
  const impersonating = authenticated && snapshot.impersonating
  const principalId =
    snapshot.state === 'authenticated'
      ? (snapshot.principal?.account_id ?? '')
      : ''
  const { generation } = useIdentityChange()
  const grant = useImpersonationStatus(impersonating)
  const notice = useImpersonationNoticeState(grant)
  const [callbackError, setCallbackError] = useState<string | null>(null)
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
  const featuresReady = presence.state === 'ready'
  const [tenantName, selectTenantName] = useTenantSelection(
    impersonating ? null : getCurrentTenant(),
  )
  const previousTenantForSwitch = useRef<string | null>(tenantName)
  useEffect(() => {
    const previous = previousTenantForSwitch.current
    previousTenantForSwitch.current = tenantName
    if (
      previous !== null &&
      previous !== tenantName &&
      isOrganizationAdminPath(pathname) &&
      !(
        tenantName === null &&
        authenticated &&
        !featuresReady
      )
    ) {
      navigate(TENANT_HOME_PATH)
    }
  }, [tenantName, pathname, navigate, authenticated, featuresReady])
  const selfState = useSelfTenantState()
  const canReadRoster = usePermission(
    ADMIN_PERMISSION_DOMAIN,
    'tenant.read',
  )
  const [tenantSettled, setTenantSettled] = useState(false)
  const [identitySettling, setIdentitySettling] = useState(false)
  const [previousImpersonating, setPreviousImpersonating] =
    useState(impersonating)
  // Detect during render: React re-renders before committing, so the first
  // commit after the flip already withholds the switcher.
  if (previousImpersonating !== impersonating) {
    setPreviousImpersonating(impersonating)
    if (previousImpersonating) {
      setIdentitySettling(true)
    }
  }
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
    // Deferred so the request seam's synchronous throw (no bound request fn) becomes a rejection the catch handles.
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
        if (cancelled) {
          return
        }
        setRosterFailed(true)
      })
    return () => {
      cancelled = true
    }
  }, [
    authenticated,
    impersonating,
    featuresReady,
    rosterServed,
    canReadRoster,
    principalId,
    rosterAttempt,
  ])

  useEffect(() => {
    if (!authenticated) {
      selectTenantName(null)
      setTenantSettled(false)
      return
    }
    setTenantSettled(false)
    if (impersonating) {
      const granted = grant?.tenant ?? null
      selectTenantName(granted)
      setTenantSettled(granted !== null)
      return
    }
    if (!featuresReady) {
      selectTenantName(null)
      setIdentitySettling(false)
      return
    }
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
    const listUsable = selfState.settled || selfState.tenants.length > 0
    if (!listUsable) {
      selectTenantName(null)
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
    grant?.tenant,
    selectTenantName,
  ])

  // navigate changes identity with the location; read it through a ref so the effect does not re-run per route change.
  const navigateRef = useRef(navigate)
  useEffect(() => {
    navigateRef.current = navigate
  }, [navigate])

  const menuTenants: DemoTenant[] = selfState.tenants.map((tenant) => ({
    id: tenant.name,
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
  const organizationAdmission = useOrganizationAdminAdmission()
  const organizationItems: readonly AppShellNavItem[] | undefined =
    organizationAdmission.status === 'allowed'
      ? organizationAdmission.children
      : undefined
  const { t } = useTranslation(DEMO_NAMESPACE)
  const organizations: AccountMenuOrganizations | undefined =
    impersonating || identitySettling
      ? undefined
      : {
          tenants: menuTenants,
          tenantName,
          onSelect: selectTenantName,
          createHref:
            orgCreateServed && !impersonating ? ORGANIZATIONS_NEW_PATH : undefined,
          notice: rosterFailed ? (
            <TenantRosterFailure
              onRetry={() => setRosterAttempt((current) => current + 1)}
            />
          ) : undefined,
        }
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
      {authenticated ? <ColorModeSync /> : null}
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
              <ContactVerificationBanner />
            </>
          }
          sidebarCollapsed={sidebarCollapsed}
          onSidebarCollapsedChange={onSidebarCollapsedChange}
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
          // Pass this slot only when it holds something: an empty slot still makes the sidebar shape reserve its bottom row.
          userMenu={
            accountEntryInUserSlot(shellLayout) || impersonating
              ? (rail) => (
                  <>
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

function SessionGate({
  children,
}: {
  readonly children: ReactNode
}): ReactElement {
  const snapshot = useAuthState()
  const authenticated = snapshot.state === 'authenticated'
  const gate = useRef<HTMLDivElement | null>(null)
  const previousAuthenticated = useRef(authenticated)

  useLayoutEffect(() => {
    const previous = previousAuthenticated.current
    previousAuthenticated.current = authenticated
    if (previous || !authenticated) {
      return
    }
    if (!focusFellWithPageSwap(null)) {
      return
    }
    const container = gate.current
    if (container === null) {
      return
    }
    let current = focusPageLanding(container)
    if (current === null) {
      container.focus()
      current = container
    }
    if (isPageHeading(current)) {
      return
    }
    const handOff = (): boolean => {
      const active = document.activeElement
      if (
        active !== current &&
        active !== container &&
        active !== document.body
      ) {
        return true
      }
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
    // Child state settles the area without re-rendering this gate, so the DOM is what tells the page's heading arrived.
    const observer = new MutationObserver(() => {
      if (handOff()) {
        observer.disconnect()
      }
    })
    observer.observe(container, { childList: true, subtree: true })
    return () => observer.disconnect()
  }, [authenticated])

  if (!authenticated) {
    return <SignInView keepLanding />
  }
  return (
    <Box ref={gate} tabIndex={-1}>
      {children}
    </Box>
  )
}

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

function AdminAreaGate({
  children,
}: {
  readonly children: ReactNode
}): ReactElement | null {
  const snapshot = useAuthState()
  useAdminTenantSettle(snapshot.state === 'authenticated')
  const permissionRead = usePermissionRead()
  const profile = useAuthnProfileGet({
    query: { enabled: snapshot.state === 'authenticated' },
  })
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
    // Only a read state counts: a failed profile read leaves verification unknown, not "not pending".
    profile.data !== undefined &&
    contactVerificationPending(profile.data)
  ) {
    return <ContactVerificationRedirect />
  }
  if (permissionRead.failed || profile.isError) {
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

function TenantHomeRedirect(): null {
  const redirect = useRedirect()
  useEffect(() => {
    redirect(TENANT_HOME_PATH)
  }, [redirect])
  return null
}

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

function ContactVerificationRedirect(): null {
  const redirect = useRedirect()
  useEffect(() => {
    redirect(`${ACCOUNT_PATH}?verify=1`)
  }, [redirect])
  return null
}

// An unknown platform set is pending, never granted: the area is fail-closed.
function adminAreaStatus(
  snapshot: AuthSnapshot,
): 'granted' | 'denied' | 'pending' {
  if (snapshot.state !== 'authenticated') {
    return 'pending'
  }
  const set = snapshot.permissionSets[ADMIN_PERMISSION_DOMAIN]
  if (set === null) {
    return 'pending'
  }
  return set.length > 0 ? 'granted' : 'denied'
}

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

function OrganizationAccessFailed({
  busy,
  onRetry,
  returnFocus,
  controlMounted,
}: {
  readonly busy: boolean
  readonly onRetry: () => void
  readonly returnFocus: { current: boolean }
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

function AdminRouteGuard({
  children,
}: {
  readonly children: ReactNode
}): ReactElement {
  const snapshot = useAuthState()
  const matched = useMatch()
  return (
    <RouteGuard
      status={adminGuardStatus(snapshot, matched?.declaration.permission)}
    >
      {children}
    </RouteGuard>
  )
}

function adminGuardStatus(
  snapshot: AuthSnapshot,
  permission: string | undefined,
): RouteGuardStatus {
  if (permission === undefined) {
    return 'allowed'
  }
  if (snapshot.state !== 'authenticated') {
    return 'pending'
  }
  const set = snapshot.permissionSets[ADMIN_PERMISSION_DOMAIN]
  if (set === null) {
    return 'pending'
  }
  return set.includes(permission) ? 'allowed' : 'denied'
}
