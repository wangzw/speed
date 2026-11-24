import { useCallback } from 'react'
import Box from '@mui/material/Box'
import type {
  AdminImpersonationStarting,
  AdminNavGroup,
  AdminRouteTable,
} from '@speed/admin-ui'
import { AdminShell, ADMIN_UI_NAMESPACE, adminNavGroups } from '@speed/admin-ui'
import { useTranslation } from '@speed/i18n'
import type { AppShellShellLayout } from '@speed/layout-kit'
import { useLocationPath, useNavigate, useNavItems } from '@speed/route-kit'
import type { FeatureName } from '@speed/api-sdk/features'
import type { ReactElement, ReactNode } from 'react'
import {
  accountEntryInNavFooter,
  accountEntryInUserSlot,
} from '../account-entry.js'
import { useAppServices } from '../app-services.js'
import { CallbackLandingNotice } from '../callback-error-notice.js'
import { AccountEntry, AppBrand, UserMenu } from '../chrome.js'
import type { AccountMenuDestination } from '../chrome.js'
import { TENANT_HOME_PATH } from '../pages/not-found-page.js'
import { invitationPath, ACCOUNT_PATH } from '../paths.js'
import { DEMO_NAMESPACE } from '../resources.js'

export interface AdminAreaProps {
  readonly routes: AdminRouteTable
  readonly navGroups?: readonly AdminNavGroup[]
  readonly hasFeature: (name: FeatureName) => boolean
  readonly children: ReactNode
  readonly shellLayout?: AppShellShellLayout
  readonly sidebarCollapsed?: boolean
  readonly onSidebarCollapsedChange?: (collapsed: boolean) => void
  readonly onImpersonationChange?: (
    change: 'started' | 'ended',
  ) => void | Promise<void>
  readonly onImpersonationStarting?: AdminImpersonationStarting
}

export function AdminArea({
  routes,
  navGroups,
  hasFeature,
  children,
  shellLayout,
  sidebarCollapsed,
  onSidebarCollapsedChange,
  onImpersonationChange,
  onImpersonationStarting,
}: AdminAreaProps): ReactElement {
  const navigate = useNavigate()
  const { session } = useAppServices()
  const { path: pathname } = useLocationPath()
  const items = useNavItems()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const destination: AccountMenuDestination = {
    id: 'back-to-app',
    label: t('admin.backToApp'),
    target: TENANT_HOME_PATH,
    icon: <BackGlyph />,
  }
  const isSelected = useCallback(
    (path: string): boolean =>
      items.some((item) => item.id === path && item.selected),
    [items],
  )
  return (
    <AdminShell
      routes={routes}
      navGroups={navGroups ?? adminNavGroups}
      hasFeature={hasFeature}
      title={<AppBrand />}
      headerActions={
        accountEntryInUserSlot(shellLayout)
          ? () => <UserMenu destination={destination} />
          : undefined
      }
      navFooter={(rail) =>
        !accountEntryInNavFooter(shellLayout) ? null : (
          <AccountEntry
            narrow={rail.narrow}
            closeDrawer={rail.closeDrawer}
            destination={destination}
          />
        )
      }
      onNavigate={navigate}
      session={session}
      invitationLink={invitationPath}
      accountHref={ACCOUNT_PATH}
      returnPath={pathname}
      onImpersonationChange={onImpersonationChange}
      onImpersonationStarting={onImpersonationStarting}
      isSelected={isSelected}
      shellLayout={shellLayout}
      sidebarCollapsed={sidebarCollapsed}
      onSidebarCollapsedChange={onSidebarCollapsedChange}
    >
      <Box sx={{ mb: 2, maxWidth: 960, '&:empty': { display: 'none' } }}>
        <CallbackLandingNotice namespace={ADMIN_UI_NAMESPACE} />
      </Box>
      {children}
    </AdminShell>
  )
}

function BackGlyph() {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={20}
      height={20}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M19 12H5" />
      <path d="M10.5 6.5 5 12l5.5 5.5" />
    </svg>
  )
}
