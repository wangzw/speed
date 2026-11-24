/**
 * Public entry of @speed/account-ui: the signed-in account-management
 * component family. Helpers live in src/internal/ and are not exported.
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
 * The binding callback route's source-page reader. A missing or unusable
 * value reads as undefined, leaving the host its own safe landing.
 */
export { bindingReturnTarget } from './internal/binding-return.js'
export {
  useSocialMethodsAvailable,
  type SocialMethodsAvailability,
} from './social-methods.js'
export {
  OwnerTransferRequestsPage,
  type OwnerTransferRequestsPageProps,
} from './OwnerTransferRequestsPage.js'
export { MfaSection, type MfaSectionProps } from './MfaSection.js'
/**
 * The step-up re-verification dialog, exported for the host only. Same-layer
 * packages must not import it.
 */
export {
  StepUpChallenge as StepUpDialog,
  type StepUpChallengeProps as StepUpDialogProps,
} from './internal/step-up-challenge.js'
