/**
 * The account setup page: where a registration completed through the
 * sign-in surface finishes.
 *
 * The ruling is that a username-and-password registration is three existing
 * capabilities in a row (ADR-authn-signup): the SMS first login
 * opens the account with a verified phone, POST /api/v1/auth/identifiers adds a
 * username, and PUT /api/v1/auth/password sets a password. This page is the host
 * half of the second and third steps -- account-ui's IdentifiersSection
 * (username only; the email add face lives on the account page) and its
 * PasswordSection under a page header.
 *
 * The account's own profile (GET /api/v1/auth/me, the same query the sections read)
 * decides the state. A login name is a username or an email address: the
 * password route refuses an account without one with 409
 * login_identifier_required, so `PasswordSection` is given
 * `withoutLoginName="explain"`: until the profile lists one it renders its
 * own heading and the explanation that a username is needed first, instead
 * of the form. Once the account has both a login name and a password the
 * page states setup is complete, naming the sign-in name the account holds:
 * a username when it has one, an email address when that is the only
 * login name. The profile query's cache entry is the one the sections
 * invalidate after each write, so adding a username reveals the password
 * form and setting a password flips the page to complete without a reload.
 *
 * The page offers a way home and never revokes anything: skipping leaves the
 * account valid, and the holder can sign in by SMS and come back -- signing
 * in again with the same phone in register mode is an ordinary sign-in, and
 * this page then reflects the account as it now stands.
 *
 * A profile read that is still in flight or failed states nothing about the
 * account: the identifiers section renders its own loading or error surface,
 * and the password area waits for an answer it can act on.
 */

import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Link from '@mui/material/Link'
import { IdentifiersSection, PasswordSection } from '@speed/account-ui'
import { useTranslation } from '@speed/i18n'
import { useRouteLinkProps } from '@speed/route-kit'
import { PageHeader } from '@speed/ui-kit'
import type { ReactElement } from 'react'
import { isLoginName } from '../account-setup.js'
import { useAuthnProfileGet } from '../app-api/index.js'
import { useAppServices } from '../app-services.js'
import { TENANT_HOME_PATH } from './not-found-page.js'
import { DEMO_NAMESPACE } from '../resources.js'

export function AccountSetupPage(): ReactElement {
  const { session } = useAppServices()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const homeLink = useRouteLinkProps(TENANT_HOME_PATH)
  // The profile is the same read the two sections below make: one cache
  // entry, one request, and each write they perform invalidates it.
  const profile = useAuthnProfileGet()
  const identifiers = profile.data?.identifiers
  const hasLoginName =
    identifiers?.some((identifier) => isLoginName(identifier)) ?? false
  // An email address is a login name, but it is not a username: the
  // completion copy names the sign-in name the account actually holds.
  const hasUsername =
    identifiers?.some((identifier) => identifier.kind === 'username') ?? false
  const passwordSet = profile.data?.password_set === true
  const complete = hasLoginName && passwordSet
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 4, maxWidth: 640 }}>
      {/* The header reflects the state the profile reports: an account that
          already holds a login name and a password is not asked to add them,
          which is the state a returning viewer meets because signing in
          again with the same phone in register mode is an ordinary sign-in
          that lands here. */}
      <PageHeader
        title={
          complete
            ? t('accountSetup.completeTitle')
            : t('accountSetup.title')
        }
        description={complete ? undefined : t('accountSetup.description')}
      />
      {complete ? (
        <Alert severity="success">
          {hasUsername
            ? t('accountSetup.complete')
            : t('accountSetup.completeWithEmail')}
        </Alert>
      ) : null}
      {/* Usernames only: the setup page's job is the login name a password
          sign-in needs, and an email address as one is the account page's
          face, beside the other identifier writes. */}
      <IdentifiersSection session={session} emailAvailable={false} />
      {/* The section owns its heading and the withheld form: with no login
          name it renders the explanation itself, so the page states nothing
          about the password twice. */}
      <PasswordSection session={session} withoutLoginName="explain" />
      <Box>
        <Link
          href={homeLink.href}
          onClick={homeLink.onClick}
          underline="hover"
        >
          {complete ? t('accountSetup.home') : t('accountSetup.later')}
        </Link>
      </Box>
    </Box>
  )
}
