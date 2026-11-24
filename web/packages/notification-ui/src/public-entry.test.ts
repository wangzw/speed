import { describe, expect, it } from 'vitest'
import { resolveMessageText } from './index.js'

describe('public entry', () => {
  it('exports resolveMessageText for a host rendering a message itself', () => {
    expect(typeof resolveMessageText).toBe('function')
    expect(
      resolveMessageText(
        'Expires time:expires_at.',
        { expires_at: '2026-11-20T09:30:00Z' },
        (value) => `fmt:${String(value)}`,
      ),
    ).toBe('Expires fmt:2026-11-20T09:30:00Z.')
  })
})
