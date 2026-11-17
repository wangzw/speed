import { afterEach, describe, expect, it, vi } from 'vitest'
import { gateTimeScale } from '../vitest.shared'

afterEach(() => {
  vi.unstubAllEnvs()
})

describe("the preset's caller time scale", () => {
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

  it.each([
    // A decimal literal that overflows to Infinity: the finite check is what
    // refuses it, so deleting that check lets it through here.
    '1e999',
    // Not a decimal literal at all.
    'NaN',
    // Decimal, but below the floor.
    '-2',
  ])('refuses %s, which is not a usable scale', (raw) => {
    vi.stubEnv('GATE_TIME_SCALE', raw)
    expect(() => gateTimeScale()).toThrow(
      /GATE_TIME_SCALE must be a finite number of at least 1/,
    )
  })

  it.each([
    ['  ', 1],
    [' 2 ', 2],
    ['1.5e1', 15],
  ])('trims %j and reads it as %d', (raw, expected) => {
    vi.stubEnv('GATE_TIME_SCALE', raw)
    expect(gateTimeScale()).toBe(expected)
  })

  it.each(['0x10'])('refuses %s, which is not a decimal number', (raw) => {
    vi.stubEnv('GATE_TIME_SCALE', raw)
    expect(() => gateTimeScale()).toThrow(
      /GATE_TIME_SCALE must be a finite number of at least 1.*"0x10"/,
    )
  })

  it('injects the caller scale into the browser tier', async () => {
    // The browser page has no process global, so the tier's config parses
    // the variable in Node and hands the value to the setup as a define.
    // This pins that a caller's 2.5 actually reaches the tier; the browser
    // case then applies it to the async budget.
    vi.stubEnv('GATE_TIME_SCALE', '2.5')
    vi.resetModules()
    const config = (await import('../vitest.browser.config.ts')).default
    expect(config.define?.__SPEED_GATE_TIME_SCALE__).toBe('2.5')
  })
})
