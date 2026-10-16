/**
 * Error-code text resolution for the objectstore-ui namespace.
 *
 * Every reachable answer of the object page -- the transport-level codes
 * of the @speed/api-client contract (client.network / client.timeout /
 * client.protocol) and the bare-HTTP answers the api-client synthesizes
 * for a non-2xx with no readable code envelope -- maps, one leaf per
 * code, under the errors section. The objectstore module's refusals
 * travel as RFC 9457 problem details objects, whose machine-readable
 * kind is the type `urn:speed:objectstore:<code>` and whose body carries
 * no `code` member; the api-client's envelope reader requires a `code`,
 * so those refusals surface as client.http.<status> and resolve through
 * the parameterized http leaf here. A code outside the whitelist resolves
 * to errors.unknown, so the bundle can never render a raw key and a
 * missing translation never leaks another language's text.
 *
 * The key construction and the unknown fallback are the shared
 * resolver's (see @speed/i18n's error-text module). The bare-HTTP family
 * is parameterized rather than fixed (the status varies), so a status
 * carries through instead of collapsing into the single unknown line.
 */

import { CLIENT_TRANSPORT_ERROR_CODES, createErrorTextResolver } from '@speed/i18n'
import { useObjectstoreUiTranslation } from './translation.js'

/**
 * The bare-HTTP answer the api-client synthesizes for a non-2xx with no
 * readable code envelope: client.http.<status>. It is a family rather
 * than a fixed code (the status varies), so it resolves through its own
 * parameterized leaf instead of the fixed whitelist below.
 */
const HTTP_ERROR_CODE = /^client\.http\.(\d{3})$/

/**
 * Every code with a dedicated errors-section text: the transport-level
 * failures of the api-client contract. Exported so the suite can pin the
 * whitelist-to-bundle pairing in both languages.
 */
export const ERROR_TEXT_CODES = [...CLIENT_TRANSPORT_ERROR_CODES] as const

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
    const http = HTTP_ERROR_CODE.exec(code)
    if (http !== null) {
      return t('errors.http', { status: http[1] })
    }
    return resolveKnown(code)
  }
}
