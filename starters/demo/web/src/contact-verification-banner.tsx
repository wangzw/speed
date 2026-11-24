import Alert from '@mui/material/Alert'
import Link from '@mui/material/Link'
import Stack from '@mui/material/Stack'
import Typography from '@mui/material/Typography'
import { useAuthState } from '@speed/auth-core'
import { useTranslation } from '@speed/i18n'
import { useRouteLinkProps } from '@speed/route-kit'
import type { ReactElement } from 'react'
import { contactVerificationPending } from './contact-verification.js'
import { useAuthnProfileGet } from './app-api/index.js'
import { ACCOUNT_PATH } from './paths.js'
import { DEMO_NAMESPACE } from './resources.js'

const CHANGE_EMAIL_QUERY = 'change=email'

const VERIFY_QUERY = 'verify=1'

export function ContactVerificationBanner(): ReactElement | null {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const authenticated = useAuthState().state === 'authenticated'
  // The profile read is tenant-independent, so it is safe before the tenant
  // settles; only the session gates it.
  const profile = useAuthnProfileGet({
    query: { enabled: authenticated },
  })
  const verifyLink = useRouteLinkProps(`${ACCOUNT_PATH}?${VERIFY_QUERY}`)
  const changeLink = useRouteLinkProps(
    `${ACCOUNT_PATH}?${CHANGE_EMAIL_QUERY}`,
  )

  if (!authenticated || !contactVerificationPending(profile.data ?? {})) {
    return null
  }
  const email = profile.data?.verification?.email ?? ''
  const message =
    email === ''
      ? t('verificationBanner.pendingNoEmail')
      : t('verificationBanner.pending', {
          email,
          interpolation: { escapeValue: false },
        })
  return (
    <Alert
      severity="info"
      role="region"
      aria-label={`${t('verificationBanner.region')}: ${message}`}
      sx={{ borderRadius: 0 }}
    >
      <Stack
        direction={{ xs: 'column', sm: 'row' }}
        spacing={{ xs: 0.5, sm: 2 }}
        sx={{ alignItems: { sm: 'center' }, flexWrap: 'wrap' }}
      >
        <Typography variant="body2">{message}</Typography>
        <Link href={verifyLink.href} onClick={verifyLink.onClick} underline="hover">
          {email === ''
            ? t('verificationBanner.add')
            : t('verificationBanner.verify')}
        </Link>
        {email === '' ? null : (
          <Link
            href={changeLink.href}
            onClick={changeLink.onClick}
            underline="hover"
          >
            {t('verificationBanner.change')}
          </Link>
        )}
      </Stack>
    </Alert>
  )
}
