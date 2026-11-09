/**
 * Error-code text resolution for the admin-ui namespace.
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
 * The surfaces' own codes are the module-scoped refusals the
 * administration modules answer their writes with: the shape refusals of a
 * body the handler cannot read, the collisions a registration, rename or
 * domain claim is answered with, the 403 a denied or impersonation-
 * restricted write is answered with, and a setting write whose value is
 * outside the item's declared domain. A module answers a refusal with an
 * RFC 9457 problem object whose machine-readable kind is its `type`
 * (`urn:speed:<module>:<code>`), and the @speed/api-client derives the
 * colon-free frontend code `<module>.<code>` from it (each dash in the
 * code part normalized to an underscore), the key i18next resolves under
 * the errors section below.
 *
 * The whitelist composes the shared families every protected surface can
 * be answered with -- the session-lifecycle and client-transport codes
 * exported by @speed/i18n -- with the console's own codes; the key
 * construction and the unknown fallback are the shared resolver's (see
 * @speed/i18n's error-text module). Beside the fixed whitelist, the
 * api-client's bare-HTTP answers (client.http.<status>) resolve through one
 * parameterized leaf carrying the status, except the two a write refusal
 * most often carries: a bare 403 renders the permission-refusal copy and a
 * bare 401 the ended-session copy, so both tell the operator what to do
 * instead of only restating the status.
 */

import {
  CLIENT_TRANSPORT_ERROR_CODES,
  SESSION_LIFECYCLE_ERROR_CODES,
  createErrorTextResolver,
} from '@speed/i18n'
import { useAdminUiTranslation } from './translation.js'

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
  // The module-scoped refusals the administration surfaces answer their
  // writes with, one leaf per code (the api-client derives each
  // <module>.<code> from the module's RFC 9457 problem `type`).
  //
  // tenant admin: a 400 shape refusal, the 409 a registration
  // colliding with an existing identifier or name (or a tombstone's write)
  // is answered with, and the 403 the host's write guard answers a write
  // into a protected tenant with.
  'tenant.bad_request',
  'tenant.conflict',
  'tenant.protected',
  // org create (the demo host's self-service surface): the 409s a
  // creation is refused with -- another creation in flight, the
  // account's active-organization cap, and a generated identifier or
  // name collision -- and the 429 the hourly creation limit answers
  // with.
  'org.creation_in_progress',
  'org.identifier_unavailable',
  'org.organization_limit',
  'org.rate_limited',
  // authz admin: a 400 shape refusal, the 409 a role or binding collision
  // is answered with, the 403 the authorization guard refuses a write the
  // caller is not permitted to make with, and the 403 the host's write
  // guard answers a write into a protected domain with (distinct from
  // forbidden: the caller may hold every permission in that domain).
  'authz.bad_request',
  'authz.conflict',
  'authz.forbidden',
  'authz.protected',
  // authn admin (accounts, impersonations, invitations, members, sso): a
  // 400 shape refusal, the 400 an unsupported region is answered with,
  // the 409 an account, domain or factor collision is answered with, the
  // 409 an email address another account has reserved or proved is
  // answered with, the 409 a password write is refused with when the
  // account has no login name, the 409 removing the account's last way to
  // sign in is refused with, the 403 a write with an unproved email
  // address is answered with, the 409 removing a username is refused with,
  // and the 403 a denied or
  // impersonation-restricted write is answered with.
  'authn.bad_request',
  // A member write may not suspend the caller's own membership; the server
  // refuses it with 409 so the tenant keeps at least one active administrator.
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
  // The 409 an account deletion is refused with when the account is the
  // last administrator of an active business organization. The copy tells
  // the operator to transfer the role before deleting the account.
  'authn.account_last_tenant_admin',
  // The 409 an account deletion or suspension is refused with when the
  // account owns an active business organization. The copy tells the
  // operator to transfer the organization or delete it first.
  'authn.account_organization_owner',
  // The tenant admission layer's SSO step-up refusal: a local factor cannot
  // clear it, and the dialog shows the enterprise sign-in exit.
  'authn.sso_required',
  // The tenant admission layer's multi-factor refusal: the organization
  // requires a second factor, and a fresh sign-in is what satisfies it.
  'authn.insufficient_user_authentication',
  // authn's highest-privilege refused writes: the 403 of an initiator with no
  // non-SMS factor enabled for the configured period. A stronger
  // authentication cannot clear it, so it renders a waiting note rather than
  // opening the step-up dialog.
  'authn.factor_not_seasoned',
  // authn flow answers the step-up dialog can meet: a refused credential,
  // a flow the server no longer holds, throttling, an unavailable service
  // and a send that presented no valid single-use human proof. Each renders
  // its own text instead of the generic fallback.
  'authn.login_failed',
  'authn.flow_invalid',
  'authn.throttled',
  'authn.unavailable',
  'authn.human_verification_required',
  // The platform-level refusal the ratelimit layer answers a throttled
  // request with; the step-up flow's own start and answers can meet it.
  'ratelimit.throttled',
  // audit admin: the 400 a malformed query is answered with.
  'audit.bad_request',
  // settings admin and the tenant settings surface: a 400 shape refusal, the
  // 422 a write whose value is outside the declared item's domain is answered
  // with, the 403 the host's settings write seam answers a write into a
  // protected item with, the 403 an item declared read-only is answered with,
  // the 409 a write turning off a platform-enforced policy is answered with,
  // and the 422 a masked value written back is answered with.
  'settings.bad_request',
  'settings.invalid_value',
  'settings.protected',
  'settings.read_only',
  'settings.tenant_admission_enforced',
  'settings.masked_value',
  // sms admin: a 400 shape refusal, the 409 a name collision is answered
  // with, and the 409 a name backed by a configuration template is
  // answered with.
  'sms.bad_request',
  'sms.conflict',
  'sms.config_template',
] as const

/**
 * The admin-ui error-text resolver over the whitelist above, plus the
 * bare-HTTP family: a listed code resolves to its own errors-section leaf,
 * a client.http.<status> answer to the parameterized errors.client.http
 * leaf (so a 405, 401 and 500 render distinct, status-carrying lines
 * rather than one generic fallback), and anything else to the shared
 * unknown fallback. Reads the current language reactively (the returned
 * function is recreated when the language changes).
 */
/** The leaves that name a throttled answer's wait, and the codes carrying
 * one. */
const WAIT_KEY = 'throttle.retryAfter'
const WAIT_LONG_KEY = 'throttle.retryAfterLong'
const WAIT_CODES = ['authn.throttled', 'ratelimit.throttled'] as const

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
    // A write refused with a bare 403 or 401 (no readable problem envelope)
    // gets copy that says what to do: a permission refusal and an ended
    // session are actionable, while the generic status line ("the server
    // answered HTTP 403") would only restate the status.
    if (http[1] === '403') {
      return t('errors.client.httpForbidden')
    }
    if (http[1] === '401') {
      return t('errors.client.httpUnauthorized')
    }
    return t('errors.client.http', { status: http[1] ?? '' })
  }
}
