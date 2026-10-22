/**
 * Error-code text resolution for the account-ui namespace.
 *
 * Every reachable answer of the signed-in account surface -- the problem
 * codes the authn module's session, profile, factor and login-flow
 * routes answer with (pkg/authn/internal/mount's closed set, written
 * into the problem's type as `urn:speed:authn:<code>` and read back on
 * the client as `authn.<code>` with the dashes spelled as underscores:
 * bad_request for a malformed write, insufficient_user_authentication
 * for an operation the caller's token does not yet prove, conflict for
 * the last-factor and last-login-method guards, email_taken for an
 * address another account has reserved or proved, forbidden for a session
 * that is not the caller's, flow_invalid for a flow binding that has
 * gone, link_required and method_disabled for the social channel,
 * throttled for the shared rate limiter, unavailable for a dependency
 * that could not answer) -- plus the transport-level client.network /
 * client.timeout / client.protocol codes of the @speed/api-client
 * contract, maps, one bundle key per code, under the errors section
 * ('errors.authn.bad_request' and so on). Codes outside the whitelist --
 * a future authn code, a client.http.<status> answer, a non-ApiError
 * throw -- resolve to 'errors.unknown', so the bundle can never render a
 * raw key and a missing translation never leaks another language's text
 * or an English fallback.
 *
 * The whitelist composes the shared client-transport family (the codes
 * exported by @speed/i18n) with this surface's own authn codes; the key
 * construction and the unknown fallback are the shared resolver's (see
 * @speed/i18n's error-text module).
 *
 * Wording policy: the codes whose failure context is identical to the
 * sign-in surface's reuse the auth-ui bundle's text verbatim, so the same
 * server answer reads the same on both surfaces. Same-tier packages never
 * import one another's catalogs, so those leaves are deliberate
 * duplicates of the sign-in bundle's own -- a duplication the error-text
 * suite pins to its source, importing the auth-ui bundles as test data,
 * so the copies cannot drift. The account-only codes
 * (insufficient_user_authentication, impersonation_restricted, forbidden,
 * login_identifier_required, unsupported_media_type) carry
 * account-authored texts: the sign-in surface does not render them, so
 * there is no sign-in wording to copy.
 */

import {
  CLIENT_TRANSPORT_ERROR_CODES,
  createErrorTextResolver,
} from '@speed/i18n'
import { useAccountUiTranslation } from './translation.js'

/**
 * Every code with a dedicated errors-section text. Exported so the suite
 * can pin the whitelist-to-bundle pairing in both languages: a code
 * added here without its two bundle keys (or vice versa) fails the
 * pairing test beside inline-error.tsx.
 */
export const ERROR_TEXT_CODES = [
  // The authn module's problem codes (pkg/authn/internal/mount). The
  // account surface can be answered with any of them: the session routes
  // answer bad_request/forbidden/unavailable, the profile PATCH answers
  // bad_request/insufficient_user_authentication/impersonation_restricted,
  // the factor routes answer
  // bad_request/insufficient_user_authentication/impersonation_restricted/conflict/unavailable,
  // and the login-flow routes behind the link, enroll and step-up flows
  // answer the rest.
  'authn.bad_request',
  'authn.unsupported_region',
  'authn.login_failed',
  'authn.flow_invalid',
  'authn.link_required',
  'authn.insufficient_user_authentication',
  'authn.method_disabled',
  'authn.account_unavailable',
  'authn.impersonation_restricted',
  'authn.forbidden',
  'authn.contact_required',
  'authn.conflict',
  'authn.email_taken',
  'authn.login_identifier_required',
  'authn.unsupported_media_type',
  'authn.throttled',
  'authn.unavailable',
  // Transport-level failures of the api-client contract (see @speed/i18n).
  ...CLIENT_TRANSPORT_ERROR_CODES,
] as const

const KNOWN_CODES = new Set<string>(ERROR_TEXT_CODES)

/**
 * The code-to-text resolver hook: call with an ApiError code and get the
 * current-language text ('errors.unknown' for anything unlisted).
 */
export function useAccountUiErrorText(): (code: string) => string {
  const { t } = useAccountUiTranslation()
  return createErrorTextResolver(t, KNOWN_CODES)
}
