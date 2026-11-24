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
  readonly keepLanding?: boolean
  readonly initialMode?: SignInMode
  readonly onBack?: () => void
  readonly onSignedIn?: () => void
} = {}): ReactElement {
  const { session } = useAppServices()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const navigate = useNavigate()
  const { path: pathname } = useLocationPath()
  const social = useMemo(() => socialSignIn(pathname), [pathname])
  const [mode, setMode] = useState<SignInMode>(initialMode)
  const { landing, dismiss } = useCallbackLanding()
  const initialError = landing?.code
  const initialErrorEmail = landing?.email
  const onSignedIn = (context: SignInContext): void => {
    onSignedInCallback?.()
    if (context.mode !== 'register') {
      return
    }
    if (context.channel === 'password') {
      rememberInvitationLanding(pathname)
      navigate(`${ACCOUNT_PATH}?verify=1`)
      return
    }
    if (keepLanding) {
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
          onInitialErrorDismiss={dismiss}
          onForgotPassword={
            keepLanding ? undefined : () => navigate(PASSWORD_RESET_PATH)
          }
        />
      </AuthCard>
    </Box>
  )
}
