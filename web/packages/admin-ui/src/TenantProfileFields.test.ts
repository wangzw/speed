/**
 * The organization profile's shared field rules.
 *
 * The form and both surfaces read one shape, one wire conversion and one
 * local bound set; these cases pin the fallback and the bounds the server
 * would otherwise be the first to refuse.
 */

import { describe, expect, it } from 'vitest'
import {
  displayNameOf,
  hasTenantProfileFieldErrors,
  profileFieldsOf,
  profileWriteOf,
  validateTenantProfileFields,
  TENANT_PROFILE_LIMITS,
} from './TenantProfileFields.js'

const EMPTY = {
  displayName: '',
  description: '',
  contactEmail: '',
  website: '',
} as const

describe('displayNameOf', () => {
  it('shows the display name when the profile names one', () => {
    expect(displayNameOf('Acme Incorporated', 'acme-inc')).toBe(
      'Acme Incorporated',
    )
  })

  it('falls back to the name when the display name is empty or blank', () => {
    expect(displayNameOf('', 'acme-inc')).toBe('acme-inc')
    expect(displayNameOf('   ', 'acme-inc')).toBe('acme-inc')
  })
})

describe('profileWire', () => {
  it('maps the form fields onto the write body', () => {
    expect(
      profileWriteOf({
        displayName: 'Acme',
        description: 'A company.',
        contactEmail: 'ops@acme.example',
        website: 'https://acme.example',
      }),
    ).toEqual({
      display_name: 'Acme',
      description: 'A company.',
      contact_email: 'ops@acme.example',
      website: 'https://acme.example',
    })
  })

  it('reads the fields from a view in either surface spelling', () => {
    expect(
      profileFieldsOf({
        displayName: 'Acme',
        description: '',
        contactEmail: '',
        website: '',
      }),
    ).toEqual({
      displayName: 'Acme',
      description: '',
      contactEmail: '',
      website: '',
    })
  })
})

describe('validateTenantProfileFields', () => {
  it('accepts every field empty', () => {
    expect(validateTenantProfileFields(EMPTY)).toEqual({})
  })

  it('accepts a valid profile', () => {
    expect(
      validateTenantProfileFields({
        displayName: 'Acme',
        description: 'A company.',
        contactEmail: 'ops@acme.example',
        website: 'https://acme.example/about',
      }),
    ).toEqual({})
  })

  it('refuses a display name beyond its rune limit', () => {
    const errors = validateTenantProfileFields({
      ...EMPTY,
      displayName: 'a'.repeat(TENANT_PROFILE_LIMITS.displayNameRunes + 1),
    })
    expect(errors.displayName).toBe('tooLong')
    expect(hasTenantProfileFieldErrors(errors)).toBe(true)
  })

  it('refuses a description beyond its rune limit', () => {
    const errors = validateTenantProfileFields({
      ...EMPTY,
      description: 'a'.repeat(TENANT_PROFILE_LIMITS.descriptionRunes + 1),
    })
    expect(errors.description).toBe('tooLong')
  })

  it('counts runes rather than UTF-16 units', () => {
    // 100 emoji are 100 code points but 200 UTF-16 units: the server counts
    // runes, so the client must too or it would refuse a value the server
    // accepts.
    expect(
      validateTenantProfileFields({
        ...EMPTY,
        displayName: '😀'.repeat(TENANT_PROFILE_LIMITS.displayNameRunes),
      }),
    ).toEqual({})
  })

  it('refuses an address that is not one bare email', () => {
    for (const contactEmail of ['not-an-email', 'a@b', 'a b@c.example', 'a@b@c.example']) {
      expect(
        validateTenantProfileFields({ ...EMPTY, contactEmail }).contactEmail,
      ).toBe('invalidEmail')
    }
  })

  it('refuses a website that is not an absolute http or https address', () => {
    for (const website of [
      'acme.example',
      'ftp://acme.example',
      'mailto:ops@acme.example',
      'https://user:pass@acme.example',
    ]) {
      expect(
        validateTenantProfileFields({ ...EMPTY, website }).website,
      ).toBe('invalidWebsite')
    }
    expect(
      validateTenantProfileFields({ ...EMPTY, website: 'http://acme.example' })
        .website,
    ).toBeUndefined()
  })
})
