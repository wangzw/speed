/**
 * axe.test.ts -- the pure decisions the axe harness makes without a DOM:
 * the tier the scan runs in, the tags that cannot carry aria-hidden, the
 * overlay family a marker class list names, and the static check that
 * holds each browser determination to a browser case that really runs the
 * assertion.
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

/**
 * The source text of one browser case: from its `it('name'` line to the
 * block's closing `  })`. Cases in this tier sit at two spaces inside a
 * `describe`, so a nested callback closes deeper (`    })`) and cannot
 * end the slice early. Null when the file has no case by that name.
 */
function caseBody(source: string, testName: string): string | null {
  const marker = `it('${testName}'`
  const start = source.indexOf(marker)
  if (start < 0) {
    return null
  }
  const rest = source.slice(start + marker.length)
  const end = rest.indexOf('\n  })')
  return end < 0 ? rest : rest.slice(0, end)
}

/**
 * Holds one declaration to a browser case that runs the axe assertion.
 * The exemption a jsdom scan takes says "the named browser case decides
 * this rule"; a case that never calls the assertion decides nothing, and
 * the jsdom scan would then be exempted by a name alone.
 */
function assertCaseRunsAxe(source: string, testName: string): void {
  const body = caseBody(source, testName)
  if (body === null) {
    throw new Error(`no browser case is named ${JSON.stringify(testName)}`)
  }
  const assertion = ['expectNoAxeViolations(', 'expectNoAxeViolationsForPage(']
  if (!assertion.some((call) => body.includes(call))) {
    throw new Error(
      `the browser case ${JSON.stringify(testName)} does not run the axe assertion`,
    )
  }
}

describe('BROWSER_DETERMINATIONS', () => {
  for (const [key, determination] of Object.entries(
    BROWSER_DETERMINATIONS,
  )) {
    it(`${key} names a real browser-tier case that runs the axe assertion`, () => {
      // The exemption a jsdom case takes is only as good as the browser
      // case behind it: a renamed or deleted case, or one that never runs
      // the scan, must break here rather than leave the rule unexamined in
      // both tiers.
      expect(determination.file).toMatch(/\.browser\.test\.tsx$/)
      const source = readFileSync(
        join(WEB_ROOT, determination.file),
        'utf8',
      )
      assertCaseRunsAxe(source, determination.testName)
    })
  }

  it('rejects a declaration whose browser case does not run the axe assertion', () => {
    // A real browser case in the registry's own files that asserts
    // geometry and never scans: pointing a declaration at it must fail.
    const withoutScan = {
      file: 'packages/admin-ui/src/AccountsPage.browser.test.tsx',
      testName:
        'pins the account row actions to the table edge while the main columns scroll',
    }
    const source = readFileSync(join(WEB_ROOT, withoutScan.file), 'utf8')
    expect(() => assertCaseRunsAxe(source, withoutScan.testName)).toThrow(
      /does not run the axe assertion/,
    )
  })
})
