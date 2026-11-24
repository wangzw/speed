/** AdminPageHeader: the package's page header, preferring the organization breadcrumb trail. */

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
