/**
 * The demo host's notification page.
 *
 * notification-ui's own declaration renders the center with no host wiring:
 * a message that carries a link is only marked read, never followed,
 * because the package does not navigate. The host owns that step, so this
 * page replaces the package's default view with the same center plus an
 * `onOpen` that follows the message's link -- which is how an owner-transfer
 * notification reaches the account-level transfer requests page.
 */

import type { ReactElement } from 'react'
import type { NotificationMessage } from '@speed/api-sdk'
import { NotificationCenter } from '@speed/notification-ui'
import { useNavigate } from '@speed/route-kit'

/** The notification center, following a message's link when it has one. */
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
