/**
 * The console area: the platform-staff face as the app's second shell.
 *
 * It renders @speed/console-ui's frame around the routed console page. The
 * console's administration surfaces mount on endpoints that resolve no tenant,
 * so this shell carries no tenant switcher -- it is a different frame from the
 * tenant-facing ProductShell, and the app chooses between them by which area
 * of its route table the URL lands in (src/app.tsx).
 *
 * The console's pages reach /tenant/admin, /settings/admin and /audit/admin
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
import type { ConsoleRouteTable } from '@speed/console-ui'
import { ConsoleShell } from '@speed/console-ui'
import { useTranslation } from '@speed/i18n'
import { useNavigate } from '@speed/route-kit'
import type { ReactElement, ReactNode } from 'react'
import { AppBrand, HeaderActions, UserMenu } from '../chrome.js'
import { invitationPath } from '../paths.js'
import { DEMO_NAMESPACE } from '../resources.js'

/** The tenant area's root: the app's business slot, which the console's exit
 * returns the operator to. */
const TENANT_HOME_PATH = '/'

export interface ConsoleAreaProps {
  /** The console's page declarations, whose labelled entries become its navigation. */
  readonly routes: ConsoleRouteTable
  /** The routed console page, rendered inside the frame's main landmark. */
  readonly children: ReactNode
}

export function ConsoleArea({ routes, children }: ConsoleAreaProps): ReactElement {
  const navigate = useNavigate()
  return (
    <ConsoleShell
      routes={routes}
      title={<AppBrand />}
      headerActions={<ConsoleHeaderActions />}
      onNavigate={navigate}
      // The console issues an invitation and shows the acceptance link for
      // the operator to hand over; the link points at this app's own landing
      // route, whose path only the host knows (src/paths).
      invitationLink={invitationPath}
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
