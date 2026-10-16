/**
 * Error-code text resolution for the auth-ui namespace.
 *
 * Every reachable answer of the sign-in and session surface -- the authn
 * flow codes the login-flow endpoints answer with (the login engine's
 * closed problem set: a refused credential, an expired or consumed flow, a
 * disabled method, an account the server will not sign in, a conflict, a
 * throttled attempt, an unavailable dependency, an unsupported phone
 * region, a malformed body), plus the transport-level client.network /
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
 * server code) and the client.* answers the client synthesizes for a bare
 * non-2xx or an unreadable body, which land on errors.unknown by design.
 */

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
  'authn.conflict',
  'authn.throttled',
  'authn.unavailable',
  'authn.unsupported_region',
  'authn.bad_request',
  // Transport-level failures of the api-client contract.
  'client.network',
  'client.timeout',
  'client.protocol',
] as const

const KNOWN_CODES = new Set<string>(ERROR_TEXT_CODES)

/**
 * The bare non-2xx the api-client synthesizes as client.http.<status> (a
 * response with no readable envelope). A family rather than a fixed code,
 * so it resolves through its own parameterized leaf -- the status kept in
 * the text -- instead of collapsing, with every other unlisted code, onto
 * the single unknown line.
 */
const HTTP_ERROR_CODE = /^client\.http\.(\d{3})$/

/**
 * The code-to-text resolver hook: call with an ApiError code and get the
 * current-language text. A client.http.<status> answer resolves to the
 * parameterized errors.client.http leaf (its status carried through);
 * anything else unlisted resolves to 'errors.unknown'.
 */
export function useAuthUiErrorText(): (code: string) => string {
  const { t } = useAuthUiTranslation()
  return (code: string) => {
    const http = HTTP_ERROR_CODE.exec(code)
    return http === null
      ? t(KNOWN_CODES.has(code) ? `errors.${code}` : 'errors.unknown')
      : t('errors.client.http', { status: http[1] ?? '' })
  }
}
