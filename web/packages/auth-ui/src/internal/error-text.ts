/** The auth-ui namespace's error-code text whitelist and resolver. */

import { createErrorTextResolver } from '@speed/i18n'
import { useAuthUiTranslation } from './translation.js'

export const ERROR_TEXT_CODES = [
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
  'authn.invitation_email_unproved',
  'authn.membership_inactive',
  'authn.invitation_terminal',
  'authn.invitation_target_invalid',
  'client.network',
  'client.timeout',
  'client.protocol',
] as const

const KNOWN_CODES = new Set<string>(ERROR_TEXT_CODES)

const HTTP_FAMILY_KEY = 'errors.client.http'

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
