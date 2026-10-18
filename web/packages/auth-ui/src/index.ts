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
 * There is no separate registration form: an account is created inside a
 * login flow's account-resolution step (or by invite, or by an
 * administrator), so a "registration" is a login flow's outcome under the
 * deployment's signup setting, not a form of its own. SignInScreen
 * presents a "register" entry over the one flow where the deployment lets
 * a first login open an account; the account still opens server-side.
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
  type SignInContext,
  type SignInMode,
  type SocialSignInOptions,
  type SignInScreenProps,
} from './SignInScreen.js'
export {
  SignOutButton,
  type SignOutButtonProps,
} from './SignOutButton.js'
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
  ImpersonationNoticeProvider,
  useImpersonationNotice,
  type ImpersonationEndMenuItemProps,
  type ImpersonationNotice,
  type ImpersonationNoticeProviderProps,
} from './ImpersonationNotice.js'
