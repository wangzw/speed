import { describe, expect, it } from 'vitest'

import { parseTimeScale, scaledTimeout } from './time-scale'

describe('parseTimeScale', () => {
  it.each<[string | undefined, number]>([
    [undefined, 1],
    ['', 1],
    ['  ', 1],
    ['1', 1],
    ['3', 3],
    ['1.5', 1.5],
    ['2.5', 2.5],
    [' 2 ', 2],
    ['1.5e1', 15],
  ])('reads %j as the scale %d', (raw, expected) => {
    expect(parseTimeScale(raw)).toBe(expected)
  })

  it.each(['0', '0.5', '-2', 'abc', 'NaN', 'Infinity', '1e999', '0x10'])(
    'refuses %s',
    (raw) => {
      expect(() => parseTimeScale(raw)).toThrow(
        `GATE_TIME_SCALE must be a finite number of at least 1; got ${JSON.stringify(raw)}`,
      )
    },
  )
})

describe('scaledTimeout', () => {
  it('leaves a bound unchanged at scale 1', () => {
    expect(scaledTimeout(120_000, 1)).toBe(120_000)
  })

  it('multiplies by the scale and rounds a fractional millisecond up', () => {
    expect(scaledTimeout(120_000, 2.5)).toBe(300_000)
    expect(scaledTimeout(3, 1.5)).toBe(5)
  })
})
