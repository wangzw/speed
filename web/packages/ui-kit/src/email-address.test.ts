/**
 * The email-address rule: the server's normalization rule restated, so a
 * refused address is refused for the same reason the server would give.
 * The cases pin each clause -- trimming, printable ASCII, exactly one @
 * with a part on each side, and the 254-byte bound -- in both directions.
 */

import { describe, expect, it } from 'vitest'
import {
  EMAIL_ADDRESS_MAX_BYTES,
  parseEmailAddress,
} from './email-address.js'

describe('parseEmailAddress', () => {
  it('returns the trimmed address for a well-formed one', () => {
    expect(parseEmailAddress('ada@example.com')).toBe('ada@example.com')
    expect(parseEmailAddress('  ada@example.com  ')).toBe('ada@example.com')
    expect(parseEmailAddress('Ada.Lovelace+tag@example.co.uk')).toBe(
      'Ada.Lovelace+tag@example.co.uk',
    )
  })

  it.each([
    ['an empty value', ''],
    ['whitespace only', '   '],
    ['no @', 'ada.example.com'],
    ['nothing before the @', '@example.com'],
    ['nothing after the @', 'ada@'],
    ['two @ signs', 'ada@example@com'],
    ['a space inside', 'ada lovelace@example.com'],
    ['a tab inside', 'ada\t@example.com'],
    ['a non-ASCII byte', 'adá@example.com'],
    ['a control byte', 'ada\u0001@example.com'],
  ])('refuses %s', (_case, value) => {
    expect(parseEmailAddress(value)).toBeNull()
  })

  it('bounds the address at 254 bytes', () => {
    const longest = `${'a'.repeat(EMAIL_ADDRESS_MAX_BYTES - '@x.io'.length)}@x.io`
    expect(new TextEncoder().encode(longest)).toHaveLength(
      EMAIL_ADDRESS_MAX_BYTES,
    )
    expect(parseEmailAddress(longest)).toBe(longest)
    expect(parseEmailAddress(`a${longest}`)).toBeNull()
  })
})
