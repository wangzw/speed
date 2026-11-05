/**
 * useNotificationUnread: the caller's unread count, shaped for any control
 * that carries it -- the header bell (NotificationBell) and a host's own
 * avatar badge alike.
 *
 * The count comes from GET /notifications/messages/unread-count, keyed under
 * the acting tenant and kept fresh by the live stream. `enabled` withholds
 * the read where a host offers no notification surface at all.
 *
 * The hook returns three pieces because a badge has three jobs. `count` is
 * the number itself. `badgeText` is what the badge prints -- the count, or
 * "99+" above the cap -- and null when nothing is unread, so a host renders
 * no badge at all: a hidden "0" would still be visible text the control's
 * accessible name does not carry (WCAG 2.5.3). `phrase` is that same
 * information as a phrase for the control's accessible name; a visible badge
 * whose text is not inside the name fails the same rule.
 */

import { useNotificationUnreadCount } from './internal/queries.js'
import { useNotificationUiTranslation } from './internal/translation.js'

/** The largest count the badge prints before switching to "99+". */
export const NOTIFICATION_BADGE_MAX = 99

export interface NotificationUnread {
  /** The unread count; 0 until the read settles. */
  readonly count: number
  /** The text the badge prints ("3", "99+"), or null when nothing is unread. */
  readonly badgeText: string | null
  /** The phrase a control carrying the badge adds to its accessible name, or
   * null when nothing is unread. */
  readonly phrase: string | null
}

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
