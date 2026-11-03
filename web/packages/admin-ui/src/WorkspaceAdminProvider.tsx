/**
 * WorkspaceAdminProvider: the workspace administration surface's contexts,
 * without a frame of its own.
 *
 * The host has already rendered @speed/product-shell's `AppShell` around
 * the tenant area, so the workspace surface must not render a second one.
 * This provider installs only what the shared page components need: the
 * current tenant, the tenant-scoped operations bundle, the session, the
 * feature answer, the narrowed workspace route table, the in-place
 * navigation callback, the workspace breadcrumb, the invitation-link
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
import { WorkspaceAdminBreadcrumbProvider } from './WorkspaceAdminBreadcrumb.js'
import { WorkspaceAdminOperationsProvider } from './workspace-operations.js'
import type { WorkspaceAdminOperations } from './workspace-operations.js'

/** The workspace facts the wrapper pages read. */
export interface WorkspaceAdminContextValue {
  /** The current tenant the host resolved; every request names it. */
  readonly tenant: string
  /**
   * The role names the host seeded into this tenant and that the tenant
   * role page must render as read-only. Empty when the host names none.
   */
  readonly protectedRoles: readonly string[]
}

const WorkspaceAdminContext = createContext<WorkspaceAdminContextValue | null>(
  null,
)

/**
 * The workspace context. Throws outside the provider: a wrapper page
 * mounted without a tenant would have no request to address, and failing
 * loudly at composition beats issuing a request with an empty path
 * segment.
 */
export function useWorkspaceAdminContext(): WorkspaceAdminContextValue {
  const value = useContext(WorkspaceAdminContext)
  if (value === null) {
    throw new Error(
      'WorkspaceAdminProvider is required to render a workspace admin page',
    )
  }
  return value
}

/** The current tenant the host handed the workspace surface. */
export function useWorkspaceAdminTenant(): string {
  return useWorkspaceAdminContext().tenant
}

/** The role names the tenant role page renders as read-only. */
export function useWorkspaceAdminProtectedRoles(): readonly string[] {
  return useWorkspaceAdminContext().protectedRoles
}

export interface WorkspaceAdminProviderProps {
  /** The current tenant the host resolved. Required: the package never
   * derives it from the session or the URL and never switches it. */
  readonly tenant: string
  /** The tenant-scoped operations bundle the workspace pages run through. */
  readonly operations: WorkspaceAdminOperations
  /** The host's synchronous feature answer. Required, as on the console
   * shell: a provider that guessed would render controls the deployment
   * does not serve. */
  readonly hasFeature: (name: FeatureName) => boolean
  /** The assembled session, published so a step-up-gated write can raise it. */
  readonly session?: AuthSession
  /** The workspace declarations the host's feature predicate left in force.
   * The wrappers resolve sibling links against them by stable `id`. */
  readonly routes?: readonly AdminRoute[]
  /** The host's in-place navigation callback. Supplying it routes a plain
   * click on a workspace link through the host's router. */
  readonly onNavigate?: (href: string) => void
  /** The trail from the workspace home to the page. Supplying it mounts the
   * workspace breadcrumb provider around the children. */
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
  readonly children: ReactNode
}

/**
 * The frameless workspace surface: installs its contexts around the
 * children and renders nothing else.
 */
export function WorkspaceAdminProvider({
  tenant,
  operations,
  hasFeature,
  session,
  routes,
  onNavigate,
  breadcrumbs,
  invitationLink,
  protectedRoles,
  children,
}: WorkspaceAdminProviderProps) {
  // The operations context is declared in the console bundle's type, but
  // the workspace bundle is deliberately narrower in the platform members
  // and wider in the tenant ones: the shared pages under this provider
  // call only members the workspace bundle carries, and the tenant-mode
  // settings and permissions pages read the workspace operations context
  // instead. The cast is the one place the two shapes meet; it is not a
  // promise that a platform page can render here (the package README
  // states it cannot).
  const scopedOperations = operations as unknown as AdminOperations
  const content = (
    <WorkspaceAdminContext.Provider
      value={{ tenant, protectedRoles: protectedRoles ?? [] }}
    >
      <AdminOperationsProvider operations={scopedOperations}>
        <WorkspaceAdminOperationsProvider operations={operations}>
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
        </WorkspaceAdminOperationsProvider>
      </AdminOperationsProvider>
    </WorkspaceAdminContext.Provider>
  )
  return breadcrumbs === undefined ? (
    content
  ) : (
    <WorkspaceAdminBreadcrumbProvider breadcrumbs={breadcrumbs}>
      {content}
    </WorkspaceAdminBreadcrumbProvider>
  )
}
