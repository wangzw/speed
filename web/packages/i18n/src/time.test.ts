/**
 * time.ts behaviour: the three-tier display-timezone chain (stored
 * preference -> device -> UTC), its Intl guards, and instant formatting.
 * The guard cases are the point: a stored value the engine rejects must
 * fall through the chain, never throw into a render, and an environment
 * whose Intl cannot answer must degrade to the terminal tier. Formatting
 * assertions compare against Intl's own output for the documented style
 * pair, so the test pins the wiring rather than a locale's exact wording.
 */

import { describe, expect, it, vi } from 'vitest'
import {
  formatInstant,
  isSupportedTimeZone,
  resolveTimeZone,
} from './time.js'

/** Runs fn with Intl.DateTimeFormat stubbed to throw on every
 * construction (the environment-cannot-answer shape), restoring after. */
function withBrokenDateTimeFormat(
  fn: () => void,
  { onlyWithoutArgs = false }: { onlyWithoutArgs?: boolean } = {},
): void {
  const original = Intl.DateTimeFormat
  const broken = function (
    this: unknown,
    ...args: unknown[]
  ): Intl.DateTimeFormat {
    if (!onlyWithoutArgs || args.length === 0) {
      throw new RangeError('test: Intl.DateTimeFormat unavailable')
    }
    return new original(...(args as ConstructorParameters<typeof Intl.DateTimeFormat>))
  } as unknown as typeof Intl.DateTimeFormat
  const spy = vi.spyOn(Intl, 'DateTimeFormat').mockImplementation(broken)
  try {
    fn()
  } finally {
    spy.mockRestore()
  }
}

function deviceTimeZone(): string | null {
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
  return zone === '' ? null : (zone ?? null)
}

describe('isSupportedTimeZone', () => {
  it('accepts a real IANA zone and rejects an unknown one', () => {
    expect(isSupportedTimeZone('Asia/Shanghai')).toBe(true)
    expect(isSupportedTimeZone('UTC')).toBe(true)
    expect(isSupportedTimeZone('Mars/Olympus')).toBe(false)
    expect(isSupportedTimeZone('')).toBe(false)
  })

  it('answers false rather than throwing when the engine refuses to construct', () => {
    withBrokenDateTimeFormat(() => {
      expect(isSupportedTimeZone('Asia/Shanghai')).toBe(false)
    })
  })
})

describe('resolveTimeZone', () => {
  const device = deviceTimeZone()

  it('prefers a stored value the engine accepts', () => {
    expect(resolveTimeZone('Asia/Tokyo')).toBe('Asia/Tokyo')
  })

  it('trims a stored value before accepting it', () => {
    expect(resolveTimeZone('  Asia/Tokyo ')).toBe('Asia/Tokyo')
  })

  it('falls through an empty or absent stored value to the device zone', () => {
    expect(resolveTimeZone('')).toBe(device)
    expect(resolveTimeZone('   ')).toBe(device)
    expect(resolveTimeZone(null)).toBe(device)
    expect(resolveTimeZone(undefined)).toBe(device)
  })

  it('skips a stored value the engine rejects instead of crashing the render', () => {
    expect(resolveTimeZone('Mars/Olympus')).toBe(device)
  })

  it('lands on UTC when neither the stored value nor the device resolves', () => {
    withBrokenDateTimeFormat(() => {
      expect(resolveTimeZone('Mars/Olympus')).toBe('UTC')
      expect(resolveTimeZone('')).toBe('UTC')
      expect(resolveTimeZone(null)).toBe('UTC')
    })
  })

  it('answers a zone Intl.DateTimeFormat accepts, for every outcome', () => {
    for (const zone of [
      resolveTimeZone('Asia/Tokyo'),
      resolveTimeZone(''),
      resolveTimeZone('Mars/Olympus'),
    ]) {
      expect(() => new Intl.DateTimeFormat('en-US', { timeZone: zone })).not.toThrow()
    }
  })
})

describe('formatInstant', () => {
  const instant = '2020-01-01T00:00:00Z'

  it('formats string, number and Date inputs identically', () => {
    const date = new Date(instant)
    const expected = new Intl.DateTimeFormat('en-US', {
      dateStyle: 'medium',
      timeStyle: 'short',
      timeZone: 'UTC',
    }).format(date)
    expect(formatInstant(instant, 'en-US', { timeZone: 'UTC' })).toBe(expected)
    expect(formatInstant(date.getTime(), 'en-US', { timeZone: 'UTC' })).toBe(expected)
    expect(formatInstant(date, 'en-US', { timeZone: 'UTC' })).toBe(expected)
  })

  it('renders each style through its documented Intl pair', () => {
    const date = new Date(instant)
    const styles = {
      short: { dateStyle: 'short', timeStyle: 'short' },
      medium: { dateStyle: 'medium', timeStyle: 'short' },
      long: { dateStyle: 'medium', timeStyle: 'long' },
    } as const
    for (const [style, options] of Object.entries(styles)) {
      const expected = new Intl.DateTimeFormat('en-US', {
        ...options,
        timeZone: 'UTC',
      }).format(date)
      expect(
        formatInstant(instant, 'en-US', {
          timeZone: 'UTC',
          style: style as keyof typeof styles,
        }),
      ).toBe(expected)
    }
  })

  it('defaults to the medium tier', () => {
    const date = new Date(instant)
    const expected = new Intl.DateTimeFormat('en-US', {
      dateStyle: 'medium',
      timeStyle: 'short',
      timeZone: 'UTC',
    }).format(date)
    expect(formatInstant(instant, 'en-US', { timeZone: 'UTC' })).toBe(expected)
  })

  it('renders in the given zone and in the engine default when absent', () => {
    const date = new Date(instant)
    expect(formatInstant(instant, 'en-US', { timeZone: 'UTC' })).toBe(
      new Intl.DateTimeFormat('en-US', {
        dateStyle: 'medium',
        timeStyle: 'short',
        timeZone: 'UTC',
      }).format(date),
    )
    expect(formatInstant(instant, 'en-US', { timeZone: 'Asia/Tokyo' })).toBe(
      new Intl.DateTimeFormat('en-US', {
        dateStyle: 'medium',
        timeStyle: 'short',
        timeZone: 'Asia/Tokyo',
      }).format(date),
    )
    expect(formatInstant(instant, 'en-US')).toBe(
      new Intl.DateTimeFormat('en-US', {
        dateStyle: 'medium',
        timeStyle: 'short',
      }).format(date),
    )
  })

  it('answers the empty string for values that carry no instant', () => {
    expect(formatInstant('', 'en-US', { timeZone: 'UTC' })).toBe('')
    expect(formatInstant('   ', 'en-US', { timeZone: 'UTC' })).toBe('')
    expect(formatInstant('not an instant', 'en-US', { timeZone: 'UTC' })).toBe('')
    expect(formatInstant(Number.NaN, 'en-US', { timeZone: 'UTC' })).toBe('')
    expect(formatInstant(new Date(Number.NaN), 'en-US', { timeZone: 'UTC' })).toBe('')
  })

  it('answers the empty string rather than throwing for a language Intl refuses', () => {
    expect(formatInstant(instant, 'not a language!', { timeZone: 'UTC' })).toBe('')
  })

  it('reuses one formatter per language, style and zone', () => {
    const date = new Date(instant)
    const expected = new Intl.DateTimeFormat('fr-FR', {
      dateStyle: 'medium',
      timeStyle: 'long',
      timeZone: 'Europe/Paris',
    }).format(date)
    const Original = Intl.DateTimeFormat
    const constructions: unknown[][] = []
    const counting = function (
      this: unknown,
      ...args: unknown[]
    ): Intl.DateTimeFormat {
      constructions.push(args)
      return new Original(...(args as ConstructorParameters<typeof Intl.DateTimeFormat>))
    } as unknown as typeof Intl.DateTimeFormat
    const spy = vi.spyOn(Intl, 'DateTimeFormat').mockImplementation(counting)
    try {
      expect(
        formatInstant(instant, 'fr-FR', { timeZone: 'Europe/Paris', style: 'long' }),
      ).toBe(expected)
      expect(
        formatInstant(date.getTime(), 'fr-FR', {
          timeZone: 'Europe/Paris',
          style: 'long',
        }),
      ).toBe(expected)
      expect(constructions).toHaveLength(1)
    } finally {
      spy.mockRestore()
    }
  })
})
