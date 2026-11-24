import { describe, expect, it } from 'vitest'
import { resolveMessageText } from './message-text.js'

const fmt = (value: string | number | Date): string =>
  `fmt:${value instanceof Date ? value.toISOString() : String(value)}`

describe('resolveMessageText', () => {
  it('resolves a marker from the named parameter', () => {
    expect(
      resolveMessageText(
        'Expires \uE000time:expires_at\uE001.',
        { expires_at: '2026-11-20T09:30:00Z' },
        fmt,
      ),
    ).toBe('Expires fmt:2026-11-20T09:30:00Z.')
  })

  it('leaves a value the formatter cannot parse as written', () => {
    const onlyValid = (value: string | number | Date): string =>
      value === 'not-a-time' ? '' : fmt(value)
    expect(
      resolveMessageText(
        'Expires \uE000time:expires_at\uE001.',
        { expires_at: 'not-a-time' },
        onlyValid,
      ),
    ).toBe('Expires not-a-time.')
  })

  it('resolves every marker and tolerates repeated names', () => {
    expect(
      resolveMessageText(
        '\uE000time:a\uE001 then \uE000time:b\uE001 then \uE000time:a\uE001',
        { a: 1, b: 2 },
        fmt,
      ),
    ).toBe('fmt:1 then fmt:2 then fmt:1')
  })

  it.each([
    ['a missing parameter', {}],
    ['a null value', { a: null }],
    ['a boolean', { a: true }],
    ['an object', { a: { at: '2026-11-20T09:30:00Z' } }],
    ['an array', { a: ['2026-11-20T09:30:00Z'] }],
    ['an empty string', { a: '' }],
    ['a whitespace string', { a: '   ' }],
  ])('resolves %s to the empty string', (_name, params) => {
    expect(
      resolveMessageText('x\uE000time:a\uE001y', params, fmt),
    ).toBe('xy')
  })

  it('resolves every marker to the empty string when params is null', () => {
    expect(
      resolveMessageText(
        'a\uE000time:x\uE001b\uE000time:y\uE001c',
        null,
        fmt,
      ),
    ).toBe('abc')
  })

  it('leaves private-use characters that are not a time marker untouched', () => {
    const text = 'a\uE000not-a-marker\uE001b'
    expect(resolveMessageText(text, {}, fmt)).toBe(text)
  })

  it('returns text without a marker unchanged', () => {
    expect(resolveMessageText('plain text', { a: 1 }, fmt)).toBe('plain text')
  })
})
