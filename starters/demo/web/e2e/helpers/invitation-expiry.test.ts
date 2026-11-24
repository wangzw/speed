import { describe, expect, it } from 'vitest'

import { CHOSEN_LIFETIME_MS, expiryChoices } from './invitation-expiry'

const format = (at: number): string =>
  new Intl.DateTimeFormat('en-US', {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone: 'UTC',
  }).format(new Date(at + CHOSEN_LIFETIME_MS))

describe('expiryChoices', () => {
  it('includes the far endpoint when a sub-minute span crosses a minute boundary', () => {
    const from = Date.UTC(2026, 10, 17, 9, 30, 59)
    const to = from + 2_000
    expect(format(from)).not.toBe(format(to))

    const choices = expiryChoices(from, to)
    expect(choices).toContain(format(from))
    expect(choices).toContain(format(to))
  })

  it('treats the two recorded instants as a span regardless of their order', () => {
    const later = Date.UTC(2026, 10, 17, 9, 30, 59)
    const earlier = later - 2_000
    const choices = expiryChoices(later, earlier)
    expect(choices).toContain(format(earlier))
    expect(choices).toContain(format(later))
  })
})
