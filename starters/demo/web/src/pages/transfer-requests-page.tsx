import type { ReactElement } from 'react'
import { OwnerTransferRequestsPage } from '@speed/account-ui'
import { useNavigate } from '@speed/route-kit'
import { setCurrentTenant } from '../chrome.js'
import {
  ORGANIZATION_ADMIN_PROFILE_PATH,
  TRANSFER_REQUESTS_PATH,
  transferRequestPath,
} from '../paths.js'

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
