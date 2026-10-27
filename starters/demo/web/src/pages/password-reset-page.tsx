/**
 * The anonymous password reset page.
 *
 * auth-ui's PasswordResetScreen is the surface; this page is the host half the
 * screen leaves open: it reads the deployment's published reset channels from
 * the same discovery document the sign-in surface reads, binds the reset
 * operations to the app's generated API surface, and names the way back to
 * sign-in. The screen uses the session only for the human-verification gate --
 * an anonymous visitor has none -- so the page must sit outside both shells
 * (src/app.tsx), where no tenant context or authenticated frame is needed.
 *
 * The channels are a deployment fact: the discovery document's
 * `password_reset.methods`, published only while the switch is open, the audit
 * is present and the channel is assembled. An absent or closed member reads as
 * no channel, and the screen then renders its unavailable notice rather than
 * offering a send the server would refuse. The page waits for that read before
 * it mounts the screen, because the screen captures the resume operation once
 * at mount and cannot start a resume after a later channel list arrives.
 *
 * A password-policy refusal stays on the proved surface (the screen keeps the
 * record verified), so the page exposes no retry of its own. A settled reset
 * shows the screen's done state; leaving it through "Back to sign in" returns
 * the viewer to the anonymous sign-in surface at the deployment root.
 */

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

/** The reset channels this client renders, in the deployment's order. */
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

/**
 * The reset operations over the app's generated API surface. Each maps the
 * generated wire shape onto the screen's own: the state names, the deadlines
 * in whole seconds, and the snake_case request bodies. `currentReset` lets a
 * reload resume the record the reset cookie names; `abandonReset` is the
 * screen's best-effort cleanup on the way back to sign-in.
 */
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

/** The anonymous reset page: the deployment's channels in a centered card. */
export function PasswordResetPage(): ReactElement {
  const { session } = useAppServices()
  const { t } = useTranslation(AUTH_UI_NAMESPACE)
  const navigate = useNavigate()
  // null means the discovery read has not settled. An empty list is a settled
  // answer: the deployment offers no channel, and the screen says so.
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
        // A discovery read that fails is not a published channel: the surface
        // renders its unavailable notice rather than a send it cannot make.
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
