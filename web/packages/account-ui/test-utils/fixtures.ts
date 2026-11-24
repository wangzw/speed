/**
 * The wire-shaped test data the account suites share.
 */

export function profileBody(
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  return {
    account_id: 'user-1',
    kind: 'user',
    display_name: 'Ada',
    mfa_required: false,
    password_set: true,
    created_at: '2026-01-01T00:00:00Z',
    attributes: {},
    locale: '',
    time_zone: '',
    identifiers: [],
    links: [],
    verification: { state: 'none', email: '', since: null },
    ...overrides,
  }
}

export function identifier(
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  const kind = (overrides.kind as string | undefined) ?? 'email'
  const username = kind === 'username'
  const phone = kind === 'phone'
  return {
    id: 'ident-1',
    kind,
    value: 'ada@example.test',
    proven: !username,
    verified_at: '2026-01-01T00:00:00Z',
    verified_via: username ? 'self' : phone ? 'sms-code' : 'idp:google',
    sign_in: kind === 'email' || kind === 'username',
    contact: false,
    default_contact: false,
    ...overrides,
  }
}
