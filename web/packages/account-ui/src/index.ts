/**
 * Public entry of @speed/account-ui.
 *
 * The signed-in account-management component family over an
 * @speed/auth-core session and the generated authn operations: the
 * profile surface (the display name over the profile's own PATCH
 * /auth/me), the password surface (PUT /auth/password), the phone
 * surface (the two-step POST /auth/phone and /auth/phone/verify), the
 * sign-in-identifiers surface (the profile's own identifiers and
 * DELETE /auth/identifiers/{id}), the preferences surface (language and
 * time zone over the profile's own PATCH /auth/me), the session list
 * (with per-session and bulk revocation), the social-binding surface and
 * its callback handler, and the step-up-gated two-step-verification setup
 * -- each rendering every built-in string from the bilingual account-ui
 * namespace
 * (ACCOUNT_UI_NAMESPACE + accountUiResources, which the host registers
 * alongside ui-kit's). Components are controlled and their reads go
 * through the generated react-query hooks of @speed/api-sdk over the
 * host's QueryClient: nothing here reads storage, attaches a session,
 * navigates or touches the network directly. Helpers shared between the
 * components live in src/internal/ and are deliberately not exported.
 *
 * The session is a required prop exactly where a session operation
 * exists that the generated surface cannot express: the bindings add
 * area starts a `purpose: 'link'` flow (session.startAuthenticatedFlow),
 * the callback handler reads that flow back and completes it
 * (session.readFlow / session.answerFlow), and the two-factor enroll
 * wizard and step-up dialog drive `purpose: 'enroll'` and `purpose:
 * 'step-up'` flows the same way. The provider vocabulary
 * (SocialProvider/SocialProviderConfig) is this package's own
 * definition, copied to match @speed/auth-ui's and kept in sync with it
 * -- same-layer packages never import each other, and a social channel
 * is a deployment login-method name (`SocialProvider` is a string, not a
 * closed set).
 */

export { ACCOUNT_UI_NAMESPACE, accountUiResources } from './resources.js'
export {
  ProfileSection,
  type ProfileSectionProps,
} from './ProfileSection.js'
export {
  PasswordSection,
  type PasswordSectionProps,
} from './PasswordSection.js'
export { PhoneSection, type PhoneSectionProps } from './PhoneSection.js'
export {
  IdentifiersSection,
  type IdentifiersSectionProps,
} from './IdentifiersSection.js'
export { PreferencesSection } from './PreferencesSection.js'
export { SessionsSection } from './SessionsSection.js'
export {
  SocialBindingsSection,
  type SocialBindingsSectionProps,
  type SocialProvider,
  type SocialProviderConfig,
} from './SocialBindingsSection.js'
export {
  BindingCallbackHandler,
  type BindingCallbackHandlerProps,
} from './BindingCallbackHandler.js'
export { MfaSection, type MfaSectionProps } from './MfaSection.js'
