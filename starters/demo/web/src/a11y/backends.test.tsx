import { describe, expect, it } from 'vitest'
import { EXACT, PATTERNS, resolveBody } from './backends.js'

const API_PREFIX = '/api/v1/'

function patternText(pattern: RegExp): string {
  return pattern.source.replace(/\\\//g, '/')
}

function sampleUrl(pattern: RegExp): string {
  return patternText(pattern)
    .replace(/^\^/, '')
    .replace(/\$$/, '')
    .replace(/\[\^\/\]\+/g, 'sample')
}

describe('scripted backend tables', () => {
  it('names a full /api/v1 path in every exact entry', () => {
    for (const url of Object.keys(EXACT)) {
      expect(url.startsWith(API_PREFIX), `${url} lacks the /api/v1 prefix`).toBe(true)
      expect(url, `${url} carries a query string, which the backend never sees`).not.toContain('?')
    }
  })

  it('anchors every pattern on the /api/v1 path and the end of the URL', () => {
    for (const [pattern] of PATTERNS) {
      const text = patternText(pattern)
      expect(text.startsWith(`^${API_PREFIX}`), `${pattern} lacks the /api/v1 prefix`).toBe(
        true,
      )
      expect(text.endsWith('$'), `${pattern} is not anchored at the end of the URL`).toBe(true)
    }
  })

  it('answers every exact entry in both modes', () => {
    for (const [url, entry] of Object.entries(EXACT)) {
      expect(resolveBody('data', url), `data: ${url}`).toBe(entry.data)
      expect(resolveBody('empty', url), `empty: ${url}`).toBe(entry.empty)
    }
  })

  it('answers the profile and unread count the account menu reads', () => {
    expect(resolveBody('data', '/api/v1/auth/me')).toMatchObject({
      account_id: 'account-1',
      kind: 'user',
      display_name: 'Test User',
    })
    expect(
      resolveBody('data', '/api/v1/notifications/messages/unread-count'),
    ).toEqual({ unread: 1 })
  })

  it('answers every pattern entry in both modes, unshadowed by an earlier entry', () => {
    for (const [pattern, entry] of PATTERNS) {
      const url = sampleUrl(pattern)
      expect(pattern.test(url), `${pattern} does not match its own sample ${url}`).toBe(true)
      expect(resolveBody('data', url), `data: ${url}`).toBe(entry.data)
      expect(resolveBody('empty', url), `empty: ${url}`).toBe(entry.empty)
    }
  })
})
