/**
 * The factorseed command's one JSON line is part of its result.
 *
 * The impersonation fixture asks the command for an authenticator and an SMS
 * factor, so a zero exit that omitted either is a half-seeded operator. The
 * reader holds the line to both keys, and a failure carries the standard
 * output and standard error so the half-seeded run is visible.
 */
import { describe, expect, it } from 'vitest'

import { readSeededFactors } from './factor-seed'

const WHAT = 'writing the impersonation operator factors'
const STDERR = 'factorseed: something went wrong'

describe('readSeededFactors', () => {
  it('reads both identifiers from the line', () => {
    const line = JSON.stringify({ totp: 'totp-factor', sms: 'sms-factor' })
    expect(readSeededFactors(`${line}\n`, '', WHAT)).toEqual({
      totp: 'totp-factor',
      sms: 'sms-factor',
    })
  })

  it.each(['totp', 'sms'] as const)(
    'rejects a line that omits %s, naming both streams',
    (missing) => {
      const line = JSON.stringify(
        missing === 'totp' ? { sms: 'sms-factor' } : { totp: 'totp-factor' },
      )
      const read = (): unknown => readSeededFactors(`${line}\n`, STDERR, WHAT)
      expect(read).toThrow(`wrote no ${missing} factor`)
      expect(read).toThrow(`standard output:\n${line}\n\nstandard error:\n${STDERR}`)
    },
  )

  it('rejects an empty factor value', () => {
    const line = JSON.stringify({ totp: '', sms: 'sms-factor' })
    expect(() => readSeededFactors(`${line}\n`, STDERR, WHAT)).toThrow(
      'wrote no totp factor',
    )
  })

  it('rejects standard output that carries no JSON line', () => {
    expect(() => readSeededFactors('no line here\n', STDERR, WHAT)).toThrow(
      'wrote no JSON line',
    )
    expect(() => readSeededFactors('no line here\n', STDERR, WHAT)).toThrow(
      'standard output:\nno line here\n\nstandard error:\nfactorseed: something went wrong',
    )
  })
})
