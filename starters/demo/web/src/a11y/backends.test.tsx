/**
 * The scripted backend tables' own invariant.
 *
 * The route-level sweep trusts `EXACT` and `PATTERNS` to answer every read its
 * routes fire. That trust broke once: the pattern table was written without
 * the `/api/v1` prefix the generated operations carry, so every read with a
 * path parameter fell through to `undefined` and react-query logged "Query
 * data cannot be undefined" while the sweep scanned a state no real backend
 * produces. This pins what a single green sweep cannot: every entry names a
 * complete generated path, every pattern is anchored, and no entry is
 * shadowed by an earlier one.
 *
 * The sweep itself fails a route that issues an unscripted read
 * (./routes.test.tsx); this file keeps the tables honest on their own terms.
 */

import { describe, expect, it } from 'vitest'
import { EXACT, PATTERNS, resolveBody } from './backends.js'

/** The path prefix every generated operation carries. */
const API_PREFIX = '/api/v1/'

/** The pattern source with its regex escapes read back as a path. */
function patternText(pattern: RegExp): string {
  return pattern.source.replace(/\\\//g, '/')
}

/** One concrete URL a table pattern matches, for the resolution checks. */
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
    // The menu and avatar badge render from these two reads, and their
    // suites bind the backend without an unscripted-read reporter: a
    // removed entry would fall through to undefined and no case would fail.
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
