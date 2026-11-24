import { useEffect, useState } from 'react'
import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import Card from '@mui/material/Card'
import CircularProgress from '@mui/material/CircularProgress'
import { AUTH_UI_NAMESPACE, PasswordResetScreen } from '@speed/auth-ui'
import type {
  PasswordResetChannel,
  PasswordResetOperations,
} from '@speed/auth-ui'
import type { AuthnMethodList } from '@speed/auth-core'
import { useTranslation } from '@speed/i18n'
import { useNavigate } from '@speed/route-kit'
import {
  authnLoginPasswordResetComplete,
  authnLoginPasswordResetCurrent,
  authnLoginPasswordResetDelete,
  authnLoginPasswordResetStart,
  authnLoginPasswordResetVerify,
} from '../app-api/index.js'
import { useAppServices } from '../app-services.js'

function publishedChannels(
  document: AuthnMethodList,
): readonly PasswordResetChannel[] {
  if (document.password_reset?.enabled !== true) {
    return []
  }
  return document.password_reset.methods.filter(
    (channel): channel is PasswordResetChannel =>
      channel === 'email' || channel === 'sms',
  )
}

const operations: PasswordResetOperations = {
  async requestReset(input) {
    const state = await authnLoginPasswordResetStart({
      channel: input.channel,
      identifier: input.identifier,
      ...(input.humanProof === undefined
        ? {}
        : { human_proof: input.humanProof }),
    })
    return {
      state: 'sent',
      expiresIn: state.expires_in,
      resendAfter: state.resend_after ?? 0,
    }
  },
  async verifyReset(input) {
    const state = await authnLoginPasswordResetVerify({ code: input.code })
    return { state: 'verified', expiresIn: state.expires_in }
  },
  async completeReset(input) {
    await authnLoginPasswordResetComplete({ new_password: input.newPassword })
  },
  async currentReset() {
    const state = await authnLoginPasswordResetCurrent()
    if (state.state === 'verified') {
      return { state: 'verified', expiresIn: state.expires_in }
    }
    return {
      state: 'sent',
      expiresIn: state.expires_in,
      resendAfter: state.resend_after ?? 0,
    }
  },
  async abandonReset() {
    await authnLoginPasswordResetDelete()
  },
}

export function PasswordResetPage(): ReactElement {
  const { session } = useAppServices()
  const { t } = useTranslation(AUTH_UI_NAMESPACE)
  const navigate = useNavigate()
  const [channels, setChannels] = useState<readonly PasswordResetChannel[] | null>(
    null,
  )
  useEffect(() => {
    let cancelled = false
    session.listMethods().then(
      (document) => {
        if (!cancelled) setChannels(publishedChannels(document))
      },
      () => {
        if (!cancelled) setChannels([])
      },
    )
    return () => {
      cancelled = true
    }
  }, [session])

  if (channels === null) {
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
        <CircularProgress aria-label={t('passwordReset.loading')} />
      </Box>
    )
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
      <Card variant="outlined" sx={{ width: '100%', maxWidth: 440, p: 4 }}>
        <PasswordResetScreen
          session={session}
          channels={channels}
          operations={operations}
          onBackToSignIn={() => navigate('/')}
        />
      </Card>
    </Box>
  )
}
