/**
 * The persistent email-verification notice under the app bar.
 *
 * An account with an email address but no proved one reads `pending` on
 * GET /api/v1/auth/me's `verification`, however the address arrived
 * (self-registration, administrator provisioning or an import). While that
 * holds, the tenant frame shows this notice below the app bar: the target
 * address, a link to the verification page and, when there is a target, a
 * link to change it. The two actions land on the same page with different
 * intent: the verify link opens the code step, the change link carries
 * `?change=email` so the page starts in the change-address form. The notice
 * is a region, not a live region, and it carries no close control: it stays
 * until the address is proved, independent of the once-only "finish account
 * setup" notice (password setup can be complete while email verification is
 * still pending).
 *
 * The read is the same profile query the account surfaces make, so a
 * successful verification (which invalidates it) also clears this notice.
 *
 * Copyright-free copy: every string is the demo namespace's.
 */

import Alert from '@mui/material/Alert'
import Link from '@mui/material/Link'
import Stack from '@mui/material/Stack'
import Typography from '@mui/material/Typography'
import { useAuthState } from '@speed/auth-core'
import { useTranslation } from '@speed/i18n'
import { useRouteLinkProps } from '@speed/route-kit'
import type { ReactElement } from 'react'
import { contactVerificationPending } from './account-setup.js'
import { useAuthnProfileGet } from './app-api/index.js'
import { ACCOUNT_VERIFY_PATH } from './paths.js'
import { DEMO_NAMESPACE } from './resources.js'

/** The change-address entry's query flag: the verification page reads it and
 * starts the section in the change form. */
const CHANGE_EMAIL_QUERY = 'change=email'

/** The persistent pending-verification notice, or null when there is none. */
export function ContactVerificationBanner(): ReactElement | null {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const authenticated = useAuthState().state === 'authenticated'
  // The profile is tenant-independent, so the read is safe before the tenant
  // context settles; it is held back while anonymous (there is no account to
  // read, and the route would answer 401).
  const profile = useAuthnProfileGet({
    query: { enabled: authenticated },
  })
  const verifyLink = useRouteLinkProps(ACCOUNT_VERIFY_PATH)
  const changeLink = useRouteLinkProps(
    `${ACCOUNT_VERIFY_PATH}?${CHANGE_EMAIL_QUERY}`,
  )

  if (!authenticated || !contactVerificationPending(profile.data ?? {})) {
    return null
  }
  const email = profile.data?.verification?.email ?? ''
  return (
    <Alert
      severity="info"
      role="region"
      aria-label={t('verificationBanner.region')}
      sx={{ borderRadius: 0 }}
    >
      <Stack
        direction={{ xs: 'column', sm: 'row' }}
        spacing={{ xs: 0.5, sm: 2 }}
        sx={{ alignItems: { sm: 'center' }, flexWrap: 'wrap' }}
      >
        <Typography variant="body2">
          {email === ''
            ? t('verificationBanner.pendingNoEmail')
            : t('verificationBanner.pending', {
                email,
                interpolation: { escapeValue: false },
              })}
        </Typography>
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
