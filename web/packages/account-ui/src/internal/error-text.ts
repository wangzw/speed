/**
 * Error-code text resolution for the account-ui namespace.
 */

import {
  CLIENT_TRANSPORT_ERROR_CODES,
  SESSION_LIFECYCLE_ERROR_CODES,
  createErrorTextResolver,
} from '@speed/i18n'
import { useAccountUiTranslation } from './translation.js'

/** Every code with a dedicated errors-section text. */
export const ERROR_TEXT_CODES = [
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
  'authn.phone_required',
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
  'ratelimit.throttled',
  ...SESSION_LIFECYCLE_ERROR_CODES,
  ...CLIENT_TRANSPORT_ERROR_CODES,
] as const

const KNOWN_CODES = new Set<string>(ERROR_TEXT_CODES)

const HTTP_FAMILY_KEY = 'errors.client.http'

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
