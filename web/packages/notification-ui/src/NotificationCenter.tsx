/**
 * The notification surface's page: the inbox with the preference matrix
 * beneath it when the host serves that feature.
 */

import Box from '@mui/material/Box'
import Divider from '@mui/material/Divider'
import { NotificationInbox } from './NotificationInbox.js'
import type { NotificationInboxProps } from './NotificationInbox.js'
import { NotificationPreferences } from './NotificationPreferences.js'
import type { NotificationPreferencesProps } from './NotificationPreferences.js'
import { useNotificationPresence } from './NotificationPresence.js'

/** The default composition's props: `inbox` props pass through to the
 * inbox, and `preferences` renders only when presence serves the feature. */
export type NotificationCenterProps = {
  /** Props for the inbox half. */
  readonly inbox?: NotificationInboxProps
  /** Props for the preference half, rendered only when the presence
   * provider says the feature is served. */
  readonly preferences?: NotificationPreferencesProps
}

/** Renders the inbox, and the preference matrix when presence allows. */
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
