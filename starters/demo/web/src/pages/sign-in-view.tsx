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
 * It reads the session through AppServices, so it is mounted only under the
 * app-services provider (src/app-services.tsx).
 */

import Box from '@mui/material/Box'
import Paper from '@mui/material/Paper'
import Typography from '@mui/material/Typography'
import { SignInScreen } from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import type { ReactElement } from 'react'
import { useAppServices } from '../app-services.js'
import { DEMO_NAMESPACE } from '../resources.js'

export function SignInView(): ReactElement {
  const { session } = useAppServices()
  const { t } = useTranslation(DEMO_NAMESPACE)
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
        <SignInScreen session={session} />
      </Paper>
    </Box>
  )
}
