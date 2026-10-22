/**
 * The GET /api/v1/auth/me body the registration-verification surfaces read.
 *
 * A profile carries the `verification` triple `{state, email, since}`; every
 * field the profile document has is present, so a suite that scripts only the
 * verification state still answers a whole document.
 */

import type { AuthnProfileProfile } from '../app-api/index.js'

/** A profile whose self-registration is in `state`, naming `email`. */
export function profileWithVerification(
  state: string,
  email: string,
): AuthnProfileProfile {
  return {
    account_id: 'account-1',
    kind: 'user',
    display_name: 'Test User',
    mfa_required: false,
    password_set: true,
    created_at: '2026-01-01T00:00:00Z',
    attributes: {},
    locale: 'en-US',
    time_zone: 'UTC',
    identifiers: [],
    links: [],
    verification: { state, email, since: '' },
  }
}
