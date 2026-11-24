import type { AuthnProfileProfile } from '../app-api/index.js'

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
    color_mode: '',
    locale: 'en-US',
    time_zone: 'UTC',
    identifiers: [],
    links: [],
    verification: { state, email, since: null },
  }
}
