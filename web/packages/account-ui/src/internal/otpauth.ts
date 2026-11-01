/**
 * Parsing for `otpauth://` enrolment URIs.
 *
 * The enrolment step hands the surface one `otpauth://totp/...` string.
 * A person cannot scan it from a desktop screen and often needs to type
 * the secret by hand, so the panel needs the parts -- the issuer and
 * account to label the entry, the secret to display, and the algorithm,
 * digit count and period an authenticator applies -- rather than the URI
 * as one opaque blob. This module turns the URI into exactly those parts
 * and refuses anything it cannot represent honestly.
 *
 * The accepted shape is the Key Uri Format:
 *
 *   otpauth://totp/LABEL?secret=...&issuer=...&algorithm=...&digits=...&period=...
 *
 * The scheme and the `totp` type are matched case-insensitively: the URL
 * parser folds the scheme, and a non-special scheme's host keeps its
 * spelling, so the type is folded before comparison. The label is
 * percent-decoded and split on its first colon into an optional issuer and
 * an account name, so an `Example%20Co:alice%40example.com` label reads as
 * the issuer `Example Co` and the account `alice@example.com`. A label
 * with no colon is the account alone.
 *
 * Failure is explicit: every refusal names the field that could not be
 * honoured, so the surface can say why rather than render half a panel.
 * `secret` is returned exactly as the URI carries it (base32, no
 * re-encoding) and the whole URI never leaves this module through the
 * result.
 */

/** The HMAC hash functions an authenticator accepts for TOTP. */
export type OtpauthAlgorithm = 'SHA1' | 'SHA256' | 'SHA512'

/** The code lengths TOTP defines in practice. */
export type OtpauthDigits = 6 | 7 | 8

/** The parts of an enrolment URI the surface renders. */
export type Otpauth = {
  /** The service the entry belongs to, when the URI names one. */
  issuer?: string
  /** The account name the entry is filed under, when the label carries one. */
  account?: string
  /** The shared secret exactly as the URI spells it (base32). */
  secret: string
  /** The HMAC hash the authenticator must apply. */
  algorithm: OtpauthAlgorithm
  /** How many digits a generated code carries. */
  digits: OtpauthDigits
  /** How long a code stays valid, in seconds. */
  period: number
}

/**
 * Why a URI could not be parsed. Each value names the single field that
 * could not be honoured, so the surface says what is wrong instead of
 * showing a partial panel.
 */
export type OtpauthParseError =
  | 'invalid-uri'
  | 'unsupported-scheme'
  | 'unsupported-type'
  | 'invalid-label'
  | 'missing-secret'
  | 'invalid-secret'
  | 'unsupported-algorithm'
  | 'invalid-digits'
  | 'invalid-period'

/** The outcome of {@link parseOtpauth}: the parts, or the field that failed. */
export type OtpauthParseResult =
  | { ok: true; otpauth: Otpauth }
  | { ok: false; error: OtpauthParseError }

const DEFAULT_ALGORITHM: OtpauthAlgorithm = 'SHA1'
const DEFAULT_DIGITS: OtpauthDigits = 6
const DEFAULT_PERIOD = 30

const ALGORITHMS: ReadonlySet<string> = new Set(['SHA1', 'SHA256', 'SHA512'])

/** Base32 alphabet with optional trailing padding. Case is preserved on
 * the way out: the URI's spelling is what is displayed and typed. */
const BASE32 = /^[A-Za-z2-7]+=*$/

const DECIMAL = /^\d+$/

function fail(error: OtpauthParseError): OtpauthParseResult {
  return { ok: false, error }
}

/**
 * Parses one `otpauth://totp/...` URI into the parts the enrolment panel
 * renders.
 *
 * The defaults are the ones an authenticator applies when the URI omits a
 * parameter: SHA1, six digits, a 30-second period. A value outside the
 * ranges an authenticator understands is refused rather than coerced --
 * silently rounding `digits=5` to six would show a code the server never
 * accepts. When the label carries an issuer it wins over the `issuer`
 * query parameter, which is the fallback for URIs written without the
 * prefixed label.
 */
export function parseOtpauth(uri: string): OtpauthParseResult {
  let url: URL
  try {
    url = new URL(uri.trim())
  } catch {
    return fail('invalid-uri')
  }

  // The URL parser lowercases the scheme. A non-special scheme's host is
  // an opaque host and keeps its spelling, so the type is folded here.
  if (url.protocol !== 'otpauth:') {
    return fail('unsupported-scheme')
  }
  if (url.hostname.toLowerCase() !== 'totp') {
    return fail('unsupported-type')
  }

  let label: string
  try {
    label = decodeURIComponent(url.pathname.replace(/^\/+/, ''))
  } catch {
    return fail('invalid-label')
  }

  const colon = label.indexOf(':')
  const labelIssuer = colon >= 0 ? nonEmpty(label.slice(0, colon)) : undefined
  const account = nonEmpty(colon >= 0 ? label.slice(colon + 1) : label)

  const params = url.searchParams

  const secret = params.get('secret')
  if (secret === null || secret === '') {
    return fail('missing-secret')
  }
  if (!BASE32.test(secret)) {
    return fail('invalid-secret')
  }

  const queryIssuer = nonEmpty(params.get('issuer') ?? '')
  const issuer = labelIssuer ?? queryIssuer

  const algorithm = (params.get('algorithm') ?? DEFAULT_ALGORITHM).toUpperCase()
  if (!ALGORITHMS.has(algorithm)) {
    return fail('unsupported-algorithm')
  }

  const rawDigits = params.get('digits')
  const digits = rawDigits === null ? DEFAULT_DIGITS : parsePositiveInteger(rawDigits)
  if (digits === null || (digits !== 6 && digits !== 7 && digits !== 8)) {
    return fail('invalid-digits')
  }

  const rawPeriod = params.get('period')
  const period = rawPeriod === null ? DEFAULT_PERIOD : parsePositiveInteger(rawPeriod)
  if (period === null) {
    return fail('invalid-period')
  }

  return {
    ok: true,
    otpauth: {
      ...(issuer === undefined ? {} : { issuer }),
      ...(account === undefined ? {} : { account }),
      secret,
      algorithm: algorithm as OtpauthAlgorithm,
      digits,
      period,
    },
  }
}

/**
 * Formats a base32 secret for hand entry: four-character groups separated
 * by spaces, so a person copying it is less likely to lose their place.
 * The authenticator ignores whitespace, so the displayed form is the one
 * that is typed. Padding is left out -- it carries no information and is
 * one more character to mistype.
 */
export function formatSecretForDisplay(secret: string): string {
  const grouped = secret
    .trim()
    .replace(/=+$/, '')
    .match(/.{1,4}/g)
  return grouped === null ? '' : grouped.join(' ')
}

/** A trimmed non-empty string, or undefined for a blank one. */
function nonEmpty(value: string): string | undefined {
  const trimmed = value.trim()
  return trimmed === '' ? undefined : trimmed
}

/**
 * A positive decimal integer, or null when the value does not spell one.
 * Callers reach here only for a parameter that is present; an absent
 * parameter takes the field's default before this is called.
 */
function parsePositiveInteger(value: string): number | null {
  if (!DECIMAL.test(value)) {
    return null
  }
  const parsed = Number(value)
  return parsed > 0 ? parsed : null
}
