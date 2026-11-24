
import { describe, expect, it } from 'vitest'
import {
  defaultPhoneCountry,
  formatNationalPhoneNumber,
  isPhoneCountry,
  nationalNumberDigits,
  parseInternationalPhoneNumber,
  phoneCountryCallingCode,
  phoneCountryName,
  toE164,
  validatePhoneNumber,
} from './phone-number.js'

describe('defaultPhoneCountry', () => {
  it('takes the region a browser locale names', () => {
    expect(defaultPhoneCountry('zh-CN')).toBe('CN')
    expect(defaultPhoneCountry('en-US')).toBe('US')
    expect(defaultPhoneCountry('de-DE')).toBe('DE')
  })

  it('maximizes a language-only tag to its region', () => {
    expect(defaultPhoneCountry('zh')).toBe('CN')
    expect(defaultPhoneCountry('en')).toBe('US')
  })

  it('falls back to US for an unusable tag', () => {
    expect(defaultPhoneCountry('not a tag')).toBe('US')
  })
})

describe('country data', () => {
  it('knows covered countries and their calling codes', () => {
    expect(isPhoneCountry('CN')).toBe(true)
    expect(isPhoneCountry('XX')).toBe(false)
    expect(phoneCountryCallingCode('CN')).toBe('86')
    expect(phoneCountryCallingCode('US')).toBe('1')
    expect(phoneCountryCallingCode('XX')).toBeNull()
  })

  it('names a country through Intl.DisplayNames', () => {
    expect(phoneCountryName('DE', 'de-DE')).toBe('Deutschland')
    expect(phoneCountryName('DE', 'en-US')).toBe('Germany')
    expect(phoneCountryName('US', 'en-US')).toBe('United States')
  })
})

describe('the edited parts', () => {
  it('keeps only the digits of a typed value', () => {
    expect(nationalNumberDigits('138 0000 0001')).toBe('13800000001')
    expect(nationalNumberDigits('(415) 555-2671')).toBe('4155552671')
  })

  it('groups the local number with spaces while typing', () => {
    expect(formatNationalPhoneNumber('CN', '13800000001')).toBe('138 0000 0001')
    expect(formatNationalPhoneNumber('CN', '138000')).toBe('138 000')
    expect(formatNationalPhoneNumber('US', '4155552671')).toBe('415 555 2671')
  })

  it('parses a pasted international number into its parts', () => {
    expect(parseInternationalPhoneNumber('+8613800000001')).toEqual({
      country: 'CN',
      nationalNumber: '13800000001',
    })
    expect(parseInternationalPhoneNumber('+14155552671')).toEqual({
      country: 'US',
      nationalNumber: '4155552671',
    })
  })

  it('reports nothing while an international value names no country yet', () => {
    expect(parseInternationalPhoneNumber('+')).toBeNull()
    expect(parseInternationalPhoneNumber('+86')).toBeNull()
    expect(parseInternationalPhoneNumber('13800000001')).toBeNull()
  })
})

describe('validation and assembly', () => {
  it('assembles E.164 for more than one country', () => {
    expect(toE164('CN', '13800000001')).toBe('+8613800000001')
    expect(toE164('US', '4155552671')).toBe('+14155552671')
  })

  it('calls a too-short number incomplete', () => {
    expect(validatePhoneNumber('CN', '138000')).toBe('incomplete')
    expect(validatePhoneNumber('CN', '')).toBe('incomplete')
    expect(toE164('CN', '138000')).toBeNull()
  })

  it('calls an implausible number invalid', () => {
    expect(validatePhoneNumber('US', '0000000000')).toBe('invalid')
    expect(toE164('US', '0000000000')).toBeNull()
  })

  it('calls a legal-length number with an impossible number plan invalid', () => {
    expect(validatePhoneNumber('CN', '12345678901')).toBe('invalid')
    expect(validatePhoneNumber('CN', '4155552671')).toBe('invalid')
    expect(toE164('CN', '12345678901')).toBeNull()
  })

  it('accepts a valid number', () => {
    expect(validatePhoneNumber('CN', '13800000001')).toBeNull()
    expect(validatePhoneNumber('US', '4155552671')).toBeNull()
  })

  it('refuses a country the metadata does not cover', () => {
    expect(validatePhoneNumber('XX', '13800000001')).toBe('invalid')
    expect(toE164('XX', '13800000001')).toBeNull()
  })
})
