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
 * The frame's top bar is the same chrome the tenant frame carries -- the brand
 * and the sign-out action (src/chrome) -- so the two shells read as one
 * product's chrome rather than two, even though this one has no tenant
 * switcher.
 */

import type { ConsoleRouteTable } from '@speed/console-ui'
import { ConsoleShell } from '@speed/console-ui'
import type { ReactElement, ReactNode } from 'react'
import { AppBrand, UserMenu } from '../chrome.js'

export interface ConsoleAreaProps {
  /** The console's page declarations, whose labelled entries become its navigation. */
  readonly routes: ConsoleRouteTable
  /** The routed console page, rendered inside the frame's main landmark. */
  readonly children: ReactNode
}

export function ConsoleArea({ routes, children }: ConsoleAreaProps): ReactElement {
  return (
    <ConsoleShell
      routes={routes}
      title={<AppBrand />}
      headerActions={<UserMenu />}
    >
      {children}
    </ConsoleShell>
  )
}
