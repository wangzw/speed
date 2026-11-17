/**
 * axe.test.ts -- the pure decisions the axe harness makes without a DOM:
 * the tier the scan runs in, the tags that cannot carry aria-hidden, and
 * the overlay family a marker class list names.
 *
 * The tier is read from whether the browser setup installed the real
 * keyboard driver (see axe.ts), so a scan is never handed keys the
 * environment cannot press on the say-so of a user agent a test set for
 * its own subject. The decision is pure over an injected driver, so it is
 * pinned here in the package's node suite.
 */

import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import {
  BROWSER_DETERMINATIONS,
  isBrowserTier,
  isAriaHiddenForbiddenTag,
  overlayKindFromClasses,
} from './axe'

/** The workspace root (`web/`), the base every declared file is relative to. */
const WEB_ROOT = fileURLToPath(new URL('../../', import.meta.url))

const driver = async (): Promise<void> => undefined

describe('isBrowserTier', () => {
  it('names the browser tier when its setup installed the real keyboard', () => {
    expect(isBrowserTier(driver)).toBe(true)
  })

  it('names the jsdom tier when no driver is installed', () => {
    expect(isBrowserTier(undefined)).toBe(false)
  })

  it('reads the installed driver off the global the browser setup writes', () => {
    const host = globalThis as { __speedAxeKeyboardDriver?: () => Promise<void> }
    expect(isBrowserTier()).toBe(false)
    host.__speedAxeKeyboardDriver = driver
    try {
      expect(isBrowserTier()).toBe(true)
    } finally {
      delete host.__speedAxeKeyboardDriver
    }
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

describe('BROWSER_DETERMINATIONS', () => {
  for (const [key, determination] of Object.entries(
    BROWSER_DETERMINATIONS,
  )) {
    it(`${key} names a real browser-tier case`, () => {
      // The exemption a jsdom case takes is only as good as the browser
      // case behind it: a renamed or deleted case must break here rather
      // than leave the rule unexamined in both tiers.
      expect(determination.file).toMatch(/\.browser\.test\.tsx$/)
      const source = readFileSync(
        join(WEB_ROOT, determination.file),
        'utf8',
      )
      expect(source).toContain(`it('${determination.testName}'`)
    })
  }
})
