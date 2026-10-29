/**
 * The auth callback page: the host route a social or enterprise provider's
 * round trip lands on.
 *
 * A redirect login leaves the page and comes back through the provider, so
 * the host mounts auth-ui's SocialCallbackHandler at its own callback route;
 * the handler reads the flow back on the session and finishes it (a required
 * second factor renders in place). The success callback navigates the viewer
 * home -- or back to the invitation landing the provider detour started from,
 * which the sign-in surface remembered before the document left
 * (src/invitation-return.ts). This page sits outside the tenant shell -- the
 * callback arrives before the session exists -- so the app renders it
 * directly (src/app.tsx).
 *
 * The handler's own content renders in the sign-in surface's card
 * (src/pages/auth-card.tsx): the same brand and page heading, with the step
 * supplying only the card's content. A first social login's
 * account-opening step therefore looks like the sign-in page it came from.
 * The handler's `absent` view -- the tenant frame's 404 -- is the host's
 * page and is rendered bare; its frame supplies that frame's own main
 * landmark.
 *
 * The route is unconditional: it is inert until a deployment enables a
 * redirect or enterprise method (`src/routes.tsx`).
 */

import type { ReactElement, ReactNode } from 'react'
import Box from '@mui/material/Box'
import { SocialCallbackHandler } from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import { useNavigate } from '@speed/route-kit'
import { useAppServices } from '../app-services.js'
import {
  forgetInvitationLanding,
  pendingInvitationLanding,
} from '../invitation-return.js'
import { DEMO_NAMESPACE } from '../resources.js'
import { AuthCard } from './auth-card.js'
import { TENANT_HOME_PATH } from './not-found-page.js'

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
  const { t } = useTranslation(DEMO_NAMESPACE)
  // The landing a provider detour started from is consumed here, once: a
  // viewer who left an invitation link comes back to it, and everybody else
  // -- including a re-entry to this route after the landing was used -- lands
  // home.
  const onSignedIn = (): void => {
    const landing = pendingInvitationLanding()
    if (landing === null) {
      navigate(TENANT_HOME_PATH)
      return
    }
    forgetInvitationLanding()
    navigate(landing)
  }
  // The username step's flow may be gone before the name is answered. The
  // route cannot start a provider flow itself, so the sign-in surface (the
  // anonymous root) is where the viewer starts again.
  const onSignIn = (): void => {
    navigate(TENANT_HOME_PATH)
  }
  return (
    <SocialCallbackHandler
      session={session}
      onSignedIn={onSignedIn}
      onSignIn={onSignIn}
      absent={absent}
      // The sign-in surface's card, brand and page heading included: the
      // account-opening step a first social login parks at renders inside
      // the same card, and the step supplies only the card's content.
      frame={(content) => (
        // No subtitle: on this route the content opens with the account
        // step's own two sentences, and the card's sentence would repeat
        // what they say.
        <AuthPageFrame title={t('signIn.heading')}>{content}</AuthPageFrame>
      )}
    />
  )
}

/** The callback page's own main landmark and the sign-in card around the
 * handler's content. */
function AuthPageFrame({
  title,
  children,
}: {
  readonly title: string
  readonly children: ReactNode
}): ReactElement {
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
      <AuthCard title={title}>
        {children}
      </AuthCard>
    </Box>
  )
}
