import type { ReactElement } from 'react'
import type { NotificationMessage } from '@speed/api-sdk'
import { NotificationCenter } from '@speed/notification-ui'
import { useNavigate } from '@speed/route-kit'

export function NotificationsPage(): ReactElement {
  const navigate = useNavigate()
  return (
    <NotificationCenter
      inbox={{
        onOpen: (message: NotificationMessage) => {
          if (message.link !== '') {
            navigate(message.link)
          }
        },
      }}
    />
  )
}
