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
 * the last-factor guard, last_login_method for the last-login-way guard,
 * email_taken for an
 * address another account has reserved or proved, forbidden for a session
 * that is not the caller's, flow_invalid for a flow binding that has
 * gone, link_required and method_disabled for the social channel,
 * throttled for the shared rate limiter, unavailable for a dependency
 * that could not answer) -- plus the transport-level client.network /
 * client.timeout / client.protocol codes of the @speed/api-client
 * contract and the platform-level `ratelimit.throttled` the address and
 * quota layers answer a throttled request with, maps, one bundle key per
 * code, under the errors section ('errors.authn.bad_request' and so on).
 * A `client.http.<status>` answer -- the bare non-2xx the client
 * synthesizes -- resolves through its own parameterized leaf, carrying the
 * status so a 405, 401 and 500 stay distinguishable. Codes outside the
 * whitelist -- a future authn code, a non-ApiError throw -- resolve to
 * 'errors.unknown', so the bundle can never render a raw key and a missing
 * translation never leaks another language's text or an English fallback.
 *
 * The whitelist composes the shared session-lifecycle and client-transport
 * families (both exported by @speed/i18n) with this surface's own authn
 * codes; a session that dies mid-flight surfaces through the refresh leg
 * as one of the session-lifecycle codes, so each of them carries its own
 * text here rather than collapsing onto 'errors.unknown'. The key
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
 * login_identifier_required, username_immutable, unsupported_media_type,
 * factor_not_found)
 * and the shared
 * session-lifecycle family carry account-authored texts: the sign-in
 * surface does not render them, so there is no sign-in wording to copy.
 * The lifecycle leaves state the outcome (the session has ended) without
 * naming which of the merged causes -- expiry, revocation, reuse -- the
 * server answered with, since the account page has no different remedy to
 * offer and the code must not assert a cause the surface cannot observe.
 */

import {
  CLIENT_TRANSPORT_ERROR_CODES,
  SESSION_LIFECYCLE_ERROR_CODES,
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
  // bad_request/insufficient_user_authentication/impersonation_restricted/
  // factor_not_found/conflict/unavailable,
  // the identifier and unlink routes answer
  // conflict/last_login_method/username_immutable,
  // and the login-flow routes behind the link, enroll and step-up flows
  // answer the rest.
  'authn.bad_request',
  'authn.unsupported_region',
  'authn.login_failed',
  'authn.flow_invalid',
  'authn.link_required',
  'authn.insufficient_user_authentication',
  'authn.sso_required',
  'authn.method_disabled',
  'authn.account_unavailable',
  'authn.impersonation_restricted',
  'authn.forbidden',
  'authn.contact_required',
  'authn.factor_not_found',
  'authn.conflict',
  'authn.email_taken',
  'authn.login_identifier_required',
  'authn.last_login_method',
  'authn.username_immutable',
  'authn.unsupported_media_type',
  'authn.throttled',
  'authn.unavailable',
  'authn.human_verification_required',
  // The shared platform-level refusal the ratelimit layer answers a
  // throttled request with, whichever surface the request came from.
  'ratelimit.throttled',
  // The shared signed-in-surface families: a session that dies mid-flight
  // and the transport-level failures of the api-client contract (see
  // @speed/i18n's error-text module).
  ...SESSION_LIFECYCLE_ERROR_CODES,
  ...CLIENT_TRANSPORT_ERROR_CODES,
] as const

const KNOWN_CODES = new Set<string>(ERROR_TEXT_CODES)

/**
 * The bundle leaf the bare non-2xx family resolves through: the
 * api-client synthesizes `client.http.<status>` for a response with no
 * readable envelope, and the shared resolver carries the status into the
 * namespace's parameterized leaf -- keeping a 405, 401 and 500
 * distinguishable instead of collapsing, with every other unlisted code,
 * onto the single unknown line.
 */
const HTTP_FAMILY_KEY = 'errors.client.http'

/**
 * The code-to-text resolver hook: call with an ApiError code and get the
 * current-language text ('errors.unknown' for anything unlisted).
 */
/** The leaves that name a throttled answer's wait, and the codes that
 * carry one. */
const WAIT_KEY = 'throttle.retryAfter'
const WAIT_LONG_KEY = 'throttle.retryAfterLong'
const WAIT_CODES = ['authn.throttled', 'ratelimit.throttled'] as const

export function useAccountUiErrorText(): (
  code: string,
  retryAfterSeconds?: number,
) => string {
  const { t } = useAccountUiTranslation()
  return createErrorTextResolver(t, KNOWN_CODES, {
    httpFamily: { key: HTTP_FAMILY_KEY },
    wait: { key: WAIT_KEY, longKey: WAIT_LONG_KEY, codes: WAIT_CODES },
  })
}
