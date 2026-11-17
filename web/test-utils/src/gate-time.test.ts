import { afterEach, describe, expect, it, vi } from 'vitest'
import { gateTimeScale, parseGateTimeScale } from './gate-time.js'

afterEach(() => {
  vi.unstubAllEnvs()
})

describe('the caller time scale', () => {
  it('is 1 when the variable is unset', () => {
    vi.stubEnv('GATE_TIME_SCALE', undefined)
    expect(gateTimeScale()).toBe(1)
  })

  it('is 1 when the variable is empty', () => {
    vi.stubEnv('GATE_TIME_SCALE', '')
    expect(gateTimeScale()).toBe(1)
  })

  it('takes a finite value of at least 1, decimals included', () => {
    vi.stubEnv('GATE_TIME_SCALE', '2.5')
    expect(gateTimeScale()).toBe(2.5)
  })

  it('refuses a value that is not a number', () => {
    vi.stubEnv('GATE_TIME_SCALE', 'abc')
    expect(() => gateTimeScale()).toThrow(
      /GATE_TIME_SCALE must be a finite number of at least 1.*"abc"/,
    )
  })

  it.each(['0', '0.5'])('refuses %s, which lowers the shared budget', (raw) => {
    vi.stubEnv('GATE_TIME_SCALE', raw)
    expect(() => gateTimeScale()).toThrow(/at least 1/)
  })

  it('refuses a non-finite value', () => {
    vi.stubEnv('GATE_TIME_SCALE', 'Infinity')
    expect(() => gateTimeScale()).toThrow(/finite number/)
  })

  it.each<[string | undefined, number]>([
    [undefined, 1],
    ['', 1],
    ['  ', 1],
    ['2.5', 2.5],
    [' 2 ', 2],
    ['1.5e1', 15],
  ])('applies the same rule to the parse itself: %j is %d', (raw, expected) => {
    expect(parseGateTimeScale(raw)).toBe(expected)
  })

  it.each([
    // A decimal literal that overflows to Infinity: the finite check is what
    // refuses it, so deleting that check lets it through here.
    '1e999',
    // Not a decimal literal at all.
    'NaN',
    // Decimal, but below the floor.
    '-2',
  ])('refuses %s, which is not a usable scale', (raw) => {
    expect(() => parseGateTimeScale(raw)).toThrow(
      /GATE_TIME_SCALE must be a finite number of at least 1/,
    )
  })

  it.each(['0x10'])('refuses %s, which is not a decimal number', (raw) => {
    expect(() => parseGateTimeScale(raw)).toThrow(
      /GATE_TIME_SCALE must be a finite number of at least 1.*"0x10"/,
    )
  })
})
