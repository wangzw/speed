import { describe, expect, it } from 'vitest'
import { formatSecretForDisplay, parseOtpauth } from './otpauth.js'

const SECRET = 'JBSWY3DPEHPK3PXP'

describe('parseOtpauth', () => {
  it('parses a full Key Uri with every parameter', () => {
    const result = parseOtpauth(
      `otpauth://totp/Example%20Co:alice%40example.com?secret=${SECRET}&issuer=Example%20Co&algorithm=SHA256&digits=8&period=60`,
    )

    expect(result).toEqual({
      ok: true,
      otpauth: {
        issuer: 'Example Co',
        account: 'alice@example.com',
        secret: SECRET,
        algorithm: 'SHA256',
        digits: 8,
        period: 60,
      },
    })
  })

  it('reads a label with no issuer prefix as the account alone', () => {
    const result = parseOtpauth(`otpauth://totp/alice%40example.com?secret=${SECRET}`)

    expect(result).toEqual({
      ok: true,
      otpauth: {
        account: 'alice@example.com',
        secret: SECRET,
        algorithm: 'SHA1',
        digits: 6,
        period: 30,
      },
    })
  })

  it('applies the authenticator defaults when the parameters are absent', () => {
    const result = parseOtpauth(`otpauth://totp/Example:alice?secret=${SECRET}`)

    expect(result.ok).toBe(true)
    if (result.ok) {
      expect(result.otpauth.algorithm).toBe('SHA1')
      expect(result.otpauth.digits).toBe(6)
      expect(result.otpauth.period).toBe(30)
    }
  })

  it('decodes a percent-encoded colon in the label', () => {
    const result = parseOtpauth(
      `otpauth://totp/ACME%20Corp%3Aalice%40example.com?secret=${SECRET}`,
    )

    expect(result.ok).toBe(true)
    if (result.ok) {
      expect(result.otpauth.issuer).toBe('ACME Corp')
      expect(result.otpauth.account).toBe('alice@example.com')
    }
  })

  it('matches the scheme and type case-insensitively', () => {
    const result = parseOtpauth(`OTPAUTH://TOTP/alice?secret=${SECRET}`)

    expect(result.ok).toBe(true)
    if (result.ok) {
      expect(result.otpauth.account).toBe('alice')
    }
  })

  it('falls back to the issuer query parameter when the label has no prefix', () => {
    const result = parseOtpauth(
      `otpauth://totp/alice?secret=${SECRET}&issuer=Example%20Co`,
    )

    expect(result.ok).toBe(true)
    if (result.ok) {
      expect(result.otpauth.issuer).toBe('Example Co')
    }
  })

  it('keeps the secret exactly as the URI spells it', () => {
    const lower = 'jbswy3dpehpk3pxp'
    const result = parseOtpauth(`otpauth://totp/alice?secret=${lower}`)

    expect(result.ok).toBe(true)
    if (result.ok) {
      expect(result.otpauth.secret).toBe(lower)
    }
  })

  it('never carries the whole URI in the parsed parts', () => {
    const uri = `otpauth://totp/Example:alice?secret=${SECRET}&issuer=Example`
    const result = parseOtpauth(uri)

    expect(JSON.stringify(result)).not.toContain('otpauth://')
  })

  it('refuses a missing secret', () => {
    expect(parseOtpauth('otpauth://totp/alice')).toEqual({
      ok: false,
      error: 'missing-secret',
    })
    expect(parseOtpauth('otpauth://totp/alice?secret=')).toEqual({
      ok: false,
      error: 'missing-secret',
    })
  })

  it('refuses a secret outside the base32 alphabet', () => {
    expect(parseOtpauth('otpauth://totp/alice?secret=not%20base32!')).toEqual({
      ok: false,
      error: 'invalid-secret',
    })
  })

  it('refuses a non-TOTP type', () => {
    expect(parseOtpauth(`otpauth://hotp/alice?secret=${SECRET}`)).toEqual({
      ok: false,
      error: 'unsupported-type',
    })
  })

  it('refuses another scheme', () => {
    expect(parseOtpauth(`https://totp/alice?secret=${SECRET}`)).toEqual({
      ok: false,
      error: 'unsupported-scheme',
    })
  })

  it('refuses an unparseable URI', () => {
    expect(parseOtpauth('')).toEqual({ ok: false, error: 'invalid-uri' })
    expect(parseOtpauth('not a uri')).toEqual({ ok: false, error: 'invalid-uri' })
  })

  it('refuses a percent-encoded label it cannot decode', () => {
    expect(parseOtpauth(`otpauth://totp/bad%zz?secret=${SECRET}`)).toEqual({
      ok: false,
      error: 'invalid-label',
    })
  })

  it.each(['5', '9', '0', 'six', '6x', ''])(
    'refuses digits=%s',
    (digits) => {
      expect(
        parseOtpauth(`otpauth://totp/alice?secret=${SECRET}&digits=${digits}`),
      ).toEqual({ ok: false, error: 'invalid-digits' })
    },
  )

  it.each(['0', '-30', '30.5', 'forever', ''])(
    'refuses period=%s',
    (period) => {
      expect(
        parseOtpauth(`otpauth://totp/alice?secret=${SECRET}&period=${period}`),
      ).toEqual({ ok: false, error: 'invalid-period' })
    },
  )

  it('refuses an algorithm outside the whitelist', () => {
    expect(
      parseOtpauth(`otpauth://totp/alice?secret=${SECRET}&algorithm=MD5`),
    ).toEqual({ ok: false, error: 'unsupported-algorithm' })
  })
})

describe('formatSecretForDisplay', () => {
  it('groups the base32 secret in fours for hand entry', () => {
    expect(formatSecretForDisplay(SECRET)).toBe('JBSW Y3DP EHPK 3PXP')
  })

  it('leaves a final short group short', () => {
    expect(formatSecretForDisplay('JBSWY3DPEH')).toBe('JBSW Y3DP EH')
  })

  it('drops base32 padding', () => {
    expect(formatSecretForDisplay('JBSWY3DP====')).toBe('JBSW Y3DP')
  })

  it('answers the empty string for an empty secret', () => {
    expect(formatSecretForDisplay('')).toBe('')
  })

  it('shows the secret alone, never a URI', () => {
    const display = formatSecretForDisplay(SECRET)
    expect(display).not.toContain('otpauth://')
    expect(display.replace(/ /g, '')).toBe(SECRET)
  })
})
