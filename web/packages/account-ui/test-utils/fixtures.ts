/**
 * fixtures.ts -- the wire-shaped test data the account suites share.
 *
 * The GET /api/v1/auth/me body is read by several surfaces (preferences,
 * profile, password, identifiers and the composed page), so its shape is
 * built once here rather than re-spelled per suite: `profileBody` returns
 * the full profile document with the fields a read carries, and an
 * override replaces exactly the fields a test cares about.
 */

/** The GET /api/v1/auth/me body, every documented field present; `overrides`
 * replaces the named fields (a bare profile is the default). */
export function profileBody(
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  return {
    account_id: 'user-1',
    kind: 'person',
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

/** One identifier row of the profile's `identifiers`, in the fragment's
 * own shape; the default row was proved by the provider, so `proven` is true.
 * An email is a sign-in way under the old behaviour's write, which is the
 * state most rows in these suites exercise; a phone starts on neither axis. */
export function identifier(
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  const kind = (overrides.kind as string | undefined) ?? 'email'
  return {
    id: 'ident-1',
    kind,
    value: 'ada@example.test',
    proven: true,
    verified_at: '2026-01-01T00:00:00Z',
    verified_via: 'idp:google',
    sign_in: kind === 'email' || kind === 'username',
    contact: false,
    default_contact: false,
    ...overrides,
  }
}
