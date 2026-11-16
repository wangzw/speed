/**
 * Error-code text resolution for the auth-ui namespace.
 *
 * Every reachable answer of the sign-in and session surface -- the authn
 * flow codes the login-flow endpoints answer with (the login engine's
 * closed problem set: a refused credential, an expired or consumed flow, a
 * disabled method, an account the server will not sign in, a text-message
 * step the account has no verified phone for, a conflict, a throttled
 * attempt, an unavailable dependency, an unsupported phone region, a
 * malformed body), plus the transport-level client.network /
 * client.timeout / client.protocol codes of the @speed/api-client contract
 * -- maps, one bundle key per code, under the errors section
 * ('errors.authn.login_failed' and so on). A client.http.<status> answer --
 * the bare non-2xx the client synthesizes -- resolves through its own
 * parameterized leaf, carrying the status so a 405, 401 and 500 stay
 * distinguishable. Everything else outside the whitelist -- a future authn
 * code, a non-ApiError throw -- resolves to 'errors.unknown', so the bundle
 * can never render a raw key and a missing translation never leaks another
 * language's text or an English fallback.
 *
 * Codes deliberately not whitelisted are the surface's own local notices
 * (the SMS channel's used-code text is a bundle key of its own, never a
 * server code); the transport codes are whitelisted above, and a
 * client.http.<status> answer resolves through the parameterized leaf
 * rather than errors.unknown.
 */

import { createErrorTextResolver } from '@speed/i18n'
import { useAuthUiTranslation } from './translation.js'

/**
 * Every code with a dedicated errors-section text. Exported so the suite
 * can pin the whitelist-to-bundle pairing in both languages: a code added
 * here without its two bundle keys (or vice versa) fails the pairing test
 * beside inline-error.tsx.
 */
export const ERROR_TEXT_CODES = [
  // The login flow's own problem set (a login, a step answer, a resume).
  'authn.login_failed',
  'authn.flow_invalid',
  'authn.link_required',
  'authn.method_disabled',
  'authn.account_unavailable',
  'authn.phone_required',
  'authn.password_reset_closed',
  'authn.password_reset_unavailable',
  'authn.conflict',
  'authn.username_taken',
  'authn.username_reserved',
  'authn.username_invalid',
  'authn.throttled',
  'authn.unavailable',
  'authn.unsupported_region',
  'authn.bad_request',
  'authn.human_verification_required',
  // The invitation acceptance face's own refusals (pkg/authn/invite): the
  // signed-in account has not proved the invited address (the screen opens its
  // proof step instead of the banner, so the text is for any other surface that
  // renders the code), the caller's membership is suspended, where the code is
  // the whole banner, the link already moved to a terminal state, where the
  // screen re-reads and the text stands in if the re-read still answers pending,
  // and the caller is not a user account (a service account or a subject with
  // no account row), where the code is the whole banner because no retry can
  // change the answer.
  'authn.invitation_email_unproved',
  'authn.membership_inactive',
  'authn.invitation_terminal',
  'authn.invitation_target_invalid',
  // Transport-level failures of the api-client contract.
  'client.network',
  'client.timeout',
  'client.protocol',
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
 * current-language text. The key construction and the unknown fallback are
 * the shared resolver's (@speed/i18n's error-text module); this hook binds
 * it to the auth-ui namespace's whitelist and its parameterized bare-HTTP
 * leaf.
 */
/** The leaves that name a throttled answer's wait, and the code carrying it. */
const WAIT_KEY = 'throttle.retryAfter'
const WAIT_LONG_KEY = 'throttle.retryAfterLong'
const WAIT_CODES = ['authn.throttled'] as const

export function useAuthUiErrorText(): (
  code: string,
  retryAfterSeconds?: number,
) => string {
  const { t } = useAuthUiTranslation()
  return createErrorTextResolver(t, KNOWN_CODES, {
    httpFamily: { key: HTTP_FAMILY_KEY },
    wait: { key: WAIT_KEY, longKey: WAIT_LONG_KEY, codes: WAIT_CODES },
  })
}
