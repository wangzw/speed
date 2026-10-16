/**
 * Error-code text resolution for the console-ui namespace.
 *
 * Every reachable answer of the platform console -- the shared
 * session-lifecycle codes any signed-in surface can receive when a
 * session dies mid-flight (the console is reached with an access token
 * like every protected surface), the transport-level client.namespace
 * codes of the @speed/api-client contract, and the console surfaces' own
 * codes -- maps, one bundle key per code, under the errors section. A
 * code outside the whitelist resolves to 'errors.unknown', so the bundle
 * can never render a raw key and a missing translation never leaks
 * another language's text or an English fallback.
 *
 * The surfaces' own codes are the module-scoped refusals the three
 * administration modules answer their writes with: a roster registration
 * or rename colliding with an existing identifier or alias (tenant conflict),
 * a settings write whose value is outside the item's declared domain, and
 * the shape refusals of a body the handler cannot read. The bundle key is
 * the colon-free frontend code (i18next's default namespace separator is
 * ':'), and how the backend's RFC 9457 problem `type` becomes that code
 * is the API contract's to fix -- the codes below are the console's
 * reading of the modules' error surfaces and are recorded on the package's
 * AGENTS.md as a contract-phase item.
 *
 * The whitelist composes the shared families every protected surface can
 * be answered with -- the session-lifecycle and client-transport codes
 * exported by @speed/i18n -- with the console's own codes; the key
 * construction and the unknown fallback are the shared resolver's (see
 * @speed/i18n's error-text module). Beside the fixed whitelist, the
 * api-client's bare-HTTP answers (client.http.<status>) resolve through
 * one parameterized leaf carrying the status, so the status-bearing
 * refusals the console can be answered with stay distinguishable instead
 * of collapsing into the single unknown line.
 */

import {
  CLIENT_TRANSPORT_ERROR_CODES,
  SESSION_LIFECYCLE_ERROR_CODES,
  createErrorTextResolver,
} from '@speed/i18n'
import { useConsoleUiTranslation } from './translation.js'

/**
 * The bare-HTTP answer the api-client synthesizes for a non-2xx with no
 * readable envelope: client.http.<status>. It is a family rather than a
 * fixed code (the status varies), so it resolves through its own
 * parameterized leaf instead of the fixed whitelist below.
 */
const HTTP_ERROR_CODE = /^client\.http\.(\d{3})$/

/**
 * Every code with a dedicated errors-section text. Exported so the suite
 * can pin the whitelist-to-bundle pairing in both languages: a code added
 * here without its two bundle keys (or vice versa) fails the pairing test
 * beside inline-error.tsx.
 */
export const ERROR_TEXT_CODES = [
  // The shared families (see @speed/i18n): the console's reads and writes
  // talk through the api-client's transport, and any surface reached with
  // a token can be answered with a session-lifecycle code when the token
  // refresh is refused.
  ...SESSION_LIFECYCLE_ERROR_CODES,
  ...CLIENT_TRANSPORT_ERROR_CODES,
  // tenant admin: the 409 the roster answers a registration or rename
  // colliding with an existing identifier or alias (a shape refusal is a
  // 400 the module names bad-request -- it has no console-specific text).
  'tenant.conflict',
  // settings admin: the 422 a write whose value is outside the declared
  // item's domain is answered with (its own shape refusal is a 400).
  'settings.invalid_value',
] as const

/**
 * The console-ui error-text resolver over the whitelist above, plus the
 * bare-HTTP family: a listed code resolves to its own errors-section leaf,
 * a client.http.<status> answer to the parameterized errors.client.http
 * leaf (so a 405, 401 and 500 render distinct, status-carrying lines
 * rather than one generic fallback), and anything else to the shared
 * unknown fallback. Reads the current language reactively (the returned
 * function is recreated when the language changes).
 */
export function useConsoleUiErrorText(): (code: string) => string {
  const { t } = useConsoleUiTranslation()
  const known = new Set<string>(ERROR_TEXT_CODES)
  const resolve = createErrorTextResolver(t, known)
  return (code) => {
    const http = HTTP_ERROR_CODE.exec(code)
    return http === null
      ? resolve(code)
      : t('errors.client.http', { status: http[1] ?? '' })
  }
}
