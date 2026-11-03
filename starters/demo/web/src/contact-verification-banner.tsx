/**
 * The persistent email-verification notice under the app bar.
 *
 * An account with an email address but no proved one reads `pending` on
 * GET /api/v1/auth/me's `verification`, however the address arrived
 * (self-registration, administrator provisioning or an import). While that
 * holds, the tenant frame shows this notice below the app bar: the target
 * address, a link to the account page's verification section and, when there
 * is a target, a link to change it. The two actions land on the same page
 * with different intent: the verify link carries `?verify=1` so the section
 * sends one code on arrival, the change link carries `?change=email` so the
 * section starts in the change-address form. The notice
 * is a region, not a live region, and it carries no close control: it stays
 * until the address is proved.
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
import { contactVerificationPending } from './contact-verification.js'
import { useAuthnProfileGet } from './app-api/index.js'
import { ACCOUNT_PATH } from './paths.js'
import { DEMO_NAMESPACE } from './resources.js'

/** The change-address entry's query flag: the account page reads it and
 * starts the verification section in the change form. */
const CHANGE_EMAIL_QUERY = 'change=email'

/** The verify entry's query flag: the account page sends one code on arrival
 * and then waits for the visitor. */
const VERIFY_QUERY = 'verify=1'

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
      // The region is named by its visible message as well as its purpose, so
      // the accessible name contains the text a sighted reader sees.
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
