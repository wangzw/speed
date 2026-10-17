/**
 * The notification surface's page declaration -- its contribution to the
 * declarative registration seam.
 *
 * One page: the notification center (the inbox with the preference matrix
 * beneath it) at '/notifications', labelled so it contributes a
 * navigation entry. The declarations are collected by the application's
 * composition layer and handed to the route layer it chose; this package
 * imports no router and does no path matching.
 *
 * The label is a React element (NotificationNavLabel) that resolves its
 * text through the notification-ui namespace when it renders, so the
 * declaration stays a plain module-level value while its navigation label
 * still translates reactively with the host's language.
 */

import type { ReactElement } from 'react'
import { useNotificationUiTranslation } from './internal/translation.js'
import { NotificationCenter } from './NotificationCenter.js'
import type { NotificationRoute, NotificationRouteTable } from './route.js'

/** A navigation label bound to a notification-ui namespace key, resolved
 * when the label renders. */
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
