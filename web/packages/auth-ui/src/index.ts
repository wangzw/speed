/**
 * Public entry of @speed/auth-ui.
 *
 * The sign-in component family over an @speed/auth-core session: the
 * password and SMS-code channels, the social / redirect section and its
 * callback handler, and SignInScreen assembling the deployment's login
 * methods -- read from the discovery document and grouped by kind (forms
 * behind a tab strip, redirects as buttons, an enterprise email-first
 * entry, the passkey ceremony) -- each driving its own slice of the
 * session's flow contract and rendering every built-in string from the
 * bilingual auth-ui namespace (AUTH_UI_NAMESPACE + authUiResources, which
 * the host registers alongside ui-kit's). Components are controlled: the
 * session comes in as a prop, a successful sign-in fires the onSignedIn
 * callback and the host navigates -- nothing here reads storage, attaches
 * a session or touches the network directly. Helpers shared between the
 * components live in src/internal/ and are deliberately not exported.
 *
 * Registration has two channels. An account can be opened inside a login
 * flow's account-resolution step (or by invite, or by an administrator),
 * and where the deployment enables password registration a dedicated
 * form opens one with `session.register`. PasswordRegisterForm drives
 * that single request and then signs in with the same credentials
 * through the ordinary password flow; the account always opens
 * server-side and the form neither establishes a session itself nor
 * writes storage. SignInScreen presents a "register" entry over both
 * channels, adding the password-registration channel when the discovery
 * document enables it.
 *
 * The session surface sits beside the sign-in family: SignOutButton
 * drives session.logout() from a click (a failed logout renders the
 * answer's code text and stays retryable; a successful one is the host's
 * to observe), and SessionEndedScreen is the pure placeholder a host
 * mounts at a view whose authenticated snapshot just turned anonymous,
 * handing the viewer back to its sign-in surface. Both render only
 * auth-ui-namespace text and neither reads session state.
 *
 * ImpersonationBanner is the same kind of presentational surface for an
 * impersonated session: the host keeps it mounted in the app frame, says
 * whether an impersonation is current and binds the operation that ends
 * it. It reads no session and issues no request of its own.
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
  type AcceptInvitationInput,
  type InvitationOperations,
  type InvitationSummary,
} from './AcceptInvitationScreen.js'
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
