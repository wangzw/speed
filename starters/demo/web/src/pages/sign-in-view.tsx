/**
 * The host's sign-in surface: the chrome the app puts around auth-ui's
 * sign-in family.
 *
 * auth-ui's SignInScreen renders no branding or page heading -- those are
 * host content -- so the host frames it in a centered card with the brand and
 * a heading; the only title the screen renders is its own register-mode
 * block. The page heading and subtitle follow the mode through
 * `onModeChange`: giving it makes the screen's `mode` the single source, so
 * the host's title says what the surface is asking for. The channels it
 * offers come from the deployment's discovery document, so the skeleton
 * renders whatever login methods the running deployment enables. The tenant
 * shell's sign-in slot, its admin area's session gate, and the
 * invitation-acceptance route all reuse this one surface, so an anonymous
 * viewer meets the same sign-in (and register) entry wherever the app gates
 * them.
 *
 * A redirect method's flow answers an authorization URL; the host navigates
 * the current window to it, and the provider's round trip lands on this app's
 * callback route (AUTH_CALLBACK_PATH), which is sent as the flow's
 * `return_to`.
 *
 * It reads the session through AppServices, so it is mounted only under the
 * app-services provider (src/app-services.tsx).
 *
 * Registration folds into the sign-in flow, so a completed registration is
 * not a page of its own: the screen reports the mode and channel it
 * completed on. The register mode decides the landing only, and only on the
 * application's own sign-in page. With the personal-tenant feature in force,
 * a registration that went through SMS (the only channel that opens an
 * account here) lands on the tenant area's home -- and so does an ordinary
 * SMS sign-in with a new number, which opens the account implicitly. Without
 * the feature there is no tenant area to land in, so the registration lands
 * on the account page with the account it just opened. Every other outcome
 * leaves the viewer in the app the session flip already opened.
 *
 * An invitation link is the exception: the landing page opens this surface in
 * place from its own sign-in and create-account actions, so a registration or
 * sign-in made there ends on that link's accept page, not on the register
 * mode's landing. The URL does not change: the session flip re-renders the
 * landing as its signed-in surface. A password registration still takes the
 * verification detour and keeps the landing in the tab's session storage
 * (src/invitation-return.ts). A redirect or enterprise provider replaces the
 * document for its round trip, so the landing is remembered before the
 * provider takes over and the callback route sends the viewer back to it.
 *
 * A surface that embeds this view inside a flow of its own passes
 * `keepLanding`: the invitation landing and the admin area's session gate
 * sign the viewer in place, so a registration completed there leaves the
 * invitation token in the URL or the admin path the viewer asked for. The
 * session flip itself re-renders those surfaces; navigating away would lose
 * what the URL carried. The password channel's verification detour is not
 * that landing: it happens wherever the registration was hosted, because the
 * opened account cannot use the tenant area before its address is proved.
 */

import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import { SignInScreen } from '@speed/auth-ui'
import type {
  SignInContext,
  SignInMode,
  SocialSignInOptions,
} from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import { useLocationPath, useNavigate } from '@speed/route-kit'
import { useMemo, useState } from 'react'
import type { ReactElement } from 'react'
import { useAppServices } from '../app-services.js'
import { rememberInvitationLanding } from '../invitation-return.js'
import {
  ACCOUNT_PATH,
  AUTH_CALLBACK_PATH,
  PASSWORD_RESET_PATH,
} from '../paths.js'
import { DEMO_NAMESPACE } from '../resources.js'
import { navigateToAuthorizationUrl } from '../sign-in-navigation.js'
import { useCallbackLanding } from '../use-callback-landing.js'
import { AuthCard } from './auth-card.js'
import { TENANT_HOME_PATH } from './not-found-page.js'

/** The social / enterprise block this host hands the screen: the provider's
 * round trip comes back to the app's callback route, and the authorization
 * URL replaces the current document. The document it leaves is gone for the
 * round trip, so a viewer who starts from an invitation link has to carry
 * that landing across it: the landing is remembered before the provider takes
 * over (the token stays out of the URL) and the callback route sends the
 * viewer back to it. Starting the flow anywhere else records no landing, so
 * an earlier one cannot outlive the link it came from. */
function socialSignIn(pathname: string): SocialSignInOptions {
  return {
    returnTo: AUTH_CALLBACK_PATH,
    onAuthorizeUrl: (_provider: string, authorizeUrl: string): void => {
      rememberInvitationLanding(pathname)
      navigateToAuthorizationUrl(authorizeUrl)
    },
  }
}

export function SignInView({
  keepLanding = false,
  initialMode = 'sign-in',
  onBack,
  onSignedIn: onSignedInCallback,
}: {
  /**
   * Whether the surface embedding this view owns the landing: true leaves
   * the viewer at the current URL after a registration, so the embedding
   * page (the invitation landing, the admin session gate) re-renders onto
   * its own content instead of the app navigating away. The application's
   * own sign-in page leaves it false and lands by the register mode.
   */
  readonly keepLanding?: boolean
  /** The mode the surface starts in; the viewer can still switch. */
  readonly initialMode?: SignInMode
  /**
   * Reports the way back to the embedding surface. Rendered above the
   * channels; absent, the surface offers none.
   */
  readonly onBack?: () => void
  /**
   * Reports a settled sign-in or registration after this view's own landing
   * decision. The invitation landing uses it to close the sign-in surface it
   * opened in place.
   */
  readonly onSignedIn?: () => void
} = {}): ReactElement {
  const { session } = useAppServices()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const navigate = useNavigate()
  const { path: pathname } = useLocationPath()
  const social = useMemo(() => socialSignIn(pathname), [pathname])
  // The mode the screen presents. Giving `onModeChange` makes the screen
  // controlled, so this state is the single source of what the surface asks
  // for; the page title above the screen follows it.
  const [mode, setMode] = useState<SignInMode>(initialMode)
  // A refused social callback lands here with its code in the URL fragment,
  // and, when the refusal was link-required, the masked address whose
  // account collided. The hook reads it on the first render -- the read
  // itself changes nothing -- and clears the fragment after mount, so the
  // URL rewrite stays out of render and a reload does not show the message
  // again.
  const { landing, dismiss } = useCallbackLanding()
  const initialError = landing?.code
  const initialErrorEmail = landing?.email
  // A registration completed through SMS: the register mode decides where
  // the just-opened account lands. With the personal tenant in force it
  // lands in the tenant area; without one there is no tenant area to land
  // in, so it lands on the account settings page, which continues on its
  // profile pane (a first password is set from the security pane).
  // Everything else -- an ordinary sign-in, any other channel -- enters the
  // app as the session flip already does.
  //
  // The feature is read from the session snapshot at completion time, not
  // from a render hook: the flow's completion commits the authenticated
  // snapshot and the feature document it read together, and this callback
  // runs before React re-renders, so a render-time value would still be the
  // anonymous one.
  const onSignedIn = (context: SignInContext): void => {
    // The host's own observer runs whatever this view decides about the
    // landing: an embedding page closes the surface it opened in place.
    onSignedInCallback?.()
    if (context.mode !== 'register') {
      return
    }
    if (context.channel === 'password') {
      // A password registration lands on the account page's verification
      // section whatever the deployment's personal-tenant policy and whatever
      // surface hosted it: the account exists but its email address is not
      // proved yet, and that section is where it is proved. A visitor who
      // registered from an invitation link keeps that landing for the way
      // back: the token stays out of the URL, every request and every
      // server-side record, held in the tab instead.
      rememberInvitationLanding(pathname)
      navigate(`${ACCOUNT_PATH}?verify=1`)
      return
    }
    if (keepLanding) {
      // The embedding page's URL is the destination: the session flip
      // re-renders it (the invitation landing onto its accept screen, the
      // admin gate onto the admin) and no navigation may take it away.
      return
    }
    if (context.channel !== 'sms') {
      return
    }
    const features = session.getSnapshot().features
    const personalServed =
      features.state === 'ready' && features.names.has('org.personal')
    if (personalServed) {
      navigate(TENANT_HOME_PATH)
      return
    }
    navigate(ACCOUNT_PATH)
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
      <AuthCard
        title={
          mode === 'register' ? t('signIn.registerHeading') : t('signIn.heading')
        }
        subtitle={
          mode === 'register'
            ? t('signIn.registerSubtitle')
            : t('signIn.subtitle')
        }
      >
        <SignInScreen
          session={session}
          mode={mode}
          onModeChange={setMode}
          onSignedIn={onSignedIn}
          social={social}
          slots={
            onBack === undefined
              ? undefined
              : {
                  aboveChannels: (
                    <Button type="button" onClick={onBack}>
                      {t('signIn.back')}
                    </Button>
                  ),
                }
          }
          initialError={initialError}
          initialErrorEmail={initialErrorEmail}
          // The screen clears the landing refusal when the viewer starts
          // something else; dropping it here keeps a re-render from handing
          // the same message back.
          onInitialErrorDismiss={dismiss}
          // The password channel's "forgot password" entry navigates to the
          // anonymous reset page. A surface that owns its own landing (an
          // invitation link, the admin gate) leaves it out: navigating away
          // would drop the token or the path the viewer arrived with.
          onForgotPassword={
            keepLanding ? undefined : () => navigate(PASSWORD_RESET_PATH)
          }
        />
      </AuthCard>
    </Box>
  )
}
