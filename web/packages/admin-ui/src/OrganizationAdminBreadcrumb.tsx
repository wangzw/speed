/**
 * The organization surface's breadcrumb context. `AdminPageHeader` prefers the
 * published trail over a page's own platform trail; the organization trail
 * starts at the organization name as plain text because the surface has no root.
 */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'
import type { PageHeaderBreadcrumb } from '@speed/ui-kit'

const OrganizationAdminBreadcrumbContext = createContext<
  readonly PageHeaderBreadcrumb[] | undefined
>(undefined)

/** The props of the organization breadcrumb provider. */
export interface OrganizationAdminBreadcrumbProviderProps {
  /** The trail from the organization down to the page. */
  readonly breadcrumbs: readonly PageHeaderBreadcrumb[]
  readonly children: ReactNode
}

/** Publishes the organization trail to the page components below. */
export function OrganizationAdminBreadcrumbProvider({
  breadcrumbs,
  children,
}: OrganizationAdminBreadcrumbProviderProps) {
  return (
    <OrganizationAdminBreadcrumbContext.Provider value={breadcrumbs}>
      {children}
    </OrganizationAdminBreadcrumbContext.Provider>
  )
}

/** The organization trail, or `undefined` when no provider is mounted; never copies it. */
export function useOrganizationAdminBreadcrumbs():
  | readonly PageHeaderBreadcrumb[]
  | undefined {
  return useContext(OrganizationAdminBreadcrumbContext)
}
