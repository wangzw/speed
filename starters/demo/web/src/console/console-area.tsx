/**
 * The console area: the platform-staff face as the app's second shell.
 *
 * It renders @speed/console-ui's frame around the routed console page. The
 * console's administration surfaces mount on endpoints that resolve no tenant,
 * so this shell carries no tenant switcher -- it is a different frame from the
 * tenant-facing ProductShell, and the app chooses between them by which area
 * of its route table the URL lands in (src/app.tsx).
 *
 * The console's pages reach /api/v1/system/tenants, /api/v1/system/settings and /api/v1/system/audit
 * through the operations bundle console-ui ships, over the request function
 * the app-entry assembly bound into the @speed/api-sdk runtime seam
 * (src/main.tsx's bootstrapSpeedApp call), so this area mounts no provider of
 * its own. A product that must change that wiring mounts console-ui's
 * ConsoleOperationsProvider here instead.
 *
 * The frame's navigation is routed in place: the shell hands each entry's
 * activation to the app's router (route-kit's navigate), so moving between
 * console pages pushes a history entry instead of reloading the page -- the
 * skeleton's session lives in memory, so a reload would sign the operator out.
 *
 * The frame's top bar is the same chrome the tenant frame carries -- the brand
 * and the sign-out action (src/chrome) -- plus an exit back to the tenant area,
 * so the console is not a navigation dead end. This one has no tenant
 * switcher, which is why the exit is a plain link rather than a switcher.
 */

import Box from '@mui/material/Box'
import Link from '@mui/material/Link'
import type { ConsoleNavGroup, ConsoleRouteTable } from '@speed/console-ui'
import { ConsoleShell, consoleNavGroups } from '@speed/console-ui'
import { useTranslation } from '@speed/i18n'
import { useNavigate } from '@speed/route-kit'
import type { FeatureName } from '@speed/api-sdk/features'
import type { ReactElement, ReactNode } from 'react'
import { useAppServices } from '../app-services.js'
import { AppBrand, HeaderActions, UserMenu } from '../chrome.js'
import { invitationPath, ACCOUNT_PATH } from '../paths.js'
import { DEMO_NAMESPACE } from '../resources.js'

/** The tenant area's root: the app's business slot, which the console's exit
 * returns the operator to. */
const TENANT_HOME_PATH = '/'

export interface ConsoleAreaProps {
  /** The console's page declarations, whose labelled entries become its navigation. */
  readonly routes: ConsoleRouteTable
  /**
   * The console sidebar's group table. Defaults to console-ui's own
   * `consoleNavGroups`, so the demo's console sidebar renders its eleven pages
   * grouped by capability domain; a host that wants a flat column passes an
   * explicit value only by overriding this.
   */
  readonly navGroups?: readonly ConsoleNavGroup[]
  /**
   * The host's synchronous feature answer, published to the routed pages so
   * a feature-gated control can hide when its feature is absent.
   */
  readonly hasFeature: (name: FeatureName) => boolean
  /** The routed console page, rendered inside the frame's main landmark. */
  readonly children: ReactNode
  /**
   * The desktop sidebar's collapse state, host-held. The console frame
   * renders the collapse toggle and reports each flip through
   * `onSidebarCollapsedChange`; the app holds one state for both areas so the
   * collapse carries across the tenant/console switch.
   */
  readonly sidebarCollapsed?: boolean
  /** Fired when the sidebar collapse state should change. */
  readonly onSidebarCollapsedChange?: (collapsed: boolean) => void
  /**
   * The host's action after an impersonation start or end settles. The
   * console page reports the change without waiting for it; the host
   * re-reads the identity the same way the tenant frame's banner does.
   */
  readonly onImpersonationChange?: (
    change: 'started' | 'ended',
  ) => void | Promise<void>
}

export function ConsoleArea({
  routes,
  navGroups,
  hasFeature,
  children,
  sidebarCollapsed,
  onSidebarCollapsedChange,
  onImpersonationChange,
}: ConsoleAreaProps): ReactElement {
  const navigate = useNavigate()
  const { session } = useAppServices()
  return (
    <ConsoleShell
      routes={routes}
      // The console's own group table groups the eleven pages by capability
      // domain, so the sidebar reads as sections rather than one long list.
      navGroups={navGroups ?? consoleNavGroups}
      hasFeature={hasFeature}
      title={<AppBrand />}
      headerActions={<ConsoleHeaderActions />}
      onNavigate={navigate}
      // The session a step-up-gated console write elevates: a privileged
      // write refused for a strength shortfall opens the re-auth dialog,
      // which drives this session's step-up flow.
      session={session}
      // The console issues an invitation and shows the acceptance link for
      // the operator to hand over; the link points at this app's own landing
      // route, whose path only the host knows (src/paths).
      invitationLink={invitationPath}
      // The account settings page the step-up dialog points at when the
      // operator's account has no active factor; the path is this app's own
      // (src/paths), reachable at /account.
      accountHref={ACCOUNT_PATH}
      // Starting or ending an impersonation changes whose identity every
      // later read belongs to; the app root's action settles the cache,
      // the session and the identity-dependent host state, then moves the
      // operator home after a start.
      onImpersonationChange={onImpersonationChange}
      sidebarCollapsed={sidebarCollapsed}
      onSidebarCollapsedChange={onSidebarCollapsedChange}
    >
      {children}
    </ConsoleShell>
  )
}

/**
 * The console banner's trailing chrome: an exit back to the tenant area beside
 * the sign-out action, in the same chrome the tenant frame carries. The exit
 * routes in place (the same seam the frame's own navigation uses), so leaving
 * the console does not reload the page and sign the operator out.
 */
function ConsoleHeaderActions(): ReactElement {
  const navigate = useNavigate()
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
      <Link
        href={TENANT_HOME_PATH}
        underline="hover"
        onClick={(event) => {
          event.preventDefault()
          navigate(TENANT_HOME_PATH)
        }}
        sx={{ color: 'inherit' }}
      >
        {t('console.backToApp')}
      </Link>
      <HeaderActions />
      <UserMenu />
    </Box>
  )
}
