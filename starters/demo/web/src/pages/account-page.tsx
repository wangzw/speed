/**
 * The account page and the social-binding callback beside it.
 *
 * @speed/account-ui ships the account family as sections, not a routed
 * screen: the page, its heading and the surrounding chrome are the host's.
 * This page is that host composition -- a ui-kit PageHeader naming the
 * page, then the sections the package exports (the self-service writes: the
 * display name, the password and the sign-in identifiers; then
 * preferences, active sessions, social bindings, and the step-up-gated
 * two-step-verification setup). Each section renders its own async state
 * (loading, empty, error) from the account-ui namespace, so nothing here
 * repeats a spinner or an error block; the catalog's states are the
 * sections' own.
 *
 * The phone write (account-ui's PhoneSection) is deliberately not mounted.
 * Its two routes are registered only when the deployment delivers an SMS
 * code provider (an assembly that names authn.sms), and this skeleton's
 * baseline carries no SMS family -- the family needs a carrier's
 * credentials, so it is a product's to add (see AGENTS.md and the
 * server's baseline criterion). A section whose submit would be answered
 * 404 is a write the page cannot keep, so the skeleton offers none; a
 * product that adds the SMS family puts PhoneSection here beside the other
 * writes.
 *
 * The social-bindings section takes the assembled session (it starts the
 * `link` flow the callback completes) and the channels the host offers.
 * The skeleton offers none -- it configures no OAuth provider, and
 * offering a channel nothing can finish is a promise no phone keeps, the
 * same rule the sign-in surface follows -- so the section lists the
 * account's existing bindings and shows no add area. Its callback route
 * is mounted beside the page (src/routes.tsx) for a product that
 * configures a provider.
 *
 * The callback page renders account-ui's BindingCallbackHandler: the flow
 * model completes the provider hand-off server-side and the handler reads
 * the `link` flow back here, then returns the viewer to the account page.
 * The client carries only the access token from step-up-gated writes; the
 * site's own signal handling stays inside account-ui.
 */

import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import {
  BindingCallbackHandler,
  IdentifiersSection,
  MfaSection,
  PasswordSection,
  PreferencesSection,
  ProfileSection,
  SessionsSection,
  SocialBindingsSection,
} from '@speed/account-ui'
import { useTranslation } from '@speed/i18n'
import { useNavigate } from '@speed/route-kit'
import { PageHeader } from '@speed/ui-kit'
import { useAppServices } from '../app-services.js'
import { ACCOUNT_PATH } from '../paths.js'
import { DEMO_NAMESPACE } from '../resources.js'

/** The account-management page: the account-ui sections under a page header. */
export function AccountSettingsPage(): ReactElement {
  const { session } = useAppServices()
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <PageHeader
        title={t('account.title')}
        description={t('account.description')}
      />
      <ProfileSection session={session} />
      <PasswordSection session={session} />
      <IdentifiersSection session={session} />
      <PreferencesSection />
      <SessionsSection />
      <SocialBindingsSection session={session} providers={[]} />
      <MfaSection session={session} />
    </Box>
  )
}

/**
 * The social-binding callback route: account-ui's handler reads the
 * `link` flow back and, on completion, returns the viewer to the account
 * page. With no provider configured no flow reaches here today; the route
 * exists so a product that configures one has the completion point wired.
 * The page carries its own PageHeader (the one h1 for the route), because
 * account-ui's handler renders none and the route still needs a
 * page-level heading.
 */
export function AccountBindingCallbackPage(): ReactElement {
  const { session } = useAppServices()
  const navigate = useNavigate()
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Box sx={{ maxWidth: 480 }}>
      <PageHeader title={t('account.bindingCallback.title')} sx={{ mb: 3 }} />
      <BindingCallbackHandler
        session={session}
        onBound={() => navigate(ACCOUNT_PATH)}
      />
    </Box>
  )
}
