/**
 * The wire shapes the account surfaces render, and the narrowing the
 * generated queries select through.
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

export type AccountIdentifier = AuthnProfileIdentifier

export type AccountLink = AuthnProfileLink

export type AccountProfile = AuthnProfileProfile

/** The listing carries only live sessions, so a row is never revoked. */
export type AccountSession = AuthnSessionItem

export type SessionList = AuthnSessionList

export type AccountFactor = AuthnMfaFactor

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
