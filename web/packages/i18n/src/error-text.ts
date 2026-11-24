/**
 * The error-code text convention: a listed code resolves to `errors.<code>`
 * and an unlisted one to `errors.unknown`. Optional seams add the
 * `client.http.*` family and a fallthrough for the platform bundle.
 */

/** The session-lifecycle answers any signed-in surface can receive. */
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

const ERROR_TEXT_SECTION = 'errors'

const UNKNOWN_ERROR_TEXT_KEY = `${ERROR_TEXT_SECTION}.unknown`

const HTTP_ERROR_CODE = /^client\.http\.(\d{3})$/

/** How a surface resolves the bare-HTTP family: `key` is the namespace's
 * parameterized leaf (its text carries the status), and `byStatus` names
 * statuses that resolve to their own leaf instead. */
export interface HttpFamilyOptions {
  readonly key: string
  readonly byStatus?: Readonly<Record<string, string>>
}

export interface ErrorTextWaitOptions {
  readonly key: string
  readonly longKey?: string
  readonly codes: readonly string[]
  readonly longFromSeconds?: number
}

/** The wait from which a countdown sentence names whole minutes instead of seconds. */
export const WAIT_LONG_FROM_SECONDS = 120

/** The remaining wait as a countdown sentence: seconds for a short wait, whole minutes from WAIT_LONG_FROM_SECONDS. */
export function waitCountdownText(
  t: ErrorTextTranslate,
  keys: { readonly short: string; readonly long?: string },
  seconds: number,
): string {
  if (seconds >= WAIT_LONG_FROM_SECONDS && keys.long !== undefined) {
    return t(keys.long, { minutes: Math.ceil(seconds / 60) })
  }
  return t(keys.short, { seconds })
}

/** Options extending the resolver; omitting them keeps the default. `httpFamily`
 * resolves `client.http.<status>` through its leaves, `wait` names a stated
 * wait, and `fallthrough` resolves an unlisted code through `errors.<code>`. */
export interface ErrorTextResolverOptions {
  readonly httpFamily?: HttpFamilyOptions
  readonly wait?: ErrorTextWaitOptions
  readonly fallthrough?: (code: string) => boolean
}

/** The interpolation options a resolver passes to its translation function:
 * the status, seconds or minutes a parameterized leaf interpolates. */
export interface ErrorTextTranslateOptions {
  readonly status?: string
  readonly seconds?: number
  readonly minutes?: number
}

/** The translation function a resolver composes. */
export interface ErrorTextTranslate {
  (key: string): string
  (key: string, options: ErrorTextTranslateOptions): string
}

/**
 * Builds a code-to-text resolver over one surface's whitelist: listed codes to
 * their own leaf, `client.http.<status>` through `httpFamily`, and anything
 * else to the unknown fallback or the fallthrough bundle.
 */
export function createErrorTextResolver(
  t: ErrorTextTranslate,
  knownCodes: ReadonlySet<string>,
  options: ErrorTextResolverOptions = {},
): (code: string, retryAfterSeconds?: number) => string {
  const { httpFamily, fallthrough, wait } = options
  const longFrom = wait?.longFromSeconds ?? WAIT_LONG_FROM_SECONDS
  return (code, retryAfterSeconds) => {
    if (knownCodes.has(code)) {
      if (
        wait !== undefined &&
        retryAfterSeconds !== undefined &&
        wait.codes.includes(code)
      ) {
        if (retryAfterSeconds >= longFrom && wait.longKey !== undefined) {
          return t(wait.longKey, {
            minutes: Math.ceil(retryAfterSeconds / 60),
          })
        }
        return t(wait.key, { seconds: retryAfterSeconds })
      }
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
