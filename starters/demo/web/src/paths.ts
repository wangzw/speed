import { notificationRoutes } from '@speed/notification-ui'

export const ADMIN_PREFIX = '/admin'

export const ORGANIZATION_ADMIN_PREFIX = '/organization/admin'

export const ORGANIZATION_ADMIN_PROFILE_PATH = `${ORGANIZATION_ADMIN_PREFIX}/profile`

export const ORGANIZATIONS_NEW_PATH = '/organizations/new'

export function isOrganizationAdminPath(pathname: string): boolean {
  return (
    pathname === ORGANIZATION_ADMIN_PREFIX ||
    pathname.startsWith(`${ORGANIZATION_ADMIN_PREFIX}/`)
  )
}

export const AUTH_CALLBACK_PATH = '/sign-in/callback'

export const PASSWORD_RESET_PATH = '/password-reset'

export const ACCOUNT_PATH = '/account'

export const ACCOUNT_SECTIONS = [
  'profile',
  'security',
  'sessions',
  'preferences',
] as const

export type AccountSection = (typeof ACCOUNT_SECTIONS)[number]

export const ACCOUNT_SECTION_PATHS: Record<AccountSection, string> = {
  profile: `${ACCOUNT_PATH}/profile`,
  security: `${ACCOUNT_PATH}/security`,
  sessions: `${ACCOUNT_PATH}/sessions`,
  preferences: `${ACCOUNT_PATH}/preferences`,
}

export const NOTIFICATIONS_PATH: string = notificationRoutes[0]?.path ?? '/'

export const ACCOUNT_CALLBACK_PATH = '/account/callback'

export const TRANSFER_REQUESTS_PATH = '/transfer-requests'

export const TRANSFER_REQUEST_PATH = `${TRANSFER_REQUESTS_PATH}/:request`

export function transferRequestPath(id: string): string {
  return `${TRANSFER_REQUESTS_PATH}/${encodeURIComponent(id)}`
}

const ACCOUNT_LEVEL_PATHS: ReadonlySet<string> = new Set([
  ACCOUNT_PATH,
  ...Object.values(ACCOUNT_SECTION_PATHS),
  ACCOUNT_CALLBACK_PATH,
  TRANSFER_REQUESTS_PATH,
])

export function isAccountLevelPath(pathname: string): boolean {
  return (
    ACCOUNT_LEVEL_PATHS.has(pathname) ||
    pathname.startsWith(`${TRANSFER_REQUESTS_PATH}/`)
  )
}

export const INVITATION_PREFIX = '/invitations'

export function invitationPath(token: string): string {
  return `${INVITATION_PREFIX}/${encodeURIComponent(token)}`
}
