/**
 * The header bell with its unread-count badge. The bell is controlled:
 * `onClick` is the host's navigation and the bell carries no routing.
 */

import type { MouseEvent, ReactNode } from 'react'
import Badge from '@mui/material/Badge'
import IconButton from '@mui/material/IconButton'
import { useNotificationUiTranslation } from './internal/translation.js'
import {
  NOTIFICATION_BADGE_MAX,
  useNotificationUnread,
} from './useNotificationUnread.js'

/** The header bell's props: `onClick` is the host's navigation and `icon`
 * replaces the built-in glyph. */
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
  const { badgeText, phrase } = useNotificationUnread()
  const label = phrase ?? t('bell.label')
  return (
    <IconButton
      color="inherit"
      aria-label={label}
      onClick={onClick}
    >
      <Badge
        color="error"
        max={NOTIFICATION_BADGE_MAX}
        // error.light fill with grey.900 ink clears contrast in every preset
        // and both modes.
        sx={{
          '& .MuiBadge-badge': { bgcolor: 'error.light', color: 'grey.900' },
        }}
        badgeContent={
          badgeText === null ? undefined : (
            <span aria-hidden="true">{badgeText}</span>
          )
        }
      >
        {icon ?? <BellIcon />}
      </Badge>
    </IconButton>
  )
}

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
