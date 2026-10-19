/**
 * The wire shapes the account surfaces render, and the narrowing the
 * generated queries select through.
 *
 * The authn OpenAPI fragments now declare their 2xx response schemas, so
 * orval emits the response models and this package no longer owns them:
 * each name below is an alias to the generated type of the operation the
 * surface reads (GET /api/v1/auth/me, GET /api/v1/auth/sessions, GET /api/v1/auth/factors).
 *
 * The narrowing functions stay because they carry runtime behaviour the
 * generated surface cannot: a narrowing maps an absent body to an empty
 * object, so a 2xx whose body omitted a collection reads as `undefined`
 * -- the caller's error variant -- and never as an empty list ("no
 * sessions" is only ever claimed for an answer that genuinely listed
 * none). They are shape assertions, not guards: nothing here throws,
 * because this package does not depend on @speed/api-client and cannot
 * mint its `client.protocol` ApiError (an unreadable answer surfaces as
 * the error variant, whose text is the errors.unknown fallback for a
 * non-ApiError).
 */

import type {
  AuthnMfaFactor,
  AuthnMfaFactorList,
  AuthnProfileIdentifier,
  AuthnProfileLink,
  AuthnProfileProfile,
  AuthnSessionItem,
  AuthnSessionList,
} from '@speed/api-sdk'

/** One verified identifier of an account (email, phone): the generated
 * identifier row of GET /api/v1/auth/me. */
export type AccountIdentifier = AuthnProfileIdentifier

/** One external binding of an account (a social or enterprise provider):
 * the generated link row of GET /api/v1/auth/me. */
export type AccountLink = AuthnProfileLink

/** The caller's own profile, the body of GET /api/v1/auth/me. */
export type AccountProfile = AuthnProfileProfile

/** One session in GET /api/v1/auth/sessions. The listing carries only live
 * sessions, so a row is never revoked or expired. */
export type AccountSession = AuthnSessionItem

/** The body of GET /api/v1/auth/sessions. */
export type SessionList = AuthnSessionList

/** One registered factor in GET /api/v1/auth/factors. */
export type AccountFactor = AuthnMfaFactor

/** The body of GET /api/v1/auth/factors. */
export type FactorList = AuthnMfaFactorList

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
