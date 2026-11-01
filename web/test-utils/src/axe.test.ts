/**
 * axe.test.ts -- the environment label the indeterminate report names.
 *
 * reportNonCriticalIncomplete interpolates environmentLabel's result into
 * "indeterminate in <label>", so the label is the report's wording for
 * where a rule could not be decided. Mapping a user agent string is pure
 * and needs no DOM, so it is pinned here in the package's node suite:
 * the Chromium tier's user agent, jsdom's (which also carries an engine
 * token, so the jsdom branch must win), and a string that is neither.
 */

import { describe, expect, it } from 'vitest'
import { environmentLabel } from './axe'

const CHROMIUM_UA =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 ' +
  '(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'

const JSDOM_UA =
  'Mozilla/5.0 (darwin) AppleWebKit/537.36 (KHTML, like Gecko) jsdom/24.1.0'

const NEITHER_UA = 'curl/8.7.1'

describe('environmentLabel', () => {
  it('names the browser tier', () => {
    expect(environmentLabel(CHROMIUM_UA)).toBe('the browser')
  })

  it('names jsdom even when the user agent also carries a browser token', () => {
    expect(environmentLabel(JSDOM_UA)).toBe('the jsdom')
  })

  it('falls back to an unknown environment for anything else', () => {
    expect(environmentLabel(NEITHER_UA)).toBe('an unknown environment')
  })
})
