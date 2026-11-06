/**
 * The client-side validation the server remains authoritative over: the
 * alias slug and reserved names, the required display name, the
 * description bound, and the platform-only fields. The self-service form
 * ignores the identifier, contact address and website even when a host
 * seeds them.
 */

import { describe, expect, it } from 'vitest'
import {
  EMPTY_ORGANIZATION_CREATE_VALUES,
  aliasViolation,
  isAliasShape,
  isReservedAlias,
  normalizeOrganizationCreateValues,
  validateOrganizationCreateValues,
} from './validation.js'

function withValues(
  overrides: Partial<typeof EMPTY_ORGANIZATION_CREATE_VALUES>,
) {
  return { ...EMPTY_ORGANIZATION_CREATE_VALUES, ...overrides }
}

describe('the alias rule', () => {
  it('accepts the server slug shape and rejects everything outside it', () => {
    for (const alias of ['acme', 'acme-labs', 'a1b', 'a'.repeat(63)]) {
      expect(isAliasShape(alias), alias).toBe(true)
    }
    for (const alias of [
      'ab',
      'a'.repeat(64),
      '-acme',
      'Acme',
      'acme_labs',
      'acme labs',
      'acme.labs',
    ]) {
      expect(isAliasShape(alias), alias).toBe(false)
    }
  })

  it('refuses the built-in reserved names and the personal prefix', () => {
    for (const alias of ['admin', 'api', 'system', 'www']) {
      expect(isReservedAlias(alias), alias).toBe(true)
    }
    expect(isReservedAlias('personal-alice')).toBe(true)
    expect(isReservedAlias('acme')).toBe(false)
  })

  it('reports the first violation an alias carries', () => {
    expect(aliasViolation('')).toBe('required')
    expect(aliasViolation('  ')).toBe('required')
    expect(aliasViolation('Acme')).toBe('aliasShape')
    expect(aliasViolation('admin')).toBe('aliasReserved')
    expect(aliasViolation('acme')).toBeUndefined()
  })
})

describe('validateOrganizationCreateValues', () => {
  it('requires the display name after trimming', () => {
    const violations = validateOrganizationCreateValues(
      withValues({ alias: 'acme', displayName: '   ' }),
      'self-service',
    )
    expect(violations).toEqual({ displayName: 'required' })
  })

  it('bounds the description in runes, not UTF-16 units', () => {
    const atLimit = validateOrganizationCreateValues(
      withValues({
        alias: 'acme',
        displayName: 'Acme',
        description: '😀'.repeat(500),
      }),
      'self-service',
    )
    expect(atLimit.description).toBeUndefined()

    const overLimit = validateOrganizationCreateValues(
      withValues({
        alias: 'acme',
        displayName: 'Acme',
        description: '😀'.repeat(501),
      }),
      'self-service',
    )
    expect(overLimit.description).toBe('descriptionTooLong')
  })

  it('checks the platform fields, and only in platform mode', () => {
    const values = withValues({
      alias: 'acme',
      displayName: 'Acme',
      id: '',
      contactEmail: 'not-an-address',
      website: 'ftp://example.com',
    })
    expect(validateOrganizationCreateValues(values, 'platform')).toMatchObject({
      id: 'required',
      contactEmail: 'emailShape',
      website: 'websiteShape',
    })
    // Self-service renders none of those fields, so their values are not
    // the operator's edit and must not block creation.
    expect(validateOrganizationCreateValues(values, 'self-service')).toEqual({})
  })

  it('accepts an absolute http/https website and a bare address', () => {
    const violations = validateOrganizationCreateValues(
      withValues({
        alias: 'acme',
        displayName: 'Acme',
        id: 'org-acme',
        contactEmail: 'team@example.com',
        website: 'https://example.com/about',
      }),
      'platform',
    )
    expect(violations).toEqual({})
  })

  it('rejects a website with userinfo and a relative address', () => {
    for (const website of [
      'https://user:secret@example.com',
      '/about',
      'example.com',
    ]) {
      const violations = validateOrganizationCreateValues(
        withValues({ alias: 'acme', displayName: 'Acme', id: 'org-acme', website }),
        'platform',
      )
      expect(violations.website, website).toBe('websiteShape')
    }
  })

  it('normalizes every field before the request', () => {
    expect(
      normalizeOrganizationCreateValues({
        id: ' org-acme ',
        alias: ' acme ',
        displayName: ' Acme Ltd ',
        description: ' A team ',
        contactEmail: ' team@example.com ',
        website: ' https://example.com ',
      }),
    ).toEqual({
      id: 'org-acme',
      alias: 'acme',
      displayName: 'Acme Ltd',
      description: 'A team',
      contactEmail: 'team@example.com',
      website: 'https://example.com',
    })
  })
})
