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
 * The account page is also where the user's theme preset is chosen: the
 * appearance section sits ahead of the preferences section, wired to the
 * theme provider's active preset and to the device's stored choice, so a pick
 * applies immediately and survives a reload (src/chrome.tsx holds the storage
 * helpers). The preset names are this bundle's copy; the package renders the
 * options the host hands it.
 *
 * The phone write (account-ui's PhoneSection) is mounted only when the
 * deployment serves the `authn.phone` feature: its two routes are registered
 * only when the deployment delivers an SMS code provider (an assembly that
 * names authn.sms), and this skeleton's baseline carries no SMS family -- the
 * family needs a carrier's credentials, so it is a product's to add (see
 * AGENTS.md and the server's baseline criterion). A section whose submit would
 * be answered 404 is a write the page cannot keep, so a deployment without the
 * feature offers none; a product that adds the SMS family gets the section
 * here beside the other writes.
 *
 * The multifactor section is mounted only when `authn.mfa` is served, and the
 * identifier section learns from `authn.identifiers.email` whether its add
 * face may offer an email (a deployment without it records usernames only).
 *
 * The social-bindings section takes the assembled session (it starts the
 * `link` flow the callback completes) and the callback route the flow
 * lands back on. The skeleton configures no OAuth provider, so the
 * deployment's discovery document carries no redirect method and the
 * add area stays empty; a product that configures one gets its channels
 * from that same document. The callback route is mounted beside the page
 * (src/routes.tsx) for that product.
 *
 * The callback page renders account-ui's BindingCallbackHandler: the flow
 * model completes the provider hand-off server-side and the handler reads
 * the `link` flow back here, then returns the viewer to the account page.
 * The client carries only the access token from step-up-gated writes; the
 * site's own signal handling stays inside account-ui. Its absent slot is the
 * app's 404: a deployment that configures no redirect method has no binding
 * round trip at all.
 */

import type { ReactElement } from 'react'
import { useEffect } from 'react'
import Box from '@mui/material/Box'
import {
  ACCOUNT_UI_NAMESPACE,
  AppearanceSection,
  BindingCallbackHandler,
  ContactVerificationSection,
  IdentifiersSection,
  MfaSection,
  PasswordSection,
  PhoneSection,
  PreferencesSection,
  ProfileSection,
  SessionsSection,
  SocialBindingsSection,
} from '@speed/account-ui'
import { useFeature } from '@speed/auth-core'
import { useTranslation } from '@speed/i18n'
import { useLocationPath, useNavigate } from '@speed/route-kit'
import { themePresets } from '@speed/theme-presets'
import { PageHeader, useAppThemePreset } from '@speed/ui-kit'
import { useAuthnProfileGet } from '../app-api/index.js'
import { useAppServices } from '../app-services.js'
import { CallbackLandingNotice } from '../callback-error-notice.js'
import { persistThemePreset } from '../chrome.js'
import {
  forgetInvitationLanding,
  pendingInvitationLanding,
} from '../invitation-return.js'
import { ACCOUNT_CALLBACK_PATH, ACCOUNT_PATH } from '../paths.js'
import { navigateToAuthorizationUrl } from '../sign-in-navigation.js'
import { useCallbackLanding } from '../use-callback-landing.js'
import { NotFoundPage } from './not-found-page.js'
import { DEMO_NAMESPACE } from '../resources.js'

/** The account-management page: the account-ui sections under a page header. */
export function AccountSettingsPage(): ReactElement {
  const { session } = useAppServices()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const { path: pathname, search } = useLocationPath()
  const landing = new URLSearchParams(search)
  // The banner and the post-registration landing reach this page with an
  // intent: `verify=1` asks for the one automatic code, `change=email` opens
  // the change-address form instead. An ordinary visit carries neither, so the
  // section waits for the visitor to ask.
  const changeOpen = landing.get('change') === 'email'
  const verifyLanding = landing.get('verify') === '1' && !changeOpen
  const navigate = useNavigate()
  // A password registration made from an invitation link lands here to prove
  // the address, and the landing it started from has to survive that detour.
  // Once the profile reads verified, the visitor goes back to the link to
  // accept the invitation. The remembered landing is cleared first, so a later
  // verification does not repeat the move.
  const profile = useAuthnProfileGet({
    query: { enabled: verifyLanding },
  })
  const returnTo = verifyLanding ? pendingInvitationLanding() : null
  useEffect(() => {
    if (returnTo === null || profile.data?.verification?.state !== 'verified') {
      return
    }
    forgetInvitationLanding()
    navigate(returnTo)
  }, [navigate, profile.data, returnTo])
  // Only the host reads the feature document; each section is mounted (or
  // told what it may offer) from the names this deployment serves.
  const phoneServed = useFeature('authn.phone')
  const mfaServed = useFeature('authn.mfa')
  const emailIdentifierServed = useFeature('authn.identifiers.email')
  // The theme preset the provider is applying, and its setter. The choices
  // are the shipped shelf with this bundle's names; a pick applies at once
  // (the provider re-renders the theme) and is written to the device so the
  // next boot starts on it (src/chrome.tsx's initialThemePreset).
  const { preset, setPreset } = useAppThemePreset()
  const presetChoices = themePresets.map((option) => ({
    id: option.id,
    label: t(`themePreset.${option.id}`),
  }))
  const choosePreset = (id: string): void => {
    setPreset(id)
    persistThemePreset(id)
  }
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <PageHeader
        title={t('account.title')}
        description={t('account.description')}
      />
      {/* A step-up or enroll round trip started here lands back here with a
          refusal's problem code in the fragment: this page is the surface
          that reads it (the redirect's `return_to`) and words it. */}
      <CallbackLandingNotice namespace={ACCOUNT_UI_NAMESPACE} />
      <ProfileSection session={session} returnPath={pathname} />
      {/* The account's email verification, completed here rather than on a
          page of its own: `none` renders nothing, `pending` offers the
          send/verify path and the change-address form, and `verified` names
          the proved address. A landing from the banner or from registration
          carries `verify=1`, which sends one code on the way; an ordinary
          visit carries no flag and waits for the visitor to ask. */}
      <ContactVerificationSection
        key={changeOpen ? 'change-email' : verifyLanding ? 'verify' : 'profile'}
        session={session}
        initialChangeOpen={changeOpen}
        autoSend={verifyLanding}
        returnPath={pathname}
      />
      <PasswordSection session={session} returnPath={pathname} />
      {phoneServed ? (
        <PhoneSection session={session} returnPath={pathname} />
      ) : null}
      <IdentifiersSection
        session={session}
        emailAvailable={emailIdentifierServed}
        returnPath={pathname}
      />
      <AppearanceSection
        presets={presetChoices}
        value={preset}
        onChange={choosePreset}
      />
      <PreferencesSection session={session} returnPath={pathname} />
      <SessionsSection />
      <SocialBindingsSection
        session={session}
        returnTo={ACCOUNT_CALLBACK_PATH}
        onAuthorizeUrl={navigateToAuthorizationUrl}
        returnPath={pathname}
      />
      {mfaServed ? (
        <MfaSection session={session} returnPath={pathname} />
      ) : null}
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
 * page-level heading. A deployment with no redirect method has no binding
 * round trip: the handler renders the app's 404 instead of a
 * flow-not-found banner.
 *
 * A refused round trip lands here with its problem code in the URL
 * fragment, the same shape a refused login lands with; there is no flow
 * left to read, so the page hands the code to the handler and gives it the
 * way back to the account page.
 */
export function AccountBindingCallbackPage(): ReactElement {
  const { session } = useAppServices()
  const navigate = useNavigate()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const landing = useCallbackLanding()
  return (
    <Box sx={{ maxWidth: 480 }}>
      <PageHeader title={t('account.bindingCallback.title')} sx={{ mb: 3 }} />
      <BindingCallbackHandler
        session={session}
        onBound={() => navigate(ACCOUNT_PATH)}
        absent={<NotFoundPage path={ACCOUNT_CALLBACK_PATH} />}
        initialError={landing?.code}
        onDismiss={() => navigate(ACCOUNT_PATH)}
      />
    </Box>
  )
}
