/**
 * AdminProvider: the console's package-level contexts without the frame. It
 * installs the same contexts AdminShell installs, with the same defaults, and
 * renders no DOM; `hasFeature` is required and every other prop is optional.
 */

import type { ReactNode } from 'react'
import type { AuthSession } from '@speed/auth-core'
import type { FeatureName } from '@speed/api-sdk/features'
import type { AdminRoute } from './route.js'
import type { AdminImpersonationChange } from './internal/impersonation-change.js'
import type { AdminImpersonationStarting } from './internal/impersonation-starting.js'
import { AdminAccountHrefProvider } from './internal/account-href.js'
import { AdminReturnPathProvider } from './internal/return-path.js'
import { AdminFeatureProvider } from './internal/features.js'
import { AdminImpersonationChangeProvider } from './internal/impersonation-change.js'
import { AdminImpersonationStartingProvider } from './internal/impersonation-starting.js'
import { AdminInvitationLinkProvider } from './internal/invitation-link.js'
import { AdminNavigationProvider } from './internal/navigation.js'
import { AdminRouteTableProvider } from './internal/route-table.js'
import { AdminSessionProvider } from './internal/session.js'

/** The props of the frameless console provider. */
export interface AdminProviderProps {
  /** The host's synchronous feature answer; required, as on the shell. */
  readonly hasFeature: (name: FeatureName) => boolean
  /** The host's in-place navigation callback; a modifier or new-tab click stays the browser's. */
  readonly onNavigate?: (href: string) => void
  /** The session a step-up-gated write may raise; absent, writes fire unguarded. */
  readonly session?: AuthSession
  /** Builds the acceptance link for a token; absent, the page shows the bare token. */
  readonly invitationLink?: (token: string) => string
  /** The host's account settings page; absent, the step-up dialog renders text only. */
  readonly accountHref?: string
  /** The host's current path, sent as the step-up flow's `return_to`. */
  readonly returnPath?: string
  /** The host's action after an impersonation start or end settles. */
  readonly onImpersonationChange?: (
    change: AdminImpersonationChange,
  ) => void | Promise<void>
  /**
   * The host's hook fired before an impersonation start is written; the returned
   * release runs when the attempt does not land.
   */
  readonly onImpersonationStarting?: AdminImpersonationStarting
  /** The host's narrowed route table; absent publishes an empty table. */
  readonly routes?: readonly AdminRoute[]
  /** The console pages (and whatever frame the host renders them in). */
  readonly children: ReactNode
}

/** The frameless console: installs the package's contexts and renders nothing else. */
export function AdminProvider({
  hasFeature,
  onNavigate,
  session,
  invitationLink,
  accountHref,
  returnPath,
  onImpersonationChange,
  onImpersonationStarting,
  routes,
  children,
}: AdminProviderProps) {
  return (
    <AdminNavigationProvider onNavigate={onNavigate}>
      <AdminRouteTableProvider routes={routes}>
        <AdminSessionProvider session={session ?? null}>
          <AdminAccountHrefProvider accountHref={accountHref}>
            <AdminReturnPathProvider returnPath={returnPath}>
              <AdminInvitationLinkProvider invitationLink={invitationLink}>
                <AdminImpersonationChangeProvider
                  onImpersonationChange={onImpersonationChange}
                >
                  <AdminImpersonationStartingProvider
                    onImpersonationStarting={onImpersonationStarting}
                  >
                    <AdminFeatureProvider hasFeature={hasFeature}>
                      {children}
                    </AdminFeatureProvider>
                  </AdminImpersonationStartingProvider>
                </AdminImpersonationChangeProvider>
              </AdminInvitationLinkProvider>
            </AdminReturnPathProvider>
          </AdminAccountHrefProvider>
        </AdminSessionProvider>
      </AdminRouteTableProvider>
    </AdminNavigationProvider>
  )
}
