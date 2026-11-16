/**
 * The absent-instant sentinel: the views' "never" value is `null`, and an
 * absent field reads the same way. Timestamp formatting lives in
 * @speed/i18n and is tested there.
 */

import { describe, expect, it } from 'vitest'
import { isNullInstant } from './datetime.js'

describe('isNullInstant', () => {
  it('recognizes a null and an absent value without throwing', () => {
    expect(isNullInstant(null)).toBe(true)
    expect(isNullInstant(undefined)).toBe(true)
  })

  it('treats a real instant as present', () => {
    expect(isNullInstant('2026-10-15T12:00:00Z')).toBe(false)
  })
})
