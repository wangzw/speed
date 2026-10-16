/**
 * The accept-invitation page: the host route an invitee's link lands on.
 *
 * An administrator issues an invitation from the console, which hands the
 * invitee a link to /invitations/:token. This page is that link's landing.
 * The invitee accepts as themselves, so the page gates on the session the
 * way the console area does: a viewer who is not signed in meets the host's
 * sign-in / register surface (and re-renders onto the accept screen the
 * moment the session flips); a signed-in viewer meets auth-ui's
 * AcceptInvitationScreen, whose two invitation operations this page binds to
 * the app's generated SDK surface (src/app-api, the platform document's
 * operations). On acceptance the page navigates home.
 *
 * The route is public and sits outside both shells (the invitee arrives
 * before any session exists), so the app renders its page directly
 * (src/app.tsx) rather than through the shells' content area.
 */

import { useMemo } from 'react'
import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import Paper from '@mui/material/Paper'
import { useAuthState } from '@speed/auth-core'
import { AcceptInvitationScreen } from '@speed/auth-ui'
import type { InvitationOperations } from '@speed/auth-ui'
import { useNavigate } from '@speed/route-kit'
import { authnInviteAccept, authnInviteAcceptView } from '../app-api/index.js'
import type { AuthnInviteView } from '../app-api/index.js'
import { SignInView } from './sign-in-view.js'

export interface AcceptInvitationPageProps {
  /** The invitation token from the matched route. */
  readonly token: string
}

export function AcceptInvitationPage({
  token,
}: AcceptInvitationPageProps): ReactElement {
  const snapshot = useAuthState()
  const navigate = useNavigate()

  // The screen drives these two operations; they reach the backend through
  // the generated surface over the request function the assembly bound
  // (src/main.tsx). The object is stable across renders -- the screen reads
  // it in an effect -- so it is memoized once.
  const invitations = useMemo<InvitationOperations>(
    () => ({
      readInvitation: (value) => authnInviteAcceptView(value).then(toSummary),
      acceptInvitation: (value, input) =>
        authnInviteAccept(value, {
          ...(input.displayName === undefined
            ? {}
            : { display_name: input.displayName }),
          ...(input.password === undefined ? {} : { password: input.password }),
        }).then(() => undefined),
    }),
    [],
  )

  // The invitee accepts as themselves: an anonymous viewer meets the host's
  // sign-in / register surface first, and the session flip re-renders this
  // page onto the accept screen.
  if (snapshot.state !== 'authenticated') {
    return <SignInView />
  }

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
      <Paper elevation={2} sx={{ width: '100%', maxWidth: 480, p: 4 }}>
        <AcceptInvitationScreen
          token={token}
          invitations={invitations}
          onAccepted={() => navigate('/')}
        />
      </Paper>
    </Box>
  )
}

/** Narrows the generated acceptView answer to the screen's own summary. */
function toSummary(view: AuthnInviteView) {
  return {
    tenant: view.invitation.tenant,
    email: view.invitation.email,
    status: view.invitation.status,
    expiresAt: view.invitation.expires_at,
  }
}
