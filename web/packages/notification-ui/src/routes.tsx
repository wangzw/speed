import type { ReactElement } from 'react'
import { useNotificationUiTranslation } from './internal/translation.js'
import { NotificationCenter } from './NotificationCenter.js'
import type { NotificationRoute, NotificationRouteTable } from './route.js'

function NotificationNavLabel({
  labelKey,
}: {
  readonly labelKey: string
}): ReactElement {
  const { t } = useNotificationUiTranslation()
  return <>{t(labelKey)}</>
}

/** The notification surface's pages: the center, at /notifications. */
export const notificationRoutes: NotificationRouteTable = [
  {
    path: '/notifications',
    view: () => <NotificationCenter />,
    label: <NotificationNavLabel labelKey="inbox.title" />,
    requires: ['notification.inbox'],
  },
] satisfies readonly NotificationRoute[]
