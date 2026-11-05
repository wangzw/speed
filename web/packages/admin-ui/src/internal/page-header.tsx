/**
 * AdminPageHeader: the package's page header.
 *
 * Every page in this package opens with ui-kit's PageHeader and declares its
 * own console trail (Admin down to the page). Outside the organization
 * surface that trail is the right one. Inside it the trail is wrong -- the
 * page hangs off the organization, not the platform console -- and the
 * organization trail published by `OrganizationAdminBreadcrumbProvider` wins.
 *
 * The choice lives here rather than in each page: a page states only its own
 * trail, and a new page inherits the organization override without knowing
 * the context exists. That is what keeps the two surfaces from leaking into
 * each other as pages are added.
 */

import type { PageHeaderProps } from '@speed/ui-kit'
import { PageHeader } from '@speed/ui-kit'
import { useOrganizationAdminBreadcrumbs } from '../OrganizationAdminBreadcrumb.js'

/** ui-kit's PageHeader, rendering the organization trail when one is published. */
export function AdminPageHeader(props: PageHeaderProps) {
  const organizationBreadcrumbs = useOrganizationAdminBreadcrumbs()
  return (
    <PageHeader
      {...props}
      breadcrumbs={organizationBreadcrumbs ?? props.breadcrumbs}
    />
  )
}
