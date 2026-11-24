import { useEffect } from 'react'
import type { ReactElement } from 'react'
import type { FeatureName } from '@speed/api-sdk/features'
import { notificationRoutes } from '@speed/notification-ui'
import { useLocationPath, useRedirect } from '@speed/route-kit'
import type { RouteDeclaration, RouteTable } from '@speed/route-kit'
import { adminShellRoutes } from './admin-routes.js'
import { organizationAdminShellRoutes } from './organization-admin.js'
import { DemoNavLabel } from './nav-label.js'
import { AccountNavIcon, HomeNavIcon, NotificationsNavIcon } from './nav-icons.js'
import {
  AccountBindingCallbackPage,
  AccountSettingsPage,
} from './pages/account-page.js'
import { AcceptInvitationPage } from './pages/accept-invitation-page.js'

import { AuthCallbackPage } from './pages/auth-callback-page.js'
import { HomePage } from './pages/home-page.js'
import { NotificationsPage } from './pages/notifications-page.js'
import { OrganizationCreateRoute } from './pages/organization-create-page.js'
import { TENANT_HOME_PATH } from './pages/not-found-page.js'
import { PasswordResetPage } from './pages/password-reset-page.js'
import { TransferRequestsPage } from './pages/transfer-requests-page.js'
import {
  ACCOUNT_CALLBACK_PATH,
  ACCOUNT_PATH,
  ACCOUNT_SECTION_PATHS,
  ACCOUNT_SECTIONS,
  AUTH_CALLBACK_PATH,
  INVITATION_PREFIX,
  ORGANIZATIONS_NEW_PATH,
  PASSWORD_RESET_PATH,
  TRANSFER_REQUESTS_PATH,
  TRANSFER_REQUEST_PATH,
} from './paths.js'

export {
  ACCOUNT_CALLBACK_PATH,
  ACCOUNT_PATH,
  AUTH_CALLBACK_PATH,
  ADMIN_PREFIX,
  NOTIFICATIONS_PATH,
  PASSWORD_RESET_PATH,
  TRANSFER_REQUESTS_PATH,
  TRANSFER_REQUEST_PATH,
} from './paths.js'
export {
  ADMIN_LANDING_PATH,
  adminShellRoutes,
  isAdminPath,
} from './admin-routes.js'

const INVITATION_ROUTE_PATH = `${INVITATION_PREFIX}/:token`

type HostRouteDeclaration = RouteDeclaration & {
  readonly requires?: readonly FeatureName[]
  readonly icon?: ReactElement
}

function AccountLandingRedirect(): null {
  const redirect = useRedirect()
  const { search } = useLocationPath()
  useEffect(() => {
    const landing = new URLSearchParams(search)
    const picked = landing.get('section')
    const pane = ACCOUNT_SECTIONS.find((section) => section === picked)
    landing.delete('section')
    const rest = landing.toString()
    const target =
      pane === undefined ? ACCOUNT_SECTION_PATHS.profile : ACCOUNT_SECTION_PATHS[pane]
    redirect(rest === '' ? target : `${target}?${rest}`)
  }, [redirect, search])
  return null
}

export const businessRoutes: readonly HostRouteDeclaration[] = [
  {
    path: TENANT_HOME_PATH,
    view: () => <HomePage />,
    label: <DemoNavLabel labelKey="nav.home" />,
    icon: <HomeNavIcon />,
  },
  {
    path: ACCOUNT_PATH,
    view: () => <AccountLandingRedirect />,
    label: <DemoNavLabel labelKey="nav.account" />,
    icon: <AccountNavIcon />,
  },
  {
    path: ACCOUNT_SECTION_PATHS.profile,
    view: () => <AccountSettingsPage section="profile" />,
  },
  {
    path: ACCOUNT_SECTION_PATHS.security,
    view: () => <AccountSettingsPage section="security" />,
  },
  {
    path: ACCOUNT_SECTION_PATHS.sessions,
    view: () => <AccountSettingsPage section="sessions" />,
  },
  {
    path: ACCOUNT_SECTION_PATHS.preferences,
    view: () => <AccountSettingsPage section="preferences" />,
  },
  {
    path: ORGANIZATIONS_NEW_PATH,
    view: () => <OrganizationCreateRoute />,
    requires: ['org.create'],
  },
  {
    path: TRANSFER_REQUESTS_PATH,
    view: () => <TransferRequestsPage />,
  },
  {
    path: TRANSFER_REQUEST_PATH,
    view: (context) => (
      <TransferRequestsPage request={context.params.request ?? ''} />
    ),
  },
  ...notificationRoutes.map((route) => ({
    ...route,
    view: () => <NotificationsPage />,
    icon: <NotificationsNavIcon />,
  })),
  {
    path: ACCOUNT_CALLBACK_PATH,
    view: () => <AccountBindingCallbackPage />,
  },
] satisfies readonly HostRouteDeclaration[]

const NAV_ICONS: ReadonlyMap<string, ReactElement> = new Map(
  businessRoutes.flatMap((route) =>
    route.icon === undefined
      ? []
      : ([[route.path, route.icon]] as const),
  ),
)

export function navIconFor(path: string): ReactElement | undefined {
  return NAV_ICONS.get(path)
}

export const authCallbackRoutes: RouteTable = [
  {
    path: AUTH_CALLBACK_PATH,
    view: () => <AuthCallbackPage />,
  },
] satisfies readonly HostRouteDeclaration[]

export const invitationRoutes: RouteTable = [
  {
    path: INVITATION_ROUTE_PATH,
    view: (context) => (
      <AcceptInvitationPage token={context.params.token ?? ''} />
    ),
  },
] satisfies readonly HostRouteDeclaration[]

export const passwordResetRoutes: RouteTable = [
  {
    path: PASSWORD_RESET_PATH,
    view: () => <PasswordResetPage />,
  },
] satisfies readonly HostRouteDeclaration[]

export const routes: RouteTable = [
  ...businessRoutes,
  ...authCallbackRoutes,
  ...invitationRoutes,
  ...passwordResetRoutes,
  ...adminShellRoutes,
  ...organizationAdminShellRoutes,
]
