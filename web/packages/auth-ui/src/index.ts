/**
 * Public entry of @speed/auth-ui: the sign-in component family over an
 * @speed/auth-core session. Components are controlled over a session prop and
 * render every built-in string from the auth-ui namespace.
 */

export { AUTH_UI_NAMESPACE, authUiResources } from './resources.js'
export {
  PasswordSignInForm,
  type PasswordSignInFormProps,
} from './PasswordSignInForm.js'
export {
  PasswordRegisterForm,
  type PasswordRegisterFormProps,
} from './PasswordRegisterForm.js'
export {
  SMSSignInForm,
  type SMSSignInFormProps,
} from './SMSSignInForm.js'
export {
  SocialSignInSection,
  type SocialProvider,
  type SocialProviderConfig,
  type SocialSignInSectionProps,
} from './SocialSignInSection.js'
export {
  SocialCallbackHandler,
  type SocialCallbackHandlerProps,
} from './SocialCallbackHandler.js'
export {
  SignInScreen,
  type SignInChannel,
  type SignInBeforeStart,
  type SignInContext,
  type SignInGroup,
  type SignInMode,
  type SignInScreenProps,
  type SignInScreenSlots,
  type SignInStartContext,
  type SocialSignInOptions,
} from './SignInScreen.js'
export {
  SignOutButton,
  type SignOutButtonProps,
} from './SignOutButton.js'
export {
  SignOutMenuItem,
  type SignOutMenuItemProps,
} from './SignOutMenuItem.js'
export {
  SessionEndedScreen,
  type SessionEndedScreenProps,
} from './SessionEndedScreen.js'
export {
  AcceptInvitationScreen,
  type AcceptInvitationScreenProps,
  type InvitationEligibility,
  type InvitationOperations,
  type InvitationSummary,
} from './AcceptInvitationScreen.js'
export { type PasswordResetChannel } from '@speed/auth-core'
export {
  PasswordResetScreen,
  type PasswordResetOperations,
  type PasswordResetRequest,
  type PasswordResetScreenProps,
  type PasswordResetSent,
  type PasswordResetVerified,
} from './PasswordResetScreen.js'
export {
  ImpersonationBanner,
  type ImpersonationBannerProps,
  type ImpersonationBannerState,
} from './ImpersonationBanner.js'
export {
  ImpersonationEndMenuItem,
  ImpersonationIndicator,
  ImpersonationNoticeBanner,
  ImpersonationNoticeIndicator,
  ImpersonationNoticeProvider,
  useImpersonationNotice,
  type ImpersonationEndMenuItemProps,
  type ImpersonationIndicatorProps,
  type ImpersonationNotice,
  type ImpersonationNoticeBannerProps,
  type ImpersonationNoticeProviderProps,
} from './ImpersonationNotice.js'
