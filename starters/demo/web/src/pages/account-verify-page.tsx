/**
 * The account verification page: where a password registration lands after
 * its automatic sign-in.
 *
 * The page is the host's frame around account-ui's
 * ContactVerificationSection: the page heading belongs to the host, the
 * section reads GET /api/v1/auth/me's `verification` and drives the send and
 * verify writes (POST /api/v1/auth/identifiers and /identifiers/verify). It
 * is a completion point rather than a navigation destination, so the route
 * carries no navigation entry; the persistent banner below the app bar and
 * the registration landing both point at it.
 *
 * The URL's `change=email` flag says the visitor arrived through the banner's
 * change action rather than its verify action: the section then starts in the
 * change-address form. The flag is part of the section's key, so following the
 * change link while already on this page re-initializes the section instead of
 * leaving the previous entry state in place.
 */

import Box from '@mui/material/Box'
import { ContactVerificationSection } from '@speed/account-ui'
import { useTranslation } from '@speed/i18n'
import { useLocationPath } from '@speed/route-kit'
import { PageHeader } from '@speed/ui-kit'
import type { ReactElement } from 'react'
import { useAppServices } from '../app-services.js'
import { DEMO_NAMESPACE } from '../resources.js'

export function AccountVerifyPage(): ReactElement {
  const { session } = useAppServices()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const { search } = useLocationPath()
  const changeOpen = new URLSearchParams(search).get('change') === 'email'
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 4, maxWidth: 640 }}>
      <PageHeader
        title={t('accountVerify.title')}
        description={t('accountVerify.description')}
      />
      <ContactVerificationSection
        key={changeOpen ? 'change-email' : 'verify'}
        session={session}
        initialChangeOpen={changeOpen}
      />
    </Box>
  )
}
