/**
 * OrganizationAdminProvider: the organization administration surface's contexts,
 * without a frame of its own.
 *
 * The host has already rendered @speed/product-shell's `AppShell` around
 * the tenant area, so the organization surface must not render a second one.
 * This provider installs only what the shared page components need: the
 * current tenant, the tenant-scoped operations bundle, the session, the
 * feature answer, the narrowed organization route table, the in-place
 * navigation callback, the organization breadcrumb, the invitation-link
 * builder and the protected role names. It renders no DOM, imports neither
 * `@speed/product-shell` nor
 * `@speed/tenancy-ui`, and never derives or switches the current tenant:
 * `tenant` is the host's fact, handed in as a required prop.
 *
 * The platform console's `AdminProvider` is the other seam and stays the
 * platform staff's; neither provider hosts the other surface's pages.
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
  /**
   * The role names the host seeded into this tenant and that the tenant
   * role page must render as read-only. Empty when the host names none.
   */
  readonly protectedRoles: readonly string[]
  /**
   * The host's action after a profile save lands: reload the caller tenant
   * list the account menu and the breadcrumb read. Absent, the profile page
   * only re-reads its own read.
   */
  readonly onTenantUpdated?: () => void | Promise<unknown>
  /**
   * The host's action after a deletion ends the caller's membership in the
   * current organization: leave the organization, reload the caller tenant
   * list the account menu and the breadcrumb read, and show the message on
   * the destination. Absent, the profile page falls back to
   * `onTenantUpdated`. A transfer changes ownership only and never departs.
   */
  readonly onOrganizationLeft?: (message: string) => void | Promise<unknown>
}

const OrganizationAdminContext = createContext<OrganizationAdminContextValue | null>(
  null,
)

/**
 * The organization context. Throws outside the provider: a wrapper page
 * mounted without a tenant would have no request to address, and failing
 * loudly at composition beats issuing a request with an empty path
 * segment.
 */
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

/** The host's action after a deletion ends the caller's membership, when the
 * host supplied one. */
export function useOrganizationAdminOnOrganizationLeft():
  | ((message: string) => void | Promise<unknown>)
  | undefined {
  return useOrganizationAdminContext().onOrganizationLeft
}

export interface OrganizationAdminProviderProps {
  /** The current tenant the host resolved. Required: the package never
   * derives it from the session or the URL and never switches it. */
  readonly tenant: string
  /** The tenant-scoped operations bundle the organization pages run through. */
  readonly operations: OrganizationAdminOperations
  /** The host's synchronous feature answer. Required, as on the console
   * shell: a provider that guessed would render controls the deployment
   * does not serve. */
  readonly hasFeature: (name: FeatureName) => boolean
  /** The assembled session, published so a step-up-gated write can raise it. */
  readonly session?: AuthSession
  /** The organization declarations the host's feature predicate left in force.
   * The wrappers resolve sibling links against them by stable `id`. */
  readonly routes?: readonly AdminRoute[]
  /** The host's in-place navigation callback. Supplying it routes a plain
   * click on an organization link through the host's router. */
  readonly onNavigate?: (href: string) => void
  /** The trail from the organization home to the page. Supplying it mounts the
   * organization breadcrumb provider around the children. */
  readonly breadcrumbs?: readonly PageHeaderBreadcrumb[]
  /**
   * Builds the acceptance link for an issued invitation's token, the same
   * seam the platform console's `AdminProvider` carries. Absent, the
   * invitation page shows the bare token instead.
   */
  readonly invitationLink?: (token: string) => string
  /** Role names the tenant role page renders as read-only. Absent, no row
   * is treated as protected. */
  readonly protectedRoles?: readonly string[]
  /** The host's action after a profile save lands: reload the caller tenant
   * list the account menu and the breadcrumb read. Absent, the profile page
   * only re-reads its own read. */
  readonly onTenantUpdated?: () => void | Promise<unknown>
  /** The host's action after a deletion ends the caller's membership: leave
   * the organization, reload the caller tenant list and show the message on
   * the destination. Absent, the profile page falls back to
   * `onTenantUpdated`. A transfer changes ownership only and never departs. */
  readonly onOrganizationLeft?: (message: string) => void | Promise<unknown>
  readonly children: ReactNode
}

/**
 * The frameless organization surface: installs its contexts around the
 * children and renders nothing else.
 */
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
  // The operations context is declared in the console bundle's type, but
  // the organization bundle is deliberately narrower in the platform members
  // and wider in the tenant ones: the shared pages under this provider
  // call only members the organization bundle carries, and the tenant-mode
  // settings and permissions pages read the organization operations context
  // instead. The cast is the one place the two shapes meet; it is not a
  // promise that a platform page can render here (the package README
  // states it cannot).
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
