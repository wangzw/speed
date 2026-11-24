/**
 * Parsing for `otpauth://totp/...` enrolment URIs into their parts (issuer,
 * account, secret, algorithm, digits, period). Every refusal names the field
 * that could not be honoured.
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

/** Why a URI could not be parsed; each value names the single field that failed. */
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

const BASE32 = /^[A-Za-z2-7]+=*$/

const DECIMAL = /^\d+$/

function fail(error: OtpauthParseError): OtpauthParseResult {
  return { ok: false, error }
}

/**
 * Parses one `otpauth://totp/...` URI into the parts the enrolment panel
 * renders. Defaults match an authenticator (SHA1, six digits, 30s); an
 * out-of-range value is refused, not coerced.
 */
export function parseOtpauth(uri: string): OtpauthParseResult {
  let url: URL
  try {
    url = new URL(uri.trim())
  } catch {
    return fail('invalid-uri')
  }

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
 * Formats a base32 secret for hand entry in four-character groups, leaving out
 * padding; the authenticator ignores whitespace.
 */
export function formatSecretForDisplay(secret: string): string {
  const grouped = secret
    .trim()
    .replace(/=+$/, '')
    .match(/.{1,4}/g)
  return grouped === null ? '' : grouped.join(' ')
}

function nonEmpty(value: string): string | undefined {
  const trimmed = value.trim()
  return trimmed === '' ? undefined : trimmed
}

function parsePositiveInteger(value: string): number | null {
  if (!DECIMAL.test(value)) {
    return null
  }
  const parsed = Number(value)
  return parsed > 0 ? parsed : null
}
