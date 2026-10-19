/**
 * NotificationBell: the header bell with its unread-count badge.
 *
 * It reads the caller's unread count through the generated
 * GET /notifications/messages/unread-count operation (keyed under the
 * acting tenant, kept fresh by the live stream and by each read's own
 * staleness) and renders it as an icon-only button. The count is carried
 * on the button's accessible name (the standards' icon-only rule) and the
 * badge glyph is hidden from the accessibility tree, so assistive
 * technology reads "Notifications: 3 unread" once rather than a stray
 * "3".
 *
 * The button is controlled: `onClick` is the host's navigation to the
 * notification surface -- the bell carries no routing itself, so it sits
 * in any shell's header actions. A host may replace the default bell
 * glyph through `icon`.
 */

import type { MouseEvent, ReactNode } from 'react'
import Badge from '@mui/material/Badge'
import IconButton from '@mui/material/IconButton'
import { useNotificationUnreadCount } from './internal/queries.js'
import { useNotificationUiTranslation } from './internal/translation.js'

/** The largest count the badge prints before switching to "99+". */
const BADGE_MAX = 99

export type NotificationBellProps = {
  /** Invoked when the bell is activated; the host navigates to the
   * notification surface. */
  readonly onClick: (event: MouseEvent<HTMLButtonElement>) => void
  /** The glyph shown inside the badge; defaults to the built-in bell. */
  readonly icon?: ReactNode
}

/** The unread-count bell for a shell's header actions. */
export function NotificationBell({ onClick, icon }: NotificationBellProps) {
  const { t } = useNotificationUiTranslation()
  const { data } = useNotificationUnreadCount()
  const count = data ?? 0
  const label =
    count > 0 ? t('bell.labelWithCount', { n: count }) : t('bell.label')
  return (
    <IconButton
      color="inherit"
      aria-label={label}
      onClick={onClick}
    >
      <Badge
        color="error"
        max={BADGE_MAX}
        invisible={count === 0}
        badgeContent={
          <span aria-hidden="true">
            {count > BADGE_MAX ? `${BADGE_MAX}+` : count}
          </span>
        }
      >
        {icon ?? <BellIcon />}
      </Badge>
    </IconButton>
  )
}

/** A hand-drawn bell glyph (decorative). */
function BellIcon() {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={22}
      height={22}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M18 8a6 6 0 1 0-12 0c0 5-2 6-2 6h16s-2-1-2-6z" />
      <path d="M10.3 20a2 2 0 0 0 3.4 0" />
    </svg>
  )
}
