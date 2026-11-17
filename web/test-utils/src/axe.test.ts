/**
 * axe.test.ts -- the pure decisions the axe harness makes without a DOM:
 * the environment label the indeterminate report names, the tags that
 * cannot carry aria-hidden, and the overlay family a marker class list
 * names.
 *
 * reportNonCriticalIncomplete interpolates environmentLabel's result into
 * "indeterminate in <label>", so the label is the report's wording for
 * where a rule could not be decided. Mapping a user agent string is pure
 * and needs no DOM, so it is pinned here in the package's node suite:
 * the Chromium tier's user agent, jsdom's (which also carries an engine
 * token, so the jsdom branch must win), and a string that is neither.
 */

import { describe, expect, it } from 'vitest'
import {
  environmentLabel,
  isAriaHiddenForbiddenTag,
  overlayKindFromClasses,
} from './axe'

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

describe('isAriaHiddenForbiddenTag', () => {
  it('exempts the tags the ARIA conformance list forbids hiding', () => {
    for (const tag of ['script', 'STYLE', 'link', 'template', 'meta']) {
      expect(isAriaHiddenForbiddenTag(tag)).toBe(true)
    }
  })

  it('treats a hidden input as forbidden but a visible one as hideable', () => {
    expect(isAriaHiddenForbiddenTag('input', 'hidden')).toBe(true)
    expect(isAriaHiddenForbiddenTag('input', 'text')).toBe(false)
    expect(isAriaHiddenForbiddenTag('INPUT')).toBe(false)
  })

  it('does not exempt ordinary content', () => {
    expect(isAriaHiddenForbiddenTag('div')).toBe(false)
    expect(isAriaHiddenForbiddenTag('button')).toBe(false)
  })
})

describe('overlayKindFromClasses', () => {
  it('names a dialog by its MUI root class', () => {
    expect(overlayKindFromClasses(['MuiModal-root', 'MuiDialog-root'])).toBe(
      'dialog',
    )
  })

  it('names a drawer by its MUI root class', () => {
    expect(overlayKindFromClasses(['MuiModal-root', 'MuiDrawer-root'])).toBe(
      'drawer',
    )
  })

  it('names any other overlay, menu and select included, as other', () => {
    expect(overlayKindFromClasses(['MuiModal-root', 'MuiMenu-root'])).toBe(
      'other',
    )
    expect(overlayKindFromClasses([])).toBe('other')
  })
})
