/**
 * Error-code text resolution for the objectstore-ui namespace.
 *
 * Every reachable answer of the object page maps, one leaf per code,
 * under the errors section: the session-lifecycle codes a session that
 * dies mid-flight surfaces as through the refresh leg
 * (SESSION_LIFECYCLE_ERROR_CODES); the objectstore module's own refusal
 * codes (`objectstore.bad_request` / `objectstore.not_found` /
 * `objectstore.conflict`) and the platform authorization guard's
 * permission refusal (`authz.forbidden`); the transport-level codes of
 * the @speed/api-client contract (client.network / client.timeout /
 * client.protocol); and the bare-HTTP answers the api-client synthesizes
 * for a non-2xx with no module code (client.http.<status>). The
 * objectstore module's refusals travel as RFC 9457 problem details
 * objects whose machine-readable kind is the type
 * `urn:speed:objectstore:<code>`; the api-client derives the
 * `<module>.<code>` code from that type (dashes normalized to
 * underscores), so a refusal surfaces as e.g. `objectstore.not_found`.
 * A code outside the whitelist resolves to errors.unknown, so the bundle
 * can never render a raw key and a missing translation never leaks
 * another language's text.
 *
 * The key construction and the unknown fallback are the shared
 * resolver's (see @speed/i18n's error-text module). The bare-HTTP family
 * is parameterized rather than fixed (the status varies), so a status
 * carries through instead of collapsing into the single unknown line.
 *
 * A bare 404 is the one status with dedicated text: on this page it is the
 * tenant admission layer's refusal, which does not say whether the tenant
 * does not exist, is disabled or has no membership for the caller. A write
 * refused that way renders the same sentence as the read's tenant state,
 * so the two surfaces never describe the same refusal differently. The
 * objectstore module's own missing-object 404 arrives as the typed
 * `objectstore.not_found` and keeps its own leaf.
 */

import {
  CLIENT_TRANSPORT_ERROR_CODES,
  SESSION_LIFECYCLE_ERROR_CODES,
  createErrorTextResolver,
} from '@speed/i18n'
import { useObjectstoreUiTranslation } from './translation.js'

/**
 * The bare-HTTP answer the api-client synthesizes for a non-2xx with no
 * module code (neither a body `code` nor a `urn:speed:` problem type):
 * client.http.<status>. It is a family rather than a fixed code (the
 * status varies), so it resolves through its own parameterized leaf
 * instead of the fixed whitelist below.
 */
const HTTP_ERROR_CODE = /^client\.http\.(\d{3})$/

/**
 * The bare 404: the admission layer's tenant refusal. It resolves to the
 * page's tenant sentence rather than the parameterized status line, so a
 * refused write names the same two causes the read's tenant state does.
 */
const TENANT_REFUSAL_CODE = 'client.http.404'

/**
 * Every code with a dedicated errors-section text: the session-lifecycle
 * answers any protected surface can receive through the refresh leg, the
 * objectstore module's own refusals, the authorization guard's permission
 * refusal, and the transport-level failures of the api-client contract.
 * Exported so the suite can pin the whitelist-to-bundle pairing in both
 * languages.
 */
export const ERROR_TEXT_CODES = [
  // A session that dies mid-flight surfaces as one of these through the
  // refresh leg; each renders its own text instead of the generic
  // fallback.
  ...SESSION_LIFECYCLE_ERROR_CODES,
  ...CLIENT_TRANSPORT_ERROR_CODES,
  // pkg/objectstore/metadata's refusals: the problem types
  // urn:speed:objectstore:{bad-request,not-found,conflict}, derived by the
  // api-client to the objectstore.<code> shape below.
  'objectstore.bad_request',
  'objectstore.not_found',
  'objectstore.conflict',
  // The authorization guard's permission refusal
  // (urn:speed:authz:forbidden): a read is branched to the no-permission
  // empty state by status, and a refused write renders this code's text.
  'authz.forbidden',
] as const

const KNOWN_CODES = new Set<string>(ERROR_TEXT_CODES)

/**
 * The code-to-text resolver hook: call with an ApiError code and get the
 * current-language text ('errors.unknown' for anything unlisted, and the
 * client.http.<status> family through its parameterized leaf).
 */
export function useObjectstoreUiErrorText(): (code: string) => string {
  const { t } = useObjectstoreUiTranslation()
  const resolveKnown = createErrorTextResolver(t, KNOWN_CODES)
  return (code) => {
    if (code === TENANT_REFUSAL_CODE) {
      return t('errors.tenantUnavailable')
    }
    const http = HTTP_ERROR_CODE.exec(code)
    if (http !== null) {
      return t('errors.http', { status: http[1] })
    }
    return resolveKnown(code)
  }
}
