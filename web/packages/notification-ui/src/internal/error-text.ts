/**
 * Error-code text resolution for the notification-ui namespace.
 *
 * Every reachable answer of the notification surface maps, one bundle
 * key per code, under the errors section: the notification module's
 * problem codes (written into the problem's type as
 * `urn:speed:notification:<code>` and read back on the client as
 * `notification.<code>` with dashes spelled as underscores --
 * `bad_request` for a malformed request, `message_not_found` for a
 * message the recipient cannot see, `not_unsubscribable` for a write to
 * a type that cannot be turned off), the session-lifecycle codes any
 * protected surface can receive through the refresh leg, and the
 * transport-level `client.network` / `client.timeout` / `client.protocol`
 * codes of the @speed/api-client contract. Codes outside the whitelist --
 * a client.http.<status> answer, a non-ApiError throw -- resolve to
 * 'errors.unknown', so the bundle can never render a raw key.
 *
 * A bare 404 is the one status with dedicated text: on these surfaces it
 * is the tenant admission layer's refusal, which does not say whether the
 * tenant does not exist, is disabled or has no membership for the caller.
 * A write refused that way renders the same sentence as the read's tenant
 * state, so the two surfaces never describe the same refusal differently.
 */

import {
  CLIENT_TRANSPORT_ERROR_CODES,
  SESSION_LIFECYCLE_ERROR_CODES,
  createErrorTextResolver,
} from '@speed/i18n'
import { useNotificationUiTranslation } from './translation.js'

/**
 * Every code with a dedicated errors-section text. Exported so the
 * suite can pin the whitelist-to-bundle pairing in both languages: a
 * code added here without its two bundle keys (or vice versa) fails the
 * pairing test beside inline-error.tsx.
 */
export const ERROR_TEXT_CODES = [
  // The notification module's problem codes (pkg/notification): the inbox
  // routes answer bad_request/message_not_found and the preference
  // routes answer bad_request/not_unsubscribable.
  'notification.bad_request',
  'notification.message_not_found',
  'notification.not_unsubscribable',
  // The session-lifecycle and transport families every protected surface
  // shares (see @speed/i18n's error-text module).
  ...SESSION_LIFECYCLE_ERROR_CODES,
  ...CLIENT_TRANSPORT_ERROR_CODES,
] as const

const KNOWN_CODES = new Set<string>(ERROR_TEXT_CODES)

/**
 * The bare 404: the admission layer's tenant refusal. It resolves to the
 * surface's tenant sentence rather than the unknown fallback, so a refused
 * write names the same two causes the read's tenant state does.
 */
const TENANT_REFUSAL_CODE = 'client.http.404'

/**
 * The code-to-text resolver hook: call with an ApiError code and get the
 * current-language text ('errors.unknown' for anything unlisted).
 */
export function useNotificationUiErrorText(): (code: string) => string {
  const { t } = useNotificationUiTranslation()
  const resolveKnown = createErrorTextResolver(t, KNOWN_CODES)
  return (code) =>
    code === TENANT_REFUSAL_CODE
      ? t('errors.tenantUnavailable')
      : resolveKnown(code)
}
