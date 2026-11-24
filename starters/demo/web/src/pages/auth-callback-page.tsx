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
  readonly absent?: ReactNode
} = {}): ReactElement {
  const { session } = useAppServices()
  const navigate = useNavigate()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const onSignedIn = (): void => {
    const landing = pendingInvitationLanding()
    if (landing === null) {
      navigate(TENANT_HOME_PATH)
      return
    }
    forgetInvitationLanding()
    navigate(landing)
  }
  const onSignIn = (): void => {
    navigate(TENANT_HOME_PATH)
  }
  return (
    <SocialCallbackHandler
      session={session}
      onSignedIn={onSignedIn}
      onSignIn={onSignIn}
      absent={absent}
      frame={(content) => (
        <AuthPageFrame title={t('signIn.heading')}>{content}</AuthPageFrame>
      )}
    />
  )
}

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
