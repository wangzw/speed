/**
 * Public entry of @speed/auth-ui.
 *
 * The sign-in component family over an @speed/auth-core session: the
 * password and SMS-code channels, the social sign-in section and its
 * callback handler, and SignInScreen assembling the channels behind a tab
 * strip -- each driving its own slice of the session's flow contract and
 * rendering every built-in string from the bilingual auth-ui namespace
 * (AUTH_UI_NAMESPACE + authUiResources, which the host registers alongside
 * ui-kit's). Components are controlled: the session comes in as a prop, a
 * successful sign-in fires the onSignedIn callback and the host navigates
 * -- nothing here reads storage, attaches a session or touches the
 * network directly. Helpers shared between the components live in
 * src/internal/ and are deliberately not exported.
 *
 * There is no registration form: an account is created inside a login
 * flow's account-resolution step (or by invite, or by an administrator),
 * so a "registration" is a login flow's outcome under the deployment's
 * signup setting, not a form of its own.
 *
 * The session surface sits beside the sign-in family: SignOutButton
 * drives session.logout() from a click (a failed logout renders the
 * answer's code text and stays retryable; a successful one is the host's
 * to observe), and SessionEndedScreen is the pure placeholder a host
 * mounts at a view whose authenticated snapshot just turned anonymous,
 * handing the viewer back to its sign-in surface. Both render only
 * auth-ui-namespace text and neither reads session state.
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
