/**
 * Public entry of @speed/account-ui.
 *
 * The signed-in account-management component family over an
 * @speed/auth-core session and the generated authn operations: the
 * profile surface (the display name over the profile's own PATCH
 * /api/v1/auth/me), the password
 * surface (PUT /api/v1/auth/password), the phone
 * surface (the two-step POST /api/v1/auth/phone and /api/v1/auth/phone/verify), the
 * sign-in-identifiers surface (the profile's own identifiers and
 * DELETE /api/v1/auth/identifiers/{id}), the preferences surface (language and
 * time zone over the profile's own PATCH /api/v1/auth/me), the appearance
 * surface (a controlled theme-preset choice whose presets and labels the
 * host supplies), the session list
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
 * panel and step-up dialog drive `purpose: 'enroll'` and `purpose:
 * 'step-up'` flows the same way. A social channel is the deployment's
 * own login-method name, read from the discovery document; the package
 * carries no host-supplied channel list.
 *
 * StepUpDialog is the dialog account-ui's own surfaces use, exported for
 * a host that needs the same re-verification surface. It is deliberately
 * not importable from the same-layer @speed/auth-ui and @speed/console-ui
 * packages (the latter keeps its own copy); see ADR-web-auth-seams.
 */

export { ACCOUNT_UI_NAMESPACE, accountUiResources } from './resources.js'
export { ProfileSection } from './ProfileSection.js'
export {
  PasswordSection,
  type PasswordSectionProps,
} from './PasswordSection.js'
export { PhoneSection, type PhoneSectionProps } from './PhoneSection.js'
export {
  IdentifiersSection,
  type IdentifiersSectionProps,
} from './IdentifiersSection.js'
export {
  ContactVerificationSection,
  type ContactVerificationSectionProps,
} from './ContactVerificationSection.js'
export {
  AppearanceSection,
  type AppearanceSectionProps,
  type ThemePresetChoice,
} from './AppearanceSection.js'
export { PreferencesSection } from './PreferencesSection.js'
export {
  ColorModeSection,
  type ColorModeSectionProps,
} from './ColorModeSection.js'
export {
  SessionsSection,
  type SessionsSectionProps,
} from './SessionsSection.js'
export {
  SocialBindingsSection,
  type SocialBindingsSectionProps,
} from './SocialBindingsSection.js'
export {
  BindingCallbackHandler,
  type BindingCallbackHandlerProps,
} from './BindingCallbackHandler.js'
/**
 * The binding callback route's source-page reader. The link flow carries
 * the page it started from inside its `return_to`; the host mounts the
 * callback handler on that route and reads the page back with this, so a
 * completed round trip returns to where it began. A missing or unusable
 * value reads as undefined, leaving the host its own safe landing.
 */
export { bindingReturnTarget } from './internal/binding-return.js'
export {
  useSocialMethodsAvailable,
  type SocialMethodsAvailability,
} from './social-methods.js'
export { MfaSection, type MfaSectionProps } from './MfaSection.js'
/**
 * The step-up re-verification dialog, exported for the host. Its gate is
 * `useStepUpGate` from @speed/auth-core; the host composes the two.
 */
export {
  StepUpChallenge as StepUpDialog,
  type StepUpChallengeProps as StepUpDialogProps,
} from './internal/step-up-challenge.js'
