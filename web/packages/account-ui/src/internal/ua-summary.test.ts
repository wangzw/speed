/**
 * summarizeUserAgent pins detection order and the bounded vocabulary.
 */

import { describe, expect, it } from 'vitest'
import { summarizeUserAgent } from './ua-summary.js'

const MACHINE_STRINGS = ['Mozilla/', 'AppleWebKit', 'Gecko', 'curl/', 'Safari/']

const CORPUS: ReadonlyArray<readonly [string, string]> = [
  [
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36',
    'Chrome · macOS',
  ],
  [
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15',
    'Safari · macOS',
  ],
  [
    'Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1',
    'Safari · iPhone',
  ],
  [
    'Mozilla/5.0 (iPad; CPU OS 14_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.7 Mobile/15E148 Safari/604.1',
    'Safari · iPad',
  ],
  [
    'Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Mobile Safari/537.36',
    'Chrome · Android',
  ],
  [
    'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 Edg/126.0.2592.61',
    'Edge · Windows',
  ],
  [
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:127.0) Gecko/20100101 Firefox/127.0',
    'Firefox · macOS',
  ],
  [
    'Mozilla/5.0 (Android 14; Mobile; rv:126.0) Gecko/126.0 Firefox/126.0',
    'Firefox · Android',
  ],
  [
    'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36 OPR/111.0.0.0',
    'Opera · Windows',
  ],
  [
    'Mozilla/5.0 (Linux; Android 13; SM-G991B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/23.0 Chrome/115.0.0.0 Mobile Safari/537.36',
    'Samsung Internet · Android',
  ],
  [
    'Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36',
    'Chrome · ChromeOS',
  ],
  [
    'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36',
    'Chrome · Linux',
  ],
  ['curl/8.4.0', 'curl'],
]

describe('summarizeUserAgent', () => {
  it.each(CORPUS)('summarises %s', (ua, expected) => {
    expect(summarizeUserAgent(ua)).toBe(expected)
  })

  it('answers null when nothing in the string is recognised, never the raw string', () => {
    expect(summarizeUserAgent('')).toBeNull()
    expect(summarizeUserAgent('  ')).toBeNull()
    expect(summarizeUserAgent('Go-http-client/1.1')).toBeNull()
    expect(summarizeUserAgent('SomeWeirdClient/1.0 (unknown platform)')).toBeNull()
    expect(summarizeUserAgent('Mozilla/5.0 (X11; FreeBSD amd64)')).toBeNull()
  })

  it('never lets a raw-UA machine substring into any answer the corpus parses', () => {
    for (const [ua] of CORPUS) {
      const summary = summarizeUserAgent(ua)
      expect(summary).not.toBeNull()
      for (const token of MACHINE_STRINGS) {
        expect(summary, `"${summary}" (from "${ua}") contains ${token}`).not.toContain(
          token,
        )
      }
    }
  })
})
