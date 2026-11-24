/**
 * The caller's unread count, shaped for any control that carries it: the
 * count, the badge text, and the accessible-name phrase.
 */

import { useNotificationUnreadCount } from './internal/queries.js'
import { useNotificationUiTranslation } from './internal/translation.js'

/** The largest count the badge prints before switching to "99+". */
export const NOTIFICATION_BADGE_MAX = 99

/** The unread count, badge text and accessible-name phrase. */
export interface NotificationUnread {
  /** The unread count; 0 until the read settles. */
  readonly count: number
  /** The text the badge prints ("3", "99+"), or null when nothing is unread. */
  readonly badgeText: string | null
  /** The phrase a control carrying the badge adds to its accessible name, or
   * null when nothing is unread. */
  readonly phrase: string | null
}

/** Returns the caller's unread count shaped for a badge-carrying control.
 * `enabled` false withholds the read. */
export function useNotificationUnread(enabled = true): NotificationUnread {
  const { t } = useNotificationUiTranslation()
  const { data } = useNotificationUnreadCount(enabled)
  const count = data ?? 0
  const badgeText =
    count === 0
      ? null
      : count > NOTIFICATION_BADGE_MAX
        ? `${NOTIFICATION_BADGE_MAX}+`
        : String(count)
  return {
    count,
    badgeText,
    phrase:
      badgeText === null ? null : t('bell.labelWithCount', { n: badgeText }),
  }
}
