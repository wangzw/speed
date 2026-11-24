/**
 * Error-code text resolution for the admin-ui namespace; a code outside the
 * whitelist resolves to `errors.unknown`, and `client.http.<status>` uses its leaf.
 */

import {
  CLIENT_TRANSPORT_ERROR_CODES,
  SESSION_LIFECYCLE_ERROR_CODES,
  createErrorTextResolver,
} from '@speed/i18n'
import { useAdminUiTranslation } from './translation.js'

/** The parameterized bare-HTTP answer the api-client synthesizes, `client.http.<status>`. */
const HTTP_ERROR_CODE = /^client\.http\.(\d{3})$/

/** Every code with a dedicated errors-section text; the pairing test pins both bundles. */
export const ERROR_TEXT_CODES = [
  ...SESSION_LIFECYCLE_ERROR_CODES,
  ...CLIENT_TRANSPORT_ERROR_CODES,
  'tenant.bad_request',
  'tenant.conflict',
  'tenant.protected',
  'org.creation_in_progress',
  'org.identifier_unavailable',
  'org.organization_limit',
  'org.rate_limited',
  'authz.bad_request',
  'authz.conflict',
  'authz.forbidden',
  'authz.protected',
  'authn.bad_request',
  'authn.cannot_suspend_self',
  'authn.unsupported_region',
  'authn.conflict',
  'authn.email_taken',
  'authn.login_identifier_required',
  'authn.last_login_method',
  'authn.username_immutable',
  'authn.contact_required',
  'authn.forbidden',
  'authn.impersonation_restricted',
  'authn.invitation_target_invalid',
  'authn.invitation_expiry_too_long',
  'authn.invitation_member',
  'authn.invitation_pending',
  'authn.invitation_terminal',
  'authn.invitation_role_unknown',
  'authn.invitation_role_protected',
  'authn.account_last_tenant_admin',
  'authn.account_organization_owner',
  'authn.sso_required',
  'authn.insufficient_user_authentication',
  'authn.factor_not_seasoned',
  'authn.login_failed',
  'authn.flow_invalid',
  'authn.throttled',
  'authn.unavailable',
  'authn.human_verification_required',
  'ratelimit.throttled',
  'audit.bad_request',
  'settings.bad_request',
  'settings.invalid_value',
  'settings.protected',
  'settings.read_only',
  'settings.tenant_admission_enforced',
  'settings.masked_value',
  'sms.bad_request',
  'sms.conflict',
  'sms.config_template',
] as const

/** The leaves that name a throttled answer's wait, and the codes carrying one. */
const WAIT_KEY = 'throttle.retryAfter'
const WAIT_LONG_KEY = 'throttle.retryAfterLong'
const WAIT_CODES = ['authn.throttled', 'ratelimit.throttled'] as const

/**
 * The admin-ui error-text resolver over the whitelist plus the bare-HTTP
 * family; reads the current language reactively.
 */
export function useAdminUiErrorText(): (
  code: string,
  retryAfterSeconds?: number,
) => string {
  const { t } = useAdminUiTranslation()
  const known = new Set<string>(ERROR_TEXT_CODES)
  const resolve = createErrorTextResolver(t, known, {
    wait: { key: WAIT_KEY, longKey: WAIT_LONG_KEY, codes: WAIT_CODES },
  })
  return (code, retryAfterSeconds) => {
    const http = HTTP_ERROR_CODE.exec(code)
    if (http === null) {
      return resolve(code, retryAfterSeconds)
    }
    // A bare 403 or 401 carries the actionable copy; the generic status line
    // would only restate the status.
    if (http[1] === '403') {
      return t('errors.client.httpForbidden')
    }
    if (http[1] === '401') {
      return t('errors.client.httpUnauthorized')
    }
    return t('errors.client.http', { status: http[1] ?? '' })
  }
}
