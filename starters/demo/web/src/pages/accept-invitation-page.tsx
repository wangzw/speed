/**
 * The accept-invitation page: the host route an invitee's link lands on.
 *
 * An administrator issues an invitation from the console, which hands the
 * invitee a link to /invitations/:token. This page is that link's landing.
 * The accept surface is public and the token is the whole credential: the
 * membership lands on the invited address's account, not on whoever happens
 * to be signed in (pkg/authn/invite binds it by the invited address). The
 * host nevertheless gates the landing on a session the way the console area
 * does -- an anonymous viewer meets the host's sign-in / register surface
 * (and re-renders onto the accept screen the moment the session flips), so
 * the invitee arrives with an identity of their own -- but that gate is a
 * composition choice, not what authorizes or binds the acceptance. A
 * signed-in viewer meets auth-ui's AcceptInvitationScreen, whose two
 * invitation operations this page binds to the app's generated SDK surface
 * (src/app-api, the platform document's operations). On acceptance the page
 * navigates home.
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
import { TENANT_HOME_PATH } from './not-found-page.js'
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

  // The host renders its sign-in / register surface ahead of the accept
  // screen so the invitee arrives with a session of their own; the accept
  // surface itself is public and the token is the whole credential -- the
  // membership binds to the invited address, not to this session. An
  // anonymous viewer meets the sign-in surface, and the session flip
  // re-renders this page onto the accept screen.
  if (snapshot.state !== 'authenticated') {
    // The URL carries the invitation token: a registration completed here
    // must stay on it, so the session flip renders this page's accept
    // screen rather than the app navigating to the register mode's landing.
    return <SignInView keepLanding />
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
          onAccepted={() => navigate(TENANT_HOME_PATH)}
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
