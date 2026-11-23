import { describe, expect, it } from 'vitest'

import { parseGateJobs } from './gate-jobs'

describe('parseGateJobs', () => {
  it.each<[string, number]>([
    ['1', 1],
    ['4', 4],
    ['8', 8],
    [' 6 ', 6],
    ['16', 16],
  ])('reads %j as the width %d', (raw, expected) => {
    expect(parseGateJobs(raw)).toBe(expected)
  })

  it.each<string | undefined>([
    undefined,
    '',
    '   ',
    '0',
    '00',
    '-2',
    '+4',
    '1.5',
    'abc',
    '8x',
    'NaN',
    'Infinity',
    '0x10',
    '1e3',
    '1_000',
    '9'.repeat(20),
  ])('refuses %j', (raw) => {
    expect(() => parseGateJobs(raw)).toThrow(/GATE_JOBS must be a positive integer/)
    expect(() => parseGateJobs(raw)).toThrow(/make e2e/)
  })

  it('names an unset variable as unset rather than as an empty string', () => {
    expect(() => parseGateJobs(undefined)).toThrow(/got \(unset\)/)
  })
})
