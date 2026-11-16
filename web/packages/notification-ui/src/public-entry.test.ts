/**
 * The public entry carries the message-text resolver.
 *
 * `renderDetail` receives the raw notification message, so a host that
 * renders the title or body itself must resolve the delivery pipeline's
 * time markers with the same function the inbox uses. That function is
 * therefore part of the package's public entry, not an internal-only
 * helper: a host has no other way to keep the private-use markers out of
 * its own detail panel.
 */

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
