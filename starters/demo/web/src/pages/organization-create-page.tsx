/**
 * The self-service organization-creation page, assembled for the demo
 * host.
 *
 * The shared page (@speed/org-create-ui) owns the form, its validation and
 * the create-then-enter sequence; this wrapper supplies what only the host
 * knows: the generated org.create operation, the tenant switch that makes
 * the new organization current (record its name, settle it, re-read the
 * caller's tenants) and the destination after a successful hand-over.
 *
 * The hand-over deliberately does not navigate: the page waits for it,
 * shows the entering state, and only then moves to the organization
 * profile. A failed caller-tenants re-read is reported as a failed
 * hand-over so the page offers a retry of the switch, never a second
 * creation.
 */

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
        // Record the name and kind before the switch: the request surface
        // announces the name, and the account menu labels the row by the
        // kind an operation's answer recorded.
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
