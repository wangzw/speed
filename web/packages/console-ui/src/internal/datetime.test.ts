/**
 * datetime: the console's UTC timestamp rendering and the zero-instant
 * sentinel.
 *
 * An instant renders through Intl in the given language and explicitly in
 * UTC; an absent or malformed value renders nothing rather than throwing.
 * The zero instant (the "never" sentinel) is recognized without a
 * timezone conversion.
 */

import { describe, expect, it } from 'vitest'
import { formatUtc, isZeroInstant } from './datetime.js'

describe('formatUtc', () => {
  it('formats an instant in UTC in the given language', () => {
    const expected = new Intl.DateTimeFormat('zh-CN', {
      dateStyle: 'medium',
      timeStyle: 'short',
      timeZone: 'UTC',
    }).format(new Date('2026-10-15T12:00:00Z'))
    expect(formatUtc('2026-10-15T12:00:00Z', 'zh-CN')).toBe(expected)
  })

  it('reuses one formatter per language', () => {
    // The same language twice answers identically (a cached formatter).
    expect(formatUtc('2026-10-15T12:00:00Z', 'en-US')).toBe(
      formatUtc('2026-10-15T12:00:00Z', 'en-US'),
    )
  })

  it('renders nothing for an absent, empty or malformed value', () => {
    expect(formatUtc(undefined, 'en-US')).toBeNull()
    expect(formatUtc('', 'en-US')).toBeNull()
    expect(formatUtc('not-a-date', 'en-US')).toBeNull()
  })
})

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
