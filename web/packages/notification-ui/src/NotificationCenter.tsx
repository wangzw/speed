/**
 * NotificationCenter: the notification surface's page -- the inbox with
 * the preference matrix beneath it when the host serves that feature.
 *
 * The preference matrix is placed here, on the notification surface,
 * rather than in the account settings: notification preferences are the
 * notification capability's own fact (the server routes them under
 * /notifications, not under /api/v1/auth/me), so they live beside the inbox they
 * govern -- one surface for "what you received" and "which channels you
 * want". A host may still mount NotificationPreferences inside its own
 * account page; this page is the default composition.
 *
 * The preference half is gated by NotificationPresenceProvider, fail
 * closed: without a provider (or with `preferences` false) only the inbox
 * renders, and the taxonomy and preference reads are never issued. The
 * optional `inbox` and `preferences` props pass through to the two
 * surfaces, so a host can shape them without composing the page itself.
 */

import Box from '@mui/material/Box'
import Divider from '@mui/material/Divider'
import { NotificationInbox } from './NotificationInbox.js'
import type { NotificationInboxProps } from './NotificationInbox.js'
import { NotificationPreferences } from './NotificationPreferences.js'
import type { NotificationPreferencesProps } from './NotificationPreferences.js'
import { useNotificationPresence } from './NotificationPresence.js'

export type NotificationCenterProps = {
  /** Props for the inbox half. */
  readonly inbox?: NotificationInboxProps
  /** Props for the preference half, rendered only when the presence
   * provider says the feature is served. */
  readonly preferences?: NotificationPreferencesProps
}

export function NotificationCenter({
  inbox,
  preferences,
}: NotificationCenterProps = {}) {
  const presence = useNotificationPresence()
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <NotificationInbox {...inbox} />
      {presence.preferences ? (
        <>
          <Divider />
          <NotificationPreferences {...preferences} />
        </>
      ) : null}
    </Box>
  )
}
