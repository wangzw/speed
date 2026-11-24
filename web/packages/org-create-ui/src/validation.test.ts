import { describe, expect, it } from 'vitest'
import {
  EMPTY_ORGANIZATION_CREATE_VALUES,
  nameViolation,
  isNameShape,
  isReservedName,
  normalizeOrganizationCreateValues,
  validateOrganizationCreateValues,
} from './validation.js'

function withValues(
  overrides: Partial<typeof EMPTY_ORGANIZATION_CREATE_VALUES>,
) {
  return { ...EMPTY_ORGANIZATION_CREATE_VALUES, ...overrides }
}

describe('the name rule', () => {
  it('accepts the server slug shape and rejects everything outside it', () => {
    for (const name of ['acme', 'acme-labs', 'a1b', 'a'.repeat(63)]) {
      expect(isNameShape(name), name).toBe(true)
    }
    for (const name of [
      'ab',
      'a'.repeat(64),
      '-acme',
      'Acme',
      'acme_labs',
      'acme labs',
      'acme.labs',
    ]) {
      expect(isNameShape(name), name).toBe(false)
    }
  })

  it('refuses the built-in reserved names and the personal prefix', () => {
    for (const name of ['admin', 'api', 'system', 'www']) {
      expect(isReservedName(name), name).toBe(true)
    }
    expect(isReservedName('personal-alice')).toBe(true)
    expect(isReservedName('acme')).toBe(false)
  })

  it('reports the first violation a name carries', () => {
    expect(nameViolation('')).toBe('required')
    expect(nameViolation('  ')).toBe('required')
    expect(nameViolation('Acme')).toBe('nameShape')
    expect(nameViolation('admin')).toBe('nameReserved')
    expect(nameViolation('acme')).toBeUndefined()
  })
})

describe('validateOrganizationCreateValues', () => {
  it('requires the display name after trimming', () => {
    const violations = validateOrganizationCreateValues(
      withValues({ name: 'acme', displayName: '   ' }),
      'self-service',
    )
    expect(violations).toEqual({ displayName: 'required' })
  })

  it('bounds the display name in runes, on both surfaces', () => {
    for (const mode of ['platform', 'self-service'] as const) {
      const atLimit = validateOrganizationCreateValues(
        withValues({
          name: 'acme',
          displayName: '😀'.repeat(100),
        }),
        mode,
      )
      expect(atLimit.displayName, mode).toBeUndefined()

      const overLimit = validateOrganizationCreateValues(
        withValues({
          name: 'acme',
          displayName: '😀'.repeat(101),
        }),
        mode,
      )
      expect(overLimit.displayName, mode).toBe('displayNameTooLong')
    }
  })

  it('bounds the description in runes, not UTF-16 units', () => {
    const atLimit = validateOrganizationCreateValues(
      withValues({
        name: 'acme',
        displayName: 'Acme',
        description: '😀'.repeat(500),
      }),
      'self-service',
    )
    expect(atLimit.description).toBeUndefined()

    const overLimit = validateOrganizationCreateValues(
      withValues({
        name: 'acme',
        displayName: 'Acme',
        description: '😀'.repeat(501),
      }),
      'self-service',
    )
    expect(overLimit.description).toBe('descriptionTooLong')
  })

  it('checks the platform fields, and only in platform mode', () => {
    const values = withValues({
      name: 'acme',
      displayName: 'Acme',
      contactEmail: 'not-an-address',
      website: 'ftp://example.com',
    })
    expect(validateOrganizationCreateValues(values, 'platform')).toMatchObject({
      contactEmail: 'emailShape',
      website: 'websiteShape',
    })
    expect(validateOrganizationCreateValues(values, 'self-service')).toEqual({})
  })

  it('accepts an absolute http/https website and a bare address', () => {
    const violations = validateOrganizationCreateValues(
      withValues({
        name: 'acme',
        displayName: 'Acme',
        contactEmail: 'team@example.com',
        website: 'https://example.com/about',
      }),
      'platform',
    )
    expect(violations).toEqual({})
  })

  it('bounds the website in bytes, not UTF-16 units', () => {
    const prefix = 'https://example.com/'
    const atLimit = validateOrganizationCreateValues(
      withValues({
        name: 'acme',
        displayName: 'Acme',
        website: `${prefix}${'中'.repeat(676)}`,
      }),
      'platform',
    )
    expect(atLimit.website).toBeUndefined()

    const overLimit = validateOrganizationCreateValues(
      withValues({
        name: 'acme',
        displayName: 'Acme',
        website: `${prefix}${'中'.repeat(677)}`,
      }),
      'platform',
    )
    expect(overLimit.website).toBe('websiteTooLong')
  })

  it('rejects a website with userinfo and a relative address', () => {
    for (const website of [
      'https://user:secret@example.com',
      '/about',
      'example.com',
    ]) {
      const violations = validateOrganizationCreateValues(
        withValues({ name: 'acme', displayName: 'Acme', website }),
        'platform',
      )
      expect(violations.website, website).toBe('websiteShape')
    }
  })

  it('normalizes every field before the request', () => {
    expect(
      normalizeOrganizationCreateValues({
        name: ' acme ',
        displayName: ' Acme Ltd ',
        description: ' A team ',
        contactEmail: ' team@example.com ',
        website: ' https://example.com ',
      }),
    ).toEqual({
      name: 'acme',
      displayName: 'Acme Ltd',
      description: 'A team',
      contactEmail: 'team@example.com',
      website: 'https://example.com',
    })
  })
})
