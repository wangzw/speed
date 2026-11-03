/**
 * The zero-instant sentinel: the views' "never" value is recognised
 * without a timezone conversion, and an absent field reads the same way.
 * Timestamp formatting lives in @speed/i18n and is tested there.
 */

import { describe, expect, it } from 'vitest'
import { isZeroInstant } from './datetime.js'

describe('isZeroInstant', () => {
  it('recognizes the zero instant and the absent value', () => {
    expect(isZeroInstant('0001-01-01T00:00:00Z')).toBe(true)
    expect(isZeroInstant('')).toBe(true)
    expect(isZeroInstant(undefined)).toBe(true)
  })

  it('treats a real instant as not zero', () => {
    expect(isZeroInstant('2026-10-15T12:00:00Z')).toBe(false)
  })
})
