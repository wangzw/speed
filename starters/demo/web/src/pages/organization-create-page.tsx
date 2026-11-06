/**
 * The self-service organization-creation page, assembled for the demo
 * host.
 *
 * The shared page (@speed/org-create-ui) owns the form, its validation and
 * the create-then-enter sequence; this wrapper supplies what only the host
 * knows: the generated org.create operation, the tenant switch that makes
 * the new organization current (record its alias, settle it, re-read the
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
import { setCurrentTenantId, setTenantAlias } from '../chrome.js'
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
          alias: submission.alias,
          display_name: submission.displayName,
          ...(submission.description === ''
            ? {}
            : { description: submission.description }),
        })
        const tenant = answer.tenant
        return {
          id: tenant.id,
          alias: tenant.alias,
          displayName: tenant.displayName,
          description: tenant.description,
        }
      }}
      onCreated={async (created: CreatedOrganization) => {
        // Record the alias and kind before the switch: the request surface
        // announces the alias, and the account menu labels the row by the
        // kind an operation's answer recorded.
        setTenantAlias(created.id, created.alias, 'business')
        setCurrentTenantId(created.id)
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
