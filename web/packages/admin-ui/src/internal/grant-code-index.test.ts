/**
 * The (resource, action) -> permission-code lookup.
 *
 * The pairs and codes below are the real ones: the six governed permissions
 * pkg/authz/policy marks TenantGoverned, their translation rows from
 * pkg/authz/internal/translation, and the platform-declared codes whose
 * object names share a suffix with a governed one. The lookup has to keep
 * the two apart, because the panel refuses a bind it cannot prove.
 */

import { describe, expect, it } from 'vitest'
import { buildGrantCodeIndex } from './grant-code-index.js'
import type { AdminPermission } from '../operations.js'

function catalog(...codes: readonly string[]): readonly AdminPermission[] {
  return codes.map((permission) => ({ permission, description: '' }))
}

/** The governed set: exactly the codes a tenant administrator may hold. */
const GOVERNED = catalog(
  'authz.tenant.role.read',
  'authz.tenant.role.manage',
  'authn.membership.manage',
  'authn.idp.manage',
  'settings.tenant.read',
  'settings.tenant.write',
)

/**
 * The platform-side catalog: the governed codes beside the unqualified codes
 * sharing their object name, plus the rows whose object name is not a suffix
 * of the code at all.
 */
const DECLARED = catalog(
  'authz.tenant.role.read',
  'authz.tenant.role.manage',
  'authz.role.read',
  'authz.role.manage',
  'settings.tenant.read',
  'settings.tenant.write',
  'settings.read',
  'settings.write',
  'authn.membership.manage',
  'authn.idp.manage',
  'authn.account.create',
  'authn.account.read',
  'authn.mfa.reset',
  'audit.read',
)

describe('buildGrantCodeIndex', () => {
  it('maps every governed grant pair back to its code', () => {
    const index = buildGrantCodeIndex(GOVERNED)

    expect(index('tenant_role', 'read')).toBe('authz.tenant.role.read')
    expect(index('tenant_role', 'manage')).toBe('authz.tenant.role.manage')
    expect(index('tenant_settings', 'read')).toBe('settings.tenant.read')
    expect(index('tenant_settings', 'write')).toBe('settings.tenant.write')
    expect(index('membership', 'manage')).toBe('authn.membership.manage')
    expect(index('idp', 'manage')).toBe('authn.idp.manage')
  })

  it('keeps an unqualified object apart from the tenant-qualified one', () => {
    const index = buildGrantCodeIndex(DECLARED)

    expect(index('role', 'read')).toBe('authz.role.read')
    expect(index('role', 'manage')).toBe('authz.role.manage')
    expect(index('tenant_role', 'read')).toBe('authz.tenant.role.read')
    expect(index('settings', 'read')).toBe('settings.read')
    expect(index('settings', 'write')).toBe('settings.write')
    expect(index('tenant_settings', 'read')).toBe('settings.tenant.read')
  })

  it('matches an action the catalog declares once when the object is not a suffix', () => {
    expect(buildGrantCodeIndex(DECLARED)('factor', 'reset')).toBe(
      'authn.mfa.reset',
    )
  })

  it('resolves no pair the catalog does not name', () => {
    expect(buildGrantCodeIndex(DECLARED)('deal', 'publish')).toBeNull()
  })

  it('stays null when two equally qualified codes match the same pair', () => {
    const index = buildGrantCodeIndex(catalog('a.role.read', 'b.role.read'))

    expect(index('role', 'read')).toBeNull()
  })
})
