/**
 * The registration-verification predicate the host keys its notice on.
 *
 * `contactVerificationPending` answers the verification question from
 * GET /api/v1/auth/me's `verification.state`; only `pending` answers true.
 */

import { describe, expect, it } from 'vitest'
import {
  contactVerificationPending,
  type VerificationProfile,
} from './contact-verification.js'

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
})
