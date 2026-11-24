import type { ReactElement } from 'react'
import {
  OrganizationCreatePage,
  type CreatedOrganization,
  type OrganizationCreateSubmission,
} from '@speed/org-create-ui'
import { useNavigate } from '@speed/route-kit'
import { orgCreateCreate } from '../app-api/index.js'
import { setCurrentTenant, setTenantName } from '../chrome.js'
import { ORGANIZATION_ADMIN_PROFILE_PATH } from '../paths.js'
import { loadSelfTenants } from '../tenant-self.js'

export function OrganizationCreateRoute(): ReactElement {
  const navigate = useNavigate()
  return (
    <OrganizationCreatePage
      createOrganization={async (
        submission: OrganizationCreateSubmission,
      ): Promise<CreatedOrganization> => {
        const answer = await orgCreateCreate({
          name: submission.name,
          display_name: submission.displayName,
          ...(submission.description === ''
            ? {}
            : { description: submission.description }),
        })
        const tenant = answer.tenant
        return {
          name: tenant.name,
          displayName: tenant.display_name,
          description: tenant.description,
        }
      }}
      onCreated={async (created: CreatedOrganization) => {
        setTenantName(created.name, 'business')
        setCurrentTenant(created.name)
        const state = await loadSelfTenants()
        if (state.failed) {
          throw { code: 'client.network' }
        }
      }}
      successHref={ORGANIZATION_ADMIN_PROFILE_PATH}
      onNavigate={navigate}
    />
  )
}
