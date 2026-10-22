/**
 * The two account-readiness predicates the host keys its notices on.
 *
 * `setupIncomplete` answers the username-and-password question; it is what
 * the once-only home notice keys on. `contactVerificationPending` answers the
 * registration-verification question and is deliberately independent: a
 * password registration already holds a login name and a password, so setup
 * is complete while verification is still pending, and the verification
 * notice must still show.
 */

import { describe, expect, it } from 'vitest'
import {
  contactVerificationPending,
  setupIncomplete,
  type SetupProfile,
  type VerificationProfile,
} from './account-setup.js'

const PASSWORD = { password_set: true } as const

const USERNAME = {
  kind: 'username',
  verified_at: '2026-01-01T00:00:00Z',
} as const

const VERIFIED_EMAIL = {
  kind: 'email',
  verified_at: '2026-01-01T00:00:00Z',
} as const

const VERIFIED_PHONE = {
  kind: 'phone',
  verified_at: '2026-01-01T00:00:00Z',
} as const

describe('setupIncomplete', () => {
  it('needs both a login name and a password', () => {
    expect(
      setupIncomplete({ ...PASSWORD, identifiers: [USERNAME] }),
    ).toBe(false)
    expect(
      setupIncomplete({ ...PASSWORD, identifiers: [VERIFIED_EMAIL] }),
    ).toBe(false)
    expect(
      setupIncomplete({ password_set: false, identifiers: [USERNAME] }),
    ).toBe(true)
    expect(setupIncomplete({ ...PASSWORD, identifiers: [] })).toBe(true)
  })

  it('does not treat a verified phone as a login name', () => {
    expect(
      setupIncomplete({ ...PASSWORD, identifiers: [VERIFIED_PHONE] }),
    ).toBe(true)
  })
})

describe('contactVerificationPending', () => {
  it('answers true only for the pending state', () => {
    const pending: VerificationProfile = {
      verification: { state: 'pending' },
    }
    const verified: VerificationProfile = {
      verification: { state: 'verified' },
    }
    const none: VerificationProfile = { verification: { state: 'none' } }
    expect(contactVerificationPending(pending)).toBe(true)
    expect(contactVerificationPending(verified)).toBe(false)
    expect(contactVerificationPending(none)).toBe(false)
    expect(contactVerificationPending({})).toBe(false)
  })

  it('stays independent of setup readiness', () => {
    // A password registration: the login name and password are already there,
    // and the email address is not proved yet.
    const profile: SetupProfile & VerificationProfile = {
      ...PASSWORD,
      identifiers: [USERNAME],
      verification: { state: 'pending' },
    }
    expect(setupIncomplete(profile)).toBe(false)
    expect(contactVerificationPending(profile)).toBe(true)
  })
})
