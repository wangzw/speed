/**
 * The auth callback page: the host route a social or enterprise provider's
 * round trip lands on.
 *
 * A redirect login leaves the page and comes back through the provider, so
 * the host mounts auth-ui's SocialCallbackHandler at its own callback route;
 * the handler reads the flow back on the session and finishes it (a required
 * second factor renders in place). The success callback navigates the viewer
 * home. This page sits outside the tenant shell -- the callback arrives
 * before the session exists -- so the app renders it directly (src/app.tsx).
 *
 * The route is unconditional: it is inert until a deployment enables a
 * redirect or enterprise method (`src/routes.tsx`).
 */

import type { ReactElement, ReactNode } from 'react'
import Box from '@mui/material/Box'
import { SocialCallbackHandler } from '@speed/auth-ui'
import { useNavigate } from '@speed/route-kit'
import { useAppServices } from '../app-services.js'

export function AuthCallbackPage({
  absent,
}: {
  /** Rendered when the deployment offers no redirect or enterprise method,
   * so nothing ever returns to this route (the host passes the tenant
   * frame's 404). */
  readonly absent?: ReactNode
} = {}): ReactElement {
  const { session } = useAppServices()
  const navigate = useNavigate()
  return (
    <Box
      component="main"
      sx={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        bgcolor: 'background.default',
        p: 2,
      }}
    >
      <Box sx={{ width: '100%', maxWidth: 400 }}>
        <SocialCallbackHandler
          session={session}
          onSignedIn={() => navigate('/')}
          absent={absent}
        />
      </Box>
    </Box>
  )
}
