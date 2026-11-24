import { describe, expect, it } from 'vitest'
import {
  instantToWallClock,
  wallClockToInstant,
  type WallClock,
} from './wall-clock.js'

function iso(date: Date | null): string {
  expect(date).not.toBeNull()
  return (date as Date).toISOString()
}

describe('instantToWallClock', () => {
  it('reads the wall clock of a zone with a whole-hour offset', () => {
    expect(instantToWallClock('2026-11-16T00:30:00Z', 'Asia/Shanghai')).toEqual({
      year: 2026,
      month: 11,
      day: 16,
      hour: 8,
      minute: 30,
      second: 0,
    })
    expect(instantToWallClock('2026-11-16T00:30:00Z', 'UTC')).toEqual({
      year: 2026,
      month: 11,
      day: 16,
      hour: 0,
      minute: 30,
      second: 0,
    })
  })

  it('reads a zone with a fractional-hour offset', () => {
    expect(instantToWallClock('2026-01-01T00:00:00Z', 'Asia/Kathmandu')).toEqual({
      year: 2026,
      month: 1,
      day: 1,
      hour: 5,
      minute: 45,
      second: 0,
    })
  })

  it('reads standard and daylight offsets apart', () => {
    expect(instantToWallClock('2026-01-15T12:00:00Z', 'America/New_York')).toEqual({
      year: 2026,
      month: 1,
      day: 15,
      hour: 7,
      minute: 0,
      second: 0,
    })
    expect(instantToWallClock('2026-07-15T12:00:00Z', 'America/New_York')).toEqual({
      year: 2026,
      month: 7,
      day: 15,
      hour: 8,
      minute: 0,
      second: 0,
    })
  })

  it('accepts string, number and Date inputs, dropping the sub-second part', () => {
    const expected = {
      year: 2026,
      month: 11,
      day: 16,
      hour: 0,
      minute: 30,
      second: 0,
    }
    const date = new Date('2026-11-16T00:30:00.999Z')
    expect(instantToWallClock('2026-11-16T00:30:00.999Z', 'UTC')).toEqual(expected)
    expect(instantToWallClock(date.getTime(), 'UTC')).toEqual(expected)
    expect(instantToWallClock(date, 'UTC')).toEqual(expected)
  })

  it('returns null for a value that carries no instant or an unknown zone', () => {
    expect(instantToWallClock('', 'UTC')).toBeNull()
    expect(instantToWallClock('   ', 'UTC')).toBeNull()
    expect(instantToWallClock('not an instant', 'UTC')).toBeNull()
    expect(instantToWallClock(Number.NaN, 'UTC')).toBeNull()
    expect(instantToWallClock(new Date(Number.NaN), 'UTC')).toBeNull()
    expect(
      instantToWallClock('2026-11-16T00:30:00Z', 'Mars/Olympus'),
    ).toBeNull()
  })
})

describe('wallClockToInstant', () => {
  it('maps a whole-hour zone reading to its instant', () => {
    expect(
      iso(wallClockToInstant({ year: 2026, month: 11, day: 16, hour: 8, minute: 30 }, 'Asia/Shanghai')),
    ).toBe('2026-11-16T00:30:00.000Z')
    expect(
      iso(wallClockToInstant({ year: 2026, month: 11, day: 16, hour: 0, minute: 30 }, 'UTC')),
    ).toBe('2026-11-16T00:30:00.000Z')
  })

  it('maps a fractional-hour zone reading to its instant', () => {
    expect(
      iso(wallClockToInstant({ year: 2026, month: 1, day: 1, hour: 5, minute: 45 }, 'Asia/Kathmandu')),
    ).toBe('2026-01-01T00:00:00.000Z')
  })

  it('applies the standard and daylight offsets of the season', () => {
    expect(
      iso(wallClockToInstant({ year: 2026, month: 1, day: 15, hour: 12, minute: 0 }, 'America/New_York')),
    ).toBe('2026-01-15T17:00:00.000Z')
    expect(
      iso(wallClockToInstant({ year: 2026, month: 7, day: 15, hour: 12, minute: 0 }, 'America/New_York')),
    ).toBe('2026-07-15T16:00:00.000Z')
  })

  it('reads an absent second as zero', () => {
    expect(
      iso(wallClockToInstant({ year: 2026, month: 11, day: 16, hour: 8, minute: 30 }, 'Asia/Shanghai')),
    ).toBe('2026-11-16T00:30:00.000Z')
    expect(
      iso(
        wallClockToInstant(
          { year: 2026, month: 11, day: 16, hour: 8, minute: 30, second: 45 },
          'Asia/Shanghai',
        ),
      ),
    ).toBe('2026-11-16T00:30:45.000Z')
  })

  it('round-trips an instant through its wall clock in the same zone', () => {
    const cases = [
      ['2026-11-16T00:30:00Z', 'Asia/Shanghai'],
      ['2026-01-01T00:00:00Z', 'UTC'],
      ['2026-03-08T06:30:00Z', 'America/New_York'],
      ['2026-07-15T16:00:00Z', 'America/New_York'],
      ['2026-01-01T00:00:00Z', 'Asia/Kathmandu'],
      ['2028-02-29T23:59:59Z', 'Europe/Berlin'],
    ] as const
    for (const [instant, zone] of cases) {
      const clock = instantToWallClock(instant, zone)
      expect(clock, `${instant} in ${zone}`).not.toBeNull()
      expect(iso(wallClockToInstant(clock as WallClock, zone))).toBe(
        new Date(instant).toISOString(),
      )
    }
  })
})

describe('daylight-saving transitions', () => {
  it('answers null for a spring-forward gap', () => {
    expect(
      wallClockToInstant({ year: 2026, month: 3, day: 8, hour: 2, minute: 0 }, 'America/New_York'),
    ).toBeNull()
    expect(
      wallClockToInstant({ year: 2026, month: 3, day: 8, hour: 2, minute: 30 }, 'America/New_York'),
    ).toBeNull()
  })

  it('maps the readings on either side of a spring-forward gap', () => {
    expect(
      iso(wallClockToInstant({ year: 2026, month: 3, day: 8, hour: 1, minute: 30 }, 'America/New_York')),
    ).toBe('2026-03-08T06:30:00.000Z') 
    expect(
      iso(wallClockToInstant({ year: 2026, month: 3, day: 8, hour: 3, minute: 30 }, 'America/New_York')),
    ).toBe('2026-03-08T07:30:00.000Z') 
  })

  it('resolves a fall-back overlap to the earlier instant', () => {
    expect(
      iso(wallClockToInstant({ year: 2026, month: 11, day: 1, hour: 1, minute: 30 }, 'America/New_York')),
    ).toBe('2026-11-01T05:30:00.000Z')
    expect(instantToWallClock('2026-11-01T05:30:00Z', 'America/New_York')).toEqual({
      year: 2026,
      month: 11,
      day: 1,
      hour: 1,
      minute: 30,
      second: 0,
    })
    expect(instantToWallClock('2026-11-01T06:30:00Z', 'America/New_York')).toEqual({
      year: 2026,
      month: 11,
      day: 1,
      hour: 1,
      minute: 30,
      second: 0,
    })
  })

  it('resolves a fall-back overlap to the later instant when asked', () => {
    expect(
      iso(
        wallClockToInstant(
          { year: 2026, month: 11, day: 1, hour: 1, minute: 30 },
          'America/New_York',
          { overlap: 'later' },
        ),
      ),
    ).toBe('2026-11-01T06:30:00.000Z')
  })

  it('resolves an east-of-UTC fall-back overlap to the earlier instant', () => {
    expect(
      iso(wallClockToInstant({ year: 2026, month: 10, day: 25, hour: 2, minute: 30 }, 'Europe/Berlin')),
    ).toBe('2026-10-25T00:30:00.000Z')
    expect(instantToWallClock('2026-10-25T01:30:00Z', 'Europe/Berlin')).toEqual({
      year: 2026,
      month: 10,
      day: 25,
      hour: 2,
      minute: 30,
      second: 0,
    })
  })

  it('resolves a half-hour fall-back overlap to the earlier instant', () => {
    expect(
      iso(wallClockToInstant({ year: 2026, month: 4, day: 5, hour: 1, minute: 45 }, 'Australia/Lord_Howe')),
    ).toBe('2026-04-04T14:45:00.000Z')
    expect(instantToWallClock('2026-04-04T15:15:00Z', 'Australia/Lord_Howe')).toEqual({
      year: 2026,
      month: 4,
      day: 5,
      hour: 1,
      minute: 45,
      second: 0,
    })
  })

  it('treats the same reading in a zone without daylight saving as unambiguous', () => {
    expect(
      iso(wallClockToInstant({ year: 2026, month: 11, day: 1, hour: 1, minute: 30 }, 'Asia/Shanghai')),
    ).toBe('2026-10-31T17:30:00.000Z')
  })
})

describe('invalid readings', () => {
  it('answers null for out-of-range and fractional fields', () => {
    const invalid: WallClock[] = [
      { year: 2026, month: 0, day: 1, hour: 0, minute: 0 },
      { year: 2026, month: 13, day: 1, hour: 0, minute: 0 },
      { year: 2026, month: 1, day: 0, hour: 0, minute: 0 },
      { year: 2026, month: 1, day: 32, hour: 0, minute: 0 },
      { year: 2026, month: 1, day: 1, hour: 24, minute: 0 },
      { year: 2026, month: 1, day: 1, hour: 0, minute: 60 },
      { year: 2026, month: 1, day: 1, hour: 0, minute: 0, second: 60 },
      { year: 2026, month: 1, day: 1, hour: 1.5, minute: 0 },
      { year: 2026, month: 1, day: 1, hour: 0, minute: Number.NaN },
      { year: 0, month: 1, day: 1, hour: 0, minute: 0 },
      { year: 10000, month: 1, day: 1, hour: 0, minute: 0 },
    ]
    for (const reading of invalid) {
      expect(wallClockToInstant(reading, 'UTC'), JSON.stringify(reading)).toBeNull()
    }
  })

  it('answers null for a calendar overflow instead of normalizing it', () => {
    expect(
      wallClockToInstant({ year: 2026, month: 2, day: 29, hour: 12, minute: 0 }, 'UTC'),
    ).toBeNull()
    expect(
      wallClockToInstant({ year: 2026, month: 4, day: 31, hour: 12, minute: 0 }, 'UTC'),
    ).toBeNull()
    expect(
      iso(wallClockToInstant({ year: 2028, month: 2, day: 29, hour: 12, minute: 0 }, 'UTC')),
    ).toBe('2028-02-29T12:00:00.000Z')
  })

  it('answers null for a missing field and an unknown zone', () => {
    expect(
      wallClockToInstant(
        { year: 2026, month: 1 } as unknown as WallClock,
        'UTC',
      ),
    ).toBeNull()
    expect(
      wallClockToInstant({ year: 2026, month: 1, day: 1, hour: 0, minute: 0 }, 'Mars/Olympus'),
    ).toBeNull()
  })
})
