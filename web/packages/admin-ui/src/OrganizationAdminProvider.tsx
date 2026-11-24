/**
 * OrganizationAdminProvider: the organization surface's contexts without a frame;
 * it never derives or switches the current tenant.
 */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'
import type { AuthSession } from '@speed/auth-core'
import type { FeatureName } from '@speed/api-sdk/features'
import type { PageHeaderBreadcrumb } from '@speed/ui-kit'
import type { AdminRoute } from './route.js'
import type { AdminOperations } from './operations.js'
import { AdminOperationsProvider } from './operations.js'
import { AdminFeatureProvider } from './internal/features.js'
import { AdminNavigationProvider } from './internal/navigation.js'
import { AdminRouteTableProvider } from './internal/route-table.js'
import { AdminInvitationLinkProvider } from './internal/invitation-link.js'
import { AdminSessionProvider } from './internal/session.js'
import { OrganizationAdminBreadcrumbProvider } from './OrganizationAdminBreadcrumb.js'
import { OrganizationAdminOperationsProvider } from './organization-operations.js'
import type { OrganizationAdminOperations } from './organization-operations.js'

/** The organization facts the wrapper pages read. */
export interface OrganizationAdminContextValue {
  /** The current tenant the host resolved; every request names it. */
  readonly tenant: string
  /** The role names the host seeded and the role page renders as read-only. */
  readonly protectedRoles: readonly string[]
  /** The host's action after a profile save lands; absent, the page re-reads its own read. */
  readonly onTenantUpdated?: () => void | Promise<unknown>
  /**
   * The host's action after a deletion ends the caller's membership: leave,
   * reload the caller tenant list, show the message there. Absent, the profile
   * page falls back to `onTenantUpdated`.
   */
  readonly onOrganizationLeft?: (message: string) => void | Promise<unknown>
}

const OrganizationAdminContext = createContext<OrganizationAdminContextValue | null>(
  null,
)

/** The organization context. Throws outside the provider, since a page would have no tenant. */
export function useOrganizationAdminContext(): OrganizationAdminContextValue {
  const value = useContext(OrganizationAdminContext)
  if (value === null) {
    throw new Error(
      'OrganizationAdminProvider is required to render an organization admin page',
    )
  }
  return value
}

/** The current tenant the host handed the organization surface. */
export function useOrganizationAdminTenant(): string {
  return useOrganizationAdminContext().tenant
}

/** The role names the tenant role page renders as read-only. */
export function useOrganizationAdminProtectedRoles(): readonly string[] {
  return useOrganizationAdminContext().protectedRoles
}

/** The host's action after a profile save lands, when the host supplied one. */
export function useOrganizationAdminOnTenantUpdated():
  | (() => void | Promise<unknown>)
  | undefined {
  return useOrganizationAdminContext().onTenantUpdated
}

/** The host's action after a deletion ends the caller's membership, when supplied. */
export function useOrganizationAdminOnOrganizationLeft():
  | ((message: string) => void | Promise<unknown>)
  | undefined {
  return useOrganizationAdminContext().onOrganizationLeft
}

/** The props of OrganizationAdminProvider. */
export interface OrganizationAdminProviderProps {
  /** The current tenant the host resolved; never derived or switched here. */
  readonly tenant: string
  /** The tenant-scoped operations bundle the organization pages run through. */
  readonly operations: OrganizationAdminOperations
  /** The host's synchronous feature answer; required, as on the console shell. */
  readonly hasFeature: (name: FeatureName) => boolean
  /** The session a step-up-gated write may raise. */
  readonly session?: AuthSession
  /** The organization declarations the host's feature predicate left in force. */
  readonly routes?: readonly AdminRoute[]
  /** The host's in-place navigation callback. */
  readonly onNavigate?: (href: string) => void
  /** The trail from the organization home to the page; mounts the breadcrumb provider. */
  readonly breadcrumbs?: readonly PageHeaderBreadcrumb[]
  /** Builds the acceptance link for an issued token; absent, the page shows the bare token. */
  readonly invitationLink?: (token: string) => string
  /** Role names the role page renders as read-only; absent, none is protected. */
  readonly protectedRoles?: readonly string[]
  /** The host's action after a profile save lands. */
  readonly onTenantUpdated?: () => void | Promise<unknown>
  /**
   * The host's action after a deletion ends the caller's membership; absent, the
   * profile page falls back to `onTenantUpdated`.
   */
  readonly onOrganizationLeft?: (message: string) => void | Promise<unknown>
  readonly children: ReactNode
}

/** The frameless organization surface: installs its contexts and renders nothing else. */
export function OrganizationAdminProvider({
  tenant,
  operations,
  hasFeature,
  session,
  routes,
  onNavigate,
  breadcrumbs,
  invitationLink,
  protectedRoles,
  onTenantUpdated,
  onOrganizationLeft,
  children,
}: OrganizationAdminProviderProps) {
  // The organization bundle is wider in the tenant members and narrower in the
  // platform ones than AdminOperations. The shared pages under this provider
  // call only members it carries; the cast is the one place the two shapes meet.
  const scopedOperations = operations as unknown as AdminOperations
  const content = (
    <OrganizationAdminContext.Provider
      value={{
        tenant,
        protectedRoles: protectedRoles ?? [],
        onTenantUpdated,
        onOrganizationLeft,
      }}
    >
      <AdminOperationsProvider operations={scopedOperations}>
        <OrganizationAdminOperationsProvider operations={operations}>
          <AdminNavigationProvider onNavigate={onNavigate}>
            <AdminRouteTableProvider routes={routes}>
              <AdminSessionProvider session={session ?? null}>
                <AdminInvitationLinkProvider invitationLink={invitationLink}>
                  <AdminFeatureProvider hasFeature={hasFeature}>
                    {children}
                  </AdminFeatureProvider>
                </AdminInvitationLinkProvider>
              </AdminSessionProvider>
            </AdminRouteTableProvider>
          </AdminNavigationProvider>
        </OrganizationAdminOperationsProvider>
      </AdminOperationsProvider>
    </OrganizationAdminContext.Provider>
  )
  return breadcrumbs === undefined ? (
    content
  ) : (
    <OrganizationAdminBreadcrumbProvider breadcrumbs={breadcrumbs}>
      {content}
    </OrganizationAdminBreadcrumbProvider>
  )
}
