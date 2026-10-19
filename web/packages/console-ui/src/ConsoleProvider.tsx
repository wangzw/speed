/**
 * ConsoleProvider: the console's package-level contexts without the frame.
 *
 * ConsoleShell is one composition of the console: @speed/layout-kit's
 * AppShell around this provider around the host's routed content. A host
 * that renders a platform-staff frame of its own -- its own chrome, a
 * dialog, a side panel, an overlay that sits outside the console's main
 * area -- mounts this provider instead and keeps the console's seams
 * (navigation, session, account destination, invitation link,
 * impersonation change, impersonation start, role descriptions, feature
 * presence and the route table) available to the console pages it renders
 * inside it.
 *
 * It installs exactly the contexts ConsoleShell installs, with the same
 * defaults, and renders no DOM of its own. It remains a platform-staff
 * surface: it must not host tenant-facing pages, and it carries no
 * current-tenant affordance (see the package README and AGENTS.md).
 *
 * `hasFeature` is required, as on the shell: a provider that guessed an
 * answer would render feature-gated controls the deployment does not
 * serve. Every other prop is optional and its absence changes nothing --
 * the corresponding context is absent (or empty) and the pages keep their
 * documented fallbacks.
 */

import type { ReactNode } from 'react'
import type { AuthSession } from '@speed/auth-core'
import type { FeatureName } from '@speed/api-sdk/features'
import type { ConsoleRoute } from './route.js'
import type { ConsoleImpersonationChange } from './internal/impersonation-change.js'
import type { ConsoleImpersonationStarting } from './internal/impersonation-starting.js'
import type { ConsoleDescribeRole } from './internal/describe-role.js'
import { ConsoleAccountHrefProvider } from './internal/account-href.js'
import { ConsoleDescribeRoleProvider } from './internal/describe-role.js'
import { ConsoleFeatureProvider } from './internal/features.js'
import { ConsoleImpersonationChangeProvider } from './internal/impersonation-change.js'
import { ConsoleImpersonationStartingProvider } from './internal/impersonation-starting.js'
import { ConsoleInvitationLinkProvider } from './internal/invitation-link.js'
import { ConsoleNavigationProvider } from './internal/navigation.js'
import { ConsoleRouteTableProvider } from './internal/route-table.js'
import { ConsoleSessionProvider } from './internal/session.js'

export interface ConsoleProviderProps {
  /**
   * The host's synchronous feature answer: whether the process serves the
   * named feature. Required, as on the shell.
   */
  readonly hasFeature: (name: FeatureName) => boolean
  /**
   * The host's in-place navigation callback. Supplying it routes a plain
   * click on a console link through the host's router; a modifier or
   * new-tab click stays the browser's.
   */
  readonly onNavigate?: (href: string) => void
  /**
   * The assembled session, published so a step-up-gated write can raise
   * it. Absent, a page's step-up-gated writes fire unguarded.
   */
  readonly session?: AuthSession
  /**
   * Builds the acceptance link for an issued invitation's token. Absent,
   * the page shows the bare token instead.
   */
  readonly invitationLink?: (token: string) => string
  /**
   * The host's account settings page, where an operator registers
   * two-step verification. Absent, the step-up dialog renders text only.
   */
  readonly accountHref?: string
  /**
   * The host's action after an impersonation start or end settles: it
   * evicts the cache taken under the old identity, re-reads the session
   * and the identity-dependent host state, and moves the page.
   */
  readonly onImpersonationChange?: (
    change: ConsoleImpersonationChange,
  ) => void | Promise<void>
  /**
   * The host's hook fired synchronously before an impersonation start is
   * written. The returned release runs when the write is refused, blocked
   * by step-up, or throws; a write that lands is the host's to release
   * after its `onImpersonationChange('started')` action settles.
   */
  readonly onImpersonationStarting?: ConsoleImpersonationStarting
  /**
   * Resolves a role's `description_key` to display content. Absent, a
   * page shows the raw key.
   */
  readonly describeRole?: ConsoleDescribeRole
  /**
   * The route table whose declarations the pages resolve sibling links
   * against, by stable `id`. The provider publishes it unchanged; the
   * host hands over the table its feature presence predicate narrowed.
   * Absent publishes an empty table, so sibling links resolve to nothing.
   */
  readonly routes?: readonly ConsoleRoute[]
  /** The console pages (and whatever frame the host renders them in). */
  readonly children: ReactNode
}

/**
 * The frameless console: installs the package's contexts around the
 * children and renders nothing else.
 */
export function ConsoleProvider({
  hasFeature,
  onNavigate,
  session,
  invitationLink,
  accountHref,
  onImpersonationChange,
  onImpersonationStarting,
  describeRole,
  routes,
  children,
}: ConsoleProviderProps) {
  return (
    <ConsoleNavigationProvider onNavigate={onNavigate}>
      <ConsoleRouteTableProvider routes={routes}>
        <ConsoleSessionProvider session={session ?? null}>
          <ConsoleAccountHrefProvider accountHref={accountHref}>
            <ConsoleInvitationLinkProvider invitationLink={invitationLink}>
              <ConsoleImpersonationChangeProvider
                onImpersonationChange={onImpersonationChange}
              >
                <ConsoleImpersonationStartingProvider
                  onImpersonationStarting={onImpersonationStarting}
                >
                  <ConsoleDescribeRoleProvider describeRole={describeRole}>
                    <ConsoleFeatureProvider hasFeature={hasFeature}>
                      {children}
                    </ConsoleFeatureProvider>
                  </ConsoleDescribeRoleProvider>
                </ConsoleImpersonationStartingProvider>
              </ConsoleImpersonationChangeProvider>
            </ConsoleInvitationLinkProvider>
          </ConsoleAccountHrefProvider>
        </ConsoleSessionProvider>
      </ConsoleRouteTableProvider>
    </ConsoleNavigationProvider>
  )
}
