/**
 * The error-code text convention every error surface resolves through.
 *
 * Failures arrive as codes -- the API envelope's code, the api-client
 * transport codes, a classifier's collapse target -- and text resolves
 * from the code, never from an API's English fallback message (which
 * exists for log triage only). The convention: a listed code resolves
 * to its own leaf under the bundle's `errors` section
 * ('errors.<code>' -- the section the hand-maintained platform bundle and
 * the package catalogs share), and anything unlisted to `errors.unknown`.
 * A resolver built here can therefore never render a raw key, and a
 * missing translation never leaks another language's text.
 *
 * Two code families are part of the convention because every protected
 * surface can be answered with them: the session-lifecycle codes (a
 * session that dies mid-flight surfaces as one of these through the
 * refresh leg) and the transport-level codes of the @speed/api-client
 * contract. Each surface's whitelist composes these with the codes its
 * own operations reach and passes the union to the resolver factory.
 *
 * Two optional seams extend the convention without changing the default:
 *
 *  - `httpFamily` resolves the bare-HTTP family the api-client synthesizes
 *    (`client.http.<status>`) through one parameterized leaf, carrying the
 *    status, with optional per-status leaves that win when the status is
 *    listed (the actionable replacements a surface gives its most common
 *    refusals);
 *  - `fallthrough` lets a surface resolve any other code through the
 *    platform error bundle it registered as a fallback namespace, instead
 *    of collapsing onto `errors.unknown` -- the code resolves only when the
 *    caller confirms the bundle carries it, so a genuinely unknown code
 *    still renders the unknown fallback.
 */

/**
 * The session-lifecycle answers any signed-in surface can receive: a
 * refused refresh or a dead session surfaces as one of these, and a
 * host renders them for its own protected operations.
 */
export const SESSION_LIFECYCLE_ERROR_CODES = [
  'authn.session_not_found',
  'authn.session_revoked',
  'authn.token_expired',
  'authn.refresh_token_invalid',
  'authn.refresh_token_reused',
] as const

/** The transport-level failures of the @speed/api-client contract. */
export const CLIENT_TRANSPORT_ERROR_CODES = [
  'client.network',
  'client.timeout',
  'client.protocol',
] as const

/** The bundle section error codes resolve under, one dotted code per leaf. */
const ERROR_TEXT_SECTION = 'errors'

/** The leaf every unlisted code resolves to -- the fallback itself. */
const UNKNOWN_ERROR_TEXT_KEY = `${ERROR_TEXT_SECTION}.unknown`

/** The bare-HTTP answer the api-client synthesizes: client.http.<status>. */
const HTTP_ERROR_CODE = /^client\.http\.(\d{3})$/

/**
 * How a surface resolves the bare-HTTP family: `key` is the namespace's
 * parameterized leaf (its text carries the status), and `byStatus` names
 * statuses that resolve to their own leaf instead -- the actionable
 * replacements a surface gives its most common refusals.
 */
export interface HttpFamilyOptions {
  readonly key: string
  readonly byStatus?: Readonly<Record<string, string>>
}

/** Options extending the resolver; omitting them keeps the default. */
export interface ErrorTextResolverOptions {
  /** Resolve client.http.<status> through a parameterized leaf, not unknown. */
  readonly httpFamily?: HttpFamilyOptions
  /**
   * Resolve an unlisted code through errors.<code> when this answers true
   * (a surface whose fallback namespace carries the code), else unknown.
   */
  readonly fallthrough?: (code: string) => boolean
}

/**
 * The translation function a resolver composes: a key, plus the status
 * option the parameterized bare-HTTP leaf interpolates. A plain
 * `(key) => string` is assignable, so existing callers keep working.
 */
export type ErrorTextTranslate = (
  key: string,
  options?: { readonly status?: string },
) => string

/**
 * Builds a code-to-text resolver over one surface's whitelist: a listed
 * code resolves to its own leaf; a `client.http.<status>` code resolves
 * through `httpFamily` when configured; an unlisted code resolves through
 * `fallthrough` when it answers true; anything left resolves to the shared
 * unknown fallback. `t` is the surface namespace's translation function;
 * `knownCodes` is the whitelist membership.
 */
export function createErrorTextResolver(
  t: ErrorTextTranslate,
  knownCodes: ReadonlySet<string>,
  options: ErrorTextResolverOptions = {},
): (code: string) => string {
  const { httpFamily, fallthrough } = options
  return (code) => {
    if (knownCodes.has(code)) {
      return t(`${ERROR_TEXT_SECTION}.${code}`)
    }
    if (httpFamily !== undefined) {
      const status = HTTP_ERROR_CODE.exec(code)?.[1]
      if (status !== undefined) {
        const statusKey = httpFamily.byStatus?.[status]
        return statusKey === undefined
          ? t(httpFamily.key, { status })
          : t(statusKey)
      }
    }
    if (fallthrough?.(code) === true) {
      return t(`${ERROR_TEXT_SECTION}.${code}`)
    }
    return t(UNKNOWN_ERROR_TEXT_KEY)
  }
}
