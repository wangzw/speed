/**
 * The admin area: the platform-staff face as the app's second shell.
 *
 * It renders @speed/admin-ui's frame around the routed admin page. The
 * admin's administration surfaces mount on endpoints that resolve no tenant,
 * so this shell carries no tenant switcher -- it is a different frame from the
 * tenant-facing ProductShell, and the app chooses between them by which area
 * of its route table the URL lands in (src/app.tsx).
 *
 * The admin's pages reach /api/v1/system/tenants, /api/v1/system/settings and /api/v1/system/audit
 * through the operations bundle admin-ui ships, over the request function
 * the app-entry assembly bound into the @speed/api-sdk runtime seam
 * (src/main.tsx's bootstrapSpeedApp call), so this area mounts no provider of
 * its own. A product that must change that wiring mounts admin-ui's
 * AdminOperationsProvider here instead.
 *
 * The frame's navigation is routed in place: the shell hands each entry's
 * activation to the app's router (route-kit's navigate), so moving between
 * admin pages pushes a history entry instead of reloading the page -- the
 * skeleton's session lives in memory, so a reload would sign the operator out.
 *
 * The frame's top bar is the same chrome the tenant frame carries -- the brand
 * and the sign-out action (src/chrome) -- plus an exit back to the tenant area,
 * so the admin is not a navigation dead end. This one has no tenant
 * switcher, which is why the exit is a plain link rather than a switcher.
 * The account entry follows the same ACCOUNT_ENTRY constant as the tenant
 * frame (src/account-entry): by default it is the row at the bottom of the
 * navigation column, and the header carries no avatar menu.
 */

import { useCallback } from 'react'
import Box from '@mui/material/Box'
import IconButton from '@mui/material/IconButton'
import Link from '@mui/material/Link'
import type {
  AdminImpersonationStarting,
  AdminNavGroup,
  AdminRouteTable,
} from '@speed/admin-ui'
import { AdminShell, ADMIN_UI_NAMESPACE, adminNavGroups } from '@speed/admin-ui'
import { useTranslation } from '@speed/i18n'
import type { AppShellShellLayout } from '@speed/layout-kit'
import { useLocationPath, useNavigate, useNavItems, useRouteLinkProps } from '@speed/route-kit'
import type { FeatureName } from '@speed/api-sdk/features'
import type { ReactElement, ReactNode } from 'react'
import { ACCOUNT_ENTRY } from '../account-entry.js'
import { useAppServices } from '../app-services.js'
import { CallbackLandingNotice } from '../callback-error-notice.js'
import { AccountEntry, AppBrand, UserMenu } from '../chrome.js'
import { TENANT_HOME_PATH } from '../pages/not-found-page.js'
import { invitationPath, ACCOUNT_PATH } from '../paths.js'
import { DEMO_NAMESPACE } from '../resources.js'

export interface AdminAreaProps {
  /** The admin's page declarations, whose labelled entries become its navigation. */
  readonly routes: AdminRouteTable
  /**
   * The admin sidebar's group table. Defaults to admin-ui's own
   * `adminNavGroups`, so the demo's admin sidebar renders its eleven pages
   * grouped by capability domain; a host that wants a flat column passes an
   * explicit value only by overriding this.
   */
  readonly navGroups?: readonly AdminNavGroup[]
  /**
   * The host's synchronous feature answer, published to the routed pages so
   * a feature-gated control can hide when its feature is absent.
   */
  readonly hasFeature: (name: FeatureName) => boolean
  /** The routed admin page, rendered inside the frame's main landmark. */
  readonly children: ReactNode
  /**
   * The shell form the deployment selects (src/runtime-config.ts). Absent
   * keeps the admin frame's top-bar default.
   */
  readonly shellLayout?: AppShellShellLayout
  /**
   * The desktop sidebar's collapse state, host-held. The admin frame
   * renders the collapse toggle and reports each flip through
   * `onSidebarCollapsedChange`; the app holds one state for both areas so the
   * collapse carries across the tenant/admin switch.
   */
  readonly sidebarCollapsed?: boolean
  /** Fired when the sidebar collapse state should change. */
  readonly onSidebarCollapsedChange?: (collapsed: boolean) => void
  /**
   * The host's action after an impersonation start or end settles. The
   * admin page reports the change without waiting for it; the host
   * re-reads the identity the same way the tenant frame's banner does.
   */
  readonly onImpersonationChange?: (
    change: 'started' | 'ended',
  ) => void | Promise<void>
  /**
   * The host's hook before an impersonation start is written: the frame
   * enters its "starting" state ahead of the request and releases a write
   * that does not land (the app's read hold, src/impersonation.tsx).
   */
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
  // The page the operator is on: a step-up flow started here sends it as its
  // `return_to`, so a redirect round trip lands back on this page and its
  // refusal is read here.
  const { path: pathname } = useLocationPath()
  // The admin entries the router derived for the current URL: the frame
  // marks a row selected by asking whether that row's path is one of them
  // and is the selected one (the shell does no path matching of its own).
  const items = useNavItems()
  const isSelected = useCallback(
    (path: string): boolean =>
      items.some((item) => item.id === path && item.selected),
    [items],
  )
  return (
    <AdminShell
      routes={routes}
      // The admin's own group table groups the eleven pages by capability
      // domain, so the sidebar reads as sections rather than one long list.
      navGroups={navGroups ?? adminNavGroups}
      hasFeature={hasFeature}
      title={<AppBrand />}
      headerActions={(rail) => <AdminHeaderActions narrow={rail.narrow} />}
      // The account entry's placement follows the tenant frame's constant:
      // a row at the bottom of the navigation column by default, the header
      // menu only when the host puts it there. The admin keeps no tenant
      // switcher, so the footer holds the account entry alone.
      navFooter={(rail) =>
        ACCOUNT_ENTRY === 'header' ? null : (
          <AccountEntry
            narrow={rail.narrow}
            closeDrawer={rail.closeDrawer}
          />
        )
      }
      onNavigate={navigate}
      // The session a step-up-gated admin write elevates: a privileged
      // write refused for a strength shortfall opens the re-auth dialog,
      // which drives this session's step-up flow.
      session={session}
      // The admin issues an invitation and shows the acceptance link for
      // the operator to hand over; the link points at this app's own landing
      // route, whose path only the host knows (src/paths).
      invitationLink={invitationPath}
      // The account settings page the step-up dialog points at when the
      // operator's account has no active factor; the path is this app's own
      // (src/paths), reachable at /account.
      accountHref={ACCOUNT_PATH}
      // The page this admin URL names: a step-up flow's redirect round
      // trip lands here, so this page reads the refusal.
      returnPath={pathname}
      // Starting or ending an impersonation changes whose identity every
      // later read belongs to; the app root's action settles the cache,
      // the session and the identity-dependent host state, then moves the
      // operator home after a start.
      onImpersonationChange={onImpersonationChange}
      onImpersonationStarting={onImpersonationStarting}
      isSelected={isSelected}
      shellLayout={shellLayout}
      sidebarCollapsed={sidebarCollapsed}
      onSidebarCollapsedChange={onSidebarCollapsedChange}
    >
      {/* A step-up round trip started from this admin page lands back here
          with a refusal's problem code in the fragment: the admin area
          reads it and words it above the page. */}
      <Box sx={{ mb: 2, maxWidth: 960 }}>
        <CallbackLandingNotice namespace={ADMIN_UI_NAMESPACE} />
      </Box>
      {children}
    </AdminShell>
  )
}

/**
 * The admin banner's trailing chrome: an exit back to the tenant area beside
 * the sign-out action, in the same chrome the tenant frame carries. The exit
 * routes in place (the same seam the frame's own navigation uses), so leaving
 * the admin does not reload the page and sign the operator out. In the
 * collapsed sidebar rail only the arrow fits, and its accessible name stays
 * the exit label.
 */
function AdminHeaderActions({
  narrow = false,
}: {
  /** Whether the controls render in the collapsed sidebar rail. */
  readonly narrow?: boolean
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  // The exit is a real link whose plain-click handler route-kit supplies:
  // the browser keeps a modified or new-tab click, and an in-app click moves
  // the history without a page load.
  const { href, onClick } = useRouteLinkProps(TENANT_HOME_PATH)
  return (
    <>
      {narrow ? (
        <IconButton
          color="inherit"
          aria-label={t('admin.backToApp')}
          href={href}
          onClick={onClick}
        >
          <BackGlyph />
        </IconButton>
      ) : (
        <Link
          href={href}
          underline="hover"
          onClick={onClick}
          sx={{ color: 'inherit' }}
        >
          {t('admin.backToApp')}
        </Link>
      )}
      {/* The header carries the account menu when ACCOUNT_ENTRY includes the
          header; the sidebar-only placement keeps it in the column. */}
      {ACCOUNT_ENTRY === 'sidebar' ? null : <UserMenu />}
    </>
  )
}

/** The rail's exit mark: an arrow back, drawn decorative -- the icon button
 * carries the exit label as its accessible name. */
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
