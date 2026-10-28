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

import { useCallback } from 'react'
import Box from '@mui/material/Box'
import IconButton from '@mui/material/IconButton'
import Link from '@mui/material/Link'
import type {
  ConsoleImpersonationStarting,
  ConsoleNavGroup,
  ConsoleRouteTable,
} from '@speed/console-ui'
import { ConsoleShell, CONSOLE_UI_NAMESPACE, consoleNavGroups } from '@speed/console-ui'
import { useTranslation } from '@speed/i18n'
import type { AppShellShellLayout } from '@speed/layout-kit'
import { useLocationPath, useNavigate, useNavItems, useRouteLinkProps } from '@speed/route-kit'
import type { FeatureName } from '@speed/api-sdk/features'
import type { ReactElement, ReactNode } from 'react'
import { useAppServices } from '../app-services.js'
import { CallbackLandingNotice } from '../callback-error-notice.js'
import { AppBrand, HeaderActions, UserMenu } from '../chrome.js'
import { TENANT_HOME_PATH } from '../pages/not-found-page.js'
import { invitationPath, ACCOUNT_PATH } from '../paths.js'
import { DEMO_NAMESPACE } from '../resources.js'

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
   * The shell form the deployment selects (src/runtime-config.ts). Absent
   * keeps the console frame's top-bar default.
   */
  readonly shellLayout?: AppShellShellLayout
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
  /**
   * The host's hook before an impersonation start is written: the frame
   * enters its "starting" state ahead of the request and releases a write
   * that does not land (the app's read hold, src/impersonation.tsx).
   */
  readonly onImpersonationStarting?: ConsoleImpersonationStarting
}

export function ConsoleArea({
  routes,
  navGroups,
  hasFeature,
  children,
  shellLayout,
  sidebarCollapsed,
  onSidebarCollapsedChange,
  onImpersonationChange,
  onImpersonationStarting,
}: ConsoleAreaProps): ReactElement {
  const navigate = useNavigate()
  const { session } = useAppServices()
  // The page the operator is on: a step-up flow started here sends it as its
  // `return_to`, so a redirect round trip lands back on this page and its
  // refusal is read here.
  const { path: pathname } = useLocationPath()
  // The console entries the router derived for the current URL: the frame
  // marks a row selected by asking whether that row's path is one of them
  // and is the selected one (the shell does no path matching of its own).
  const items = useNavItems()
  const isSelected = useCallback(
    (path: string): boolean =>
      items.some((item) => item.id === path && item.selected),
    [items],
  )
  return (
    <ConsoleShell
      routes={routes}
      // The console's own group table groups the eleven pages by capability
      // domain, so the sidebar reads as sections rather than one long list.
      navGroups={navGroups ?? consoleNavGroups}
      hasFeature={hasFeature}
      title={<AppBrand />}
      headerActions={(rail) => <ConsoleHeaderActions narrow={rail.narrow} />}
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
      // The page this console URL names: a step-up flow's redirect round
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
      {/* A step-up round trip started from this console page lands back here
          with a refusal's problem code in the fragment: the console area
          reads it and words it above the page. */}
      <Box sx={{ mb: 2, maxWidth: 960 }}>
        <CallbackLandingNotice namespace={CONSOLE_UI_NAMESPACE} />
      </Box>
      {children}
    </ConsoleShell>
  )
}

/**
 * The console banner's trailing chrome: an exit back to the tenant area beside
 * the sign-out action, in the same chrome the tenant frame carries. The exit
 * routes in place (the same seam the frame's own navigation uses), so leaving
 * the console does not reload the page and sign the operator out. In the
 * collapsed sidebar rail only the arrow fits, and its accessible name stays
 * the exit label.
 */
function ConsoleHeaderActions({
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
          aria-label={t('console.backToApp')}
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
          {t('console.backToApp')}
        </Link>
      )}
      <HeaderActions />
      <UserMenu />
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
