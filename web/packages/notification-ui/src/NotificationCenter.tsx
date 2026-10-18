/**
 * NotificationCenter: the notification surface's page -- the inbox with
 * the preference matrix beneath it.
 *
 * The preference matrix is placed here, on the notification surface,
 * rather than in the account settings: notification preferences are the
 * notification capability's own fact (the server routes them under
 * /notifications, not under /api/v1/auth/me), so they live beside the inbox they
 * govern -- one surface for "what you received" and "which channels you
 * want". A host may still mount NotificationPreferences inside its own
 * account page; this page is the default composition.
 */

import Box from '@mui/material/Box'
import Divider from '@mui/material/Divider'
import { NotificationInbox } from './NotificationInbox.js'
import { NotificationPreferences } from './NotificationPreferences.js'

export function NotificationCenter() {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <NotificationInbox />
      <Divider />
      <NotificationPreferences />
    </Box>
  )
}
