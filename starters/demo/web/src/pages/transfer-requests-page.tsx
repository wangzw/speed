/**
 * The demo host's ownership transfer requests page.
 *
 * account-ui ships the surface; the host supplies the two things a package
 * cannot know: the app's routes and what "open the organization" means.
 * Each request links to the detail address this module derives from the
 * app's path constant, and the detail view's back link returns to the
 * list. A confirmation makes the caller the organization's owner, so the
 * host records the organization as the current tenant and sends the
 * caller to its profile page -- the first place the new owner can act.
 */

import type { ReactElement } from 'react'
import { OwnerTransferRequestsPage } from '@speed/account-ui'
import { useNavigate } from '@speed/route-kit'
import { setCurrentTenant } from '../chrome.js'
import {
  ORGANIZATION_ADMIN_PROFILE_PATH,
  TRANSFER_REQUESTS_PATH,
  transferRequestPath,
} from '../paths.js'

/** The transfer requests page; a named request renders its detail view. */
export function TransferRequestsPage({
  request,
}: {
  readonly request?: string
}): ReactElement {
  const navigate = useNavigate()
  return (
    <OwnerTransferRequestsPage
      request={request}
      requestHref={transferRequestPath}
      listHref={TRANSFER_REQUESTS_PATH}
      onOpenOrganization={(tenant) => {
        setCurrentTenant(tenant)
        navigate(ORGANIZATION_ADMIN_PROFILE_PATH)
      }}
    />
  )
}
