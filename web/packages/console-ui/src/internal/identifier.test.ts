/**
 * isRejectedIdentifier: the identifier rule the roster's inputs mirror
 * (pkg/tenant's IsIdentifier). A non-empty value free of blanks and control
 * characters is admitted; an empty value, or one carrying a blank or a
 * control character anywhere, is refused.
 */

import { describe, expect, it } from 'vitest'
import { isRejectedIdentifier } from './identifier.js'

describe('isRejectedIdentifier', () => {
  it('admits a non-empty value with no blank or control character', () => {
    for (const value of ['acme', 'Acme-Inc', 'acme.company', 'Acme_Inc', 'Acme-Café']) {
      expect(isRejectedIdentifier(value)).toBe(false)
    }
  })

  it('refuses an empty value', () => {
    expect(isRejectedIdentifier('')).toBe(true)
  })

  it('refuses a value carrying a blank anywhere', () => {
    for (const value of ['Acme Corp', 'Acme\tCo', 'Acme\nCo', ' Acme', 'Acme ', 'Acme Co']) {
      expect(isRejectedIdentifier(value)).toBe(true)
    }
  })

  it('refuses a value carrying a control character anywhere', () => {
    for (const value of ['Acme\u0000', 'Acme\u001f', 'Ac\u007felme']) {
      expect(isRejectedIdentifier(value)).toBe(true)
    }
  })
})
