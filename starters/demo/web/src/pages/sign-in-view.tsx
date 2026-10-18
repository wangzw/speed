/**
 * The host's sign-in surface: the chrome the app puts around auth-ui's
 * sign-in family.
 *
 * auth-ui's SignInScreen renders no heading, branding or layout -- those are
 * host content -- so the host frames it in a centered card with the brand and
 * a heading; the channels it offers come from the deployment's discovery
 * document, so the skeleton renders whatever login methods the running
 * deployment enables. The tenant shell's sign-in slot, its console area's
 * session gate, and the invitation-acceptance route all reuse this one
 * surface, so an anonymous viewer meets the same sign-in (and register) entry
 * wherever the app gates them.
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
 * completed on. The register mode decides the landing only. With the
 * personal-tenant feature in force, a registration that went through SMS
 * (the only channel that opens an account here) lands on the tenant area's
 * home -- and so does an ordinary SMS sign-in with a new number, which opens
 * the account implicitly; in both cases the home page decides whether to
 * offer the one-time 「完成账户设置」 notice from the personal-tenant ensure
 * answer and the profile, never from this screen's mode. Without the feature
 * there is no tenant area to land in, so the registration continues on the
 * account setup page to add a username and then a password. Every other
 * outcome leaves the viewer in the app the session flip already opened.
 */

import Box from '@mui/material/Box'
import Paper from '@mui/material/Paper'
import Typography from '@mui/material/Typography'
import { SignInScreen } from '@speed/auth-ui'
import type { SignInContext } from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import type { ReactElement } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAppServices } from '../app-services.js'
import { ACCOUNT_SETUP_PATH, AUTH_CALLBACK_PATH } from '../paths.js'
import { DEMO_NAMESPACE } from '../resources.js'
import { navigateToAuthorizationUrl } from '../sign-in-navigation.js'
import { TENANT_HOME_PATH } from './not-found-page.js'

/** The social / enterprise block this host hands the screen: the provider's
 * round trip comes back to the app's callback route, and the authorization
 * URL replaces the current document. */
const SOCIAL_SIGN_IN = {
  returnTo: AUTH_CALLBACK_PATH,
  onAuthorizeUrl: (_provider: string, authorizeUrl: string): void => {
    navigateToAuthorizationUrl(authorizeUrl)
  },
} as const

export function SignInView(): ReactElement {
  const { session } = useAppServices()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const navigate = useNavigate()
  // A registration completed through SMS: the register mode decides where
  // the just-opened account lands. With the personal tenant in force it
  // lands in the tenant area; the home page decides on its own whether the
  // account still needs the setup notice, from the personal-tenant ensure
  // answer and the profile -- not from this callback, so an implicit
  // opening through an ordinary SMS sign-in gets the same treatment.
  // Without the personal tenant there is no tenant area to land in, so the
  // registration continues on the setup page. Everything else -- an
  // ordinary sign-in, any other channel -- enters the app as the session
  // flip already does.
  //
  // The feature is read from the session snapshot at completion time, not
  // from a render hook: the flow's completion commits the authenticated
  // snapshot and the feature document it read together, and this callback
  // runs before React re-renders, so a render-time value would still be the
  // anonymous one.
  const onSignedIn = (context: SignInContext): void => {
    if (context.mode !== 'register' || context.channel !== 'sms') {
      return
    }
    const features = session.getSnapshot().features
    const personalServed =
      features.state === 'ready' && features.names.has('workspace.personal')
    if (personalServed) {
      navigate(TENANT_HOME_PATH)
      return
    }
    navigate(ACCOUNT_SETUP_PATH)
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
      <Paper elevation={2} sx={{ width: '100%', maxWidth: 400, p: 4 }}>
        <Box sx={{ textAlign: 'center', mb: 3 }}>
          <Typography
            variant="subtitle1"
            color="text.secondary"
            sx={{ fontWeight: 600 }}
          >
            {t('app.title')}
          </Typography>
          <Typography component="h1" variant="h5" sx={{ mt: 0.5 }}>
            {t('signIn.heading')}
          </Typography>
          <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>
            {t('signIn.subtitle')}
          </Typography>
        </Box>
        <SignInScreen
          session={session}
          onSignedIn={onSignedIn}
          social={SOCIAL_SIGN_IN}
        />
      </Paper>
    </Box>
  )
}
