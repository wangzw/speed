/**
 * The wire shapes the account surfaces render, and the narrowing the
 * generated queries select through.
 *
 * The authn OpenAPI fragments declare no response schema, so orval types
 * every generated return as `void` (see @speed/api-sdk's runtime.ts doc:
 * the seam erases the response type and document existence is the
 * consumer's business). This package therefore owns the shapes it
 * renders, field by field against the server's own views
 * (`pkg/authn/profile/views.go`, `pkg/authn/session/routes.go`,
 * `pkg/authn/mfa/routes.go`), and selects each generated read through
 * the matching narrowing below.
 *
 * Every field is optional on purpose. A narrowing is a shape assertion,
 * not a guard: it maps an absent body to an empty object, so a 2xx whose
 * body omitted a collection reads as `undefined` -- the caller's error
 * variant -- and never as an empty list ("no sessions" is only ever
 * claimed for an answer that genuinely listed none). The consumers test
 * each field for presence where the server may omit it; nothing here
 * throws, because this package does not depend on @speed/api-client and
 * cannot mint its `client.protocol` ApiError (an unreadable answer
 * surfaces as the error variant, whose text is the errors.unknown
 * fallback for a non-ApiError).
 */

/** One verified identifier of an account (email, phone). */
export interface AccountIdentifier {
  readonly id?: string
  /** The identifier kind (email, phone, ...). */
  readonly kind?: string
  /** The identifier value. */
  readonly value?: string
  /** When it was verified, RFC 3339. */
  readonly verified_at?: string
  /** How it was verified. */
  readonly verified_via?: string
}

/** One external binding of an account (a social or enterprise provider). */
export interface AccountLink {
  readonly id?: string
  /** The provider name. */
  readonly provider?: string
  /** The email the provider asserted, when it did. */
  readonly email?: string
  /** When the binding was created, RFC 3339. */
  readonly created_at?: string
  /** When the binding was last used to sign in, RFC 3339. */
  readonly last_login_at?: string
}

/** The caller's own profile, the body of GET /auth/me. */
export interface AccountProfile {
  readonly account_id?: string
  readonly kind?: string
  readonly display_name?: string
  readonly mfa_required?: boolean
  readonly password_set?: boolean
  readonly created_at?: string
  readonly attributes?: Readonly<Record<string, string>>
  /** The account's preferred locale; empty means "not chosen". */
  readonly locale?: string
  /** The account's preferred time zone; empty means "not chosen". */
  readonly time_zone?: string
  readonly identifiers?: readonly AccountIdentifier[]
  readonly links?: readonly AccountLink[]
}

/** One session in GET /auth/sessions. The listing carries only live
 * sessions, so a row is never revoked or expired. */
export interface AccountSession {
  readonly id?: string
  /** The session kind (browser, token, service). */
  readonly kind?: string
  readonly created_at?: string
  readonly last_seen_at?: string
  readonly expires_at?: string
  readonly client_ip?: string
  readonly user_agent?: string
  /** Whether this row is the session the request's own token belongs to. */
  readonly current?: boolean
}

/** The body of GET /auth/sessions. */
export interface SessionList {
  readonly sessions?: readonly AccountSession[]
}

/** One registered factor in GET /auth/factors. */
export interface AccountFactor {
  readonly id?: string
  /** The factor kind (totp, webauthn, sms). */
  readonly kind?: string
  /** A display label the server supplies. */
  readonly label?: string
  /** The factor status (active, pending, disabled). */
  readonly status?: string
  readonly created_at?: string
  readonly activated_at?: string
  readonly last_used_at?: string
}

/** The body of GET /auth/factors. */
export interface FactorList {
  readonly factors?: readonly AccountFactor[]
}

/** Narrows a profile read to its shape; an absent body reads as empty. */
export function asProfile(raw: unknown): AccountProfile {
  return (typeof raw === 'object' && raw !== null ? raw : {}) as AccountProfile
}

/** Narrows a sessions read to its shape; an absent body reads as empty. */
export function asSessionList(raw: unknown): SessionList {
  return (typeof raw === 'object' && raw !== null ? raw : {}) as SessionList
}

/** Narrows a factors read to its shape; an absent body reads as empty. */
export function asFactorList(raw: unknown): FactorList {
  return (typeof raw === 'object' && raw !== null ? raw : {}) as FactorList
}
