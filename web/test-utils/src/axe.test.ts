/**
 * axe.test.ts -- the pure decisions the axe harness makes without a DOM:
 * the tier the scan runs in, the tags that cannot carry aria-hidden, the
 * overlay family a marker class list names, and the static check that
 * holds each browser determination to the case it names.
 *
 * That check reads the browser source's syntax tree, so the case is located
 * by its full describe path rather than by the first matching text: a name
 * in a comment, a sibling case in a nested describe, and a skipped case
 * are all told apart from the case the declaration means.
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
import ts from 'typescript'
import { describe, expect, it } from 'vitest'
import {
  BROWSER_DETERMINATIONS,
  isBrowserTier,
  isAriaHiddenForbiddenTag,
  overlayKindFromClasses,
} from './axe'
import type { AxeAssertionOptions } from './axe'

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
 * The browser cases a test source declares, by their full describe path.
 * The tree is parsed with the TypeScript compiler, so a call spelled in a
 * comment, a string or a template literal is not a call, and the path is
 * the one the case really sits under rather than the first text that looks
 * like its name.
 */
interface CaseRecord {
  /** The describe names, then the case name, joined by ` > `. */
  readonly path: string
  readonly call: ts.CallExpression
  /** Whether the case or any describe above it is skipped or todo. */
  readonly skipped: boolean
}

/** The callee chain of a call, or null. */
function calleeChain(node: ts.Expression): string[] | null {
  if (ts.isIdentifier(node)) {
    return [node.text]
  }
  if (ts.isPropertyAccessExpression(node)) {
    const base = calleeChain(node.expression)
    return base === null ? null : [...base, node.name.text]
  }
  return null
}

/** Whether an object literal sets `skip` or `todo` to true. */
function optionsSkip(options: ts.ObjectLiteralExpression): boolean {
  return options.properties.some((property) => {
    const name =
      ts.isPropertyAssignment(property) || ts.isShorthandPropertyAssignment(property)
        ? property.name.getText()
        : null
    if (name !== 'skip' && name !== 'todo') {
      return false
    }
    return (
      ts.isShorthandPropertyAssignment(property)
      || (ts.isPropertyAssignment(property)
        && property.initializer.kind === ts.SyntaxKind.TrueKeyword)
    )
  })
}

/**
 * The describe or case a call declares, or null for any other call. The
 * modifiers are read off the chain (`it.skip`, `describe.skipIf`, a
 * `test.each` table), and a case skipped by its options object counts as
 * skipped, so a name alone cannot stand in for a case that never runs.
 */
function declaredCall(
  call: ts.CallExpression,
): { head: 'describe' | 'it'; name: string | null; skipped: boolean } | null {
  let expression: ts.Expression = call.expression
  // `it.each(table)('name', fn)`: the outer call's callee is a call itself.
  while (ts.isCallExpression(expression)) {
    expression = expression.expression
  }
  const chain = calleeChain(expression)
  if (chain === null) {
    return null
  }
  const [head, ...modifiers] = chain
  if (head !== 'describe' && head !== 'it' && head !== 'test') {
    return null
  }
  const [nameArg] = call.arguments
  const name =
    nameArg !== undefined
      && (ts.isStringLiteral(nameArg) || ts.isNoSubstitutionTemplateLiteral(nameArg))
      ? nameArg.text
      : null
  const options = call.arguments.find((arg) => ts.isObjectLiteralExpression(arg))
  const skipped =
    modifiers.includes('skip')
    || modifiers.includes('todo')
    || modifiers.includes('skipIf')
    || (options !== undefined && optionsSkip(options))
  return { head: head === 'describe' ? 'describe' : 'it', name, skipped }
}

/** One call's function body: the block or expression that holds its case. */
function callbackBody(call: ts.CallExpression): ts.ConciseBody | null {
  for (const argument of [...call.arguments].reverse()) {
    if (
      (ts.isArrowFunction(argument) || ts.isFunctionExpression(argument))
      && argument.body !== undefined
    ) {
      return argument.body
    }
  }
  return null
}

/** Every case a source declares, with the describe path it sits under. */
function declaredCases(source: string, filePath = 'constructed.tsx'): CaseRecord[] {
  const sourceFile = ts.createSourceFile(
    filePath,
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  )
  const cases: CaseRecord[] = []
  const visit = (node: ts.Node, path: readonly string[], skippedUpstream: boolean) => {
    if (ts.isCallExpression(node)) {
      const declared = declaredCall(node)
      if (declared !== null) {
        if (declared.head === 'describe') {
          const body = callbackBody(node)
          if (declared.name !== null && body !== null) {
            visit(
              body,
              [...path, declared.name],
              skippedUpstream || declared.skipped,
            )
          }
          return
        }
        if (declared.name !== null) {
          cases.push({
            path: [...path, declared.name].join(' > '),
            call: node,
            skipped: skippedUpstream || declared.skipped,
          })
        }
        return
      }
    }
    ts.forEachChild(node, (child) => visit(child, path, skippedUpstream))
  }
  visit(sourceFile, [], false)
  return cases
}

/** The axe-assertion call inside a case body, or null when none runs. */
function axeCallIn(call: ts.CallExpression): ts.CallExpression | null {
  const body = callbackBody(call)
  if (body === null) {
    return null
  }
  let found: ts.CallExpression | null = null
  const visit = (node: ts.Node) => {
    if (found !== null) {
      return
    }
    if (ts.isCallExpression(node)) {
      const chain = calleeChain(node.expression)
      const name = chain?.at(-1)
      if (name === 'expectNoAxeViolations' || name === 'expectNoAxeViolationsForPage') {
        found = node
        return
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(body)
  return found
}

/** Whether the call passes `decidedInBrowser: '<key>'`. */
function passesDetermination(call: ts.CallExpression, key: string): boolean {
  const [options] = call.arguments
  if (options === undefined || !ts.isObjectLiteralExpression(options)) {
    return false
  }
  return options.properties.some(
    (property) =>
      ts.isPropertyAssignment(property)
      && property.name.getText().replace(/^['"]|['"]$/g, '') === 'decidedInBrowser'
      && ts.isStringLiteral(property.initializer)
      && property.initializer.text === key,
  )
}

/**
 * Holds one declaration to a browser case that runs the axe assertion. The
 * exemption a jsdom scan takes says "the named browser case decides these
 * rules"; a case that never calls the assertion decides nothing, and the
 * jsdom scan would then be exempted by a name alone. The case and every
 * describe above it must run, and the case must pass the determination it
 * is registered under, so the browser tier's own check is wired to it.
 *
 * A call under dead code such as `if (false)` still counts: a static read
 * of the tree does not evaluate conditions (a known limitation).
 */
function assertCaseRunsAxe(
  source: string,
  testName: string,
  decidedInBrowser?: string,
): void {
  const matches = declaredCases(source).filter(
    (record) => record.path === testName,
  )
  if (matches.length === 0) {
    throw new Error(`no browser case is named ${JSON.stringify(testName)}`)
  }
  if (matches.length > 1) {
    throw new Error(
      `more than one browser case is named ${JSON.stringify(testName)}`,
    )
  }
  const [record] = matches
  if (record === undefined) {
    throw new Error(`no browser case is named ${JSON.stringify(testName)}`)
  }
  if (record.skipped) {
    throw new Error(
      `the browser case ${JSON.stringify(testName)} is skipped`,
    )
  }
  const assertion = axeCallIn(record.call)
  if (assertion === null) {
    throw new Error(
      `the browser case ${JSON.stringify(testName)} does not run the axe assertion`,
    )
  }
  if (
    decidedInBrowser !== undefined
    && !passesDetermination(assertion, decidedInBrowser)
  ) {
    throw new Error(
      `the browser case ${JSON.stringify(testName)} does not declare itself as `
        + JSON.stringify(decidedInBrowser),
    )
  }
}

describe('BROWSER_DETERMINATIONS', () => {
  for (const [key, determination] of Object.entries(
    BROWSER_DETERMINATIONS,
  )) {
    it(`${key} names a real browser-tier case that runs the axe assertion`, () => {
      // The exemption a jsdom case takes is only as good as the browser
      // case behind it: a renamed or deleted case, a skipped one, or one
      // that never runs the scan must break here rather than leave the
      // rule unexamined in both tiers.
      expect(determination.file).toMatch(/\.browser\.test\.tsx$/)
      const source = readFileSync(
        join(WEB_ROOT, determination.file),
        'utf8',
      )
      assertCaseRunsAxe(source, determination.testName, key)
    })
  }

  it('rejects a declaration whose browser case does not run the axe assertion', () => {
    // A real browser case in the registry's own files that asserts
    // geometry and never scans: pointing a declaration at it must fail.
    const withoutScan = {
      file: 'packages/admin-ui/src/AccountsPage.browser.test.tsx',
      testName:
        'AccountsPage row actions at 375 in Chromium > pins the account row actions to the table edge while the main columns scroll',
    }
    const source = readFileSync(join(WEB_ROOT, withoutScan.file), 'utf8')
    expect(() => assertCaseRunsAxe(source, withoutScan.testName)).toThrow(
      /does not run the axe assertion/,
    )
  })

  it('rejects a case whose axe assertion appears only in a comment', () => {
    const source = `describe('d', () => {
  it('target', async () => {
    // await expectNoAxeViolations()
    expect(1).toBe(1)
  })
})`
    expect(() => assertCaseRunsAxe(source, 'd > target')).toThrow(
      /does not run the axe assertion/,
    )
  })

  it('rejects a sibling case in a nested describe', () => {
    const source = `describe('d', () => {
  describe('inner', () => {
    it('target', async () => {
      expect(1).toBe(1)
    })
    it('other', async () => {
      await expectNoAxeViolations()
    })
  })
})`
    expect(() => assertCaseRunsAxe(source, 'd > inner > target')).toThrow(
      /does not run the axe assertion/,
    )
  })

  it('rejects a case under a skipped describe', () => {
    const source = `describe.skip('d', () => {
  it('target', async () => {
    await expectNoAxeViolations()
  })
})`
    expect(() => assertCaseRunsAxe(source, 'd > target')).toThrow(/is skipped/)
  })

  it('rejects a case skipped through its options object', () => {
    const source = `describe('d', () => {
  it('target', { skip: true }, async () => {
    await expectNoAxeViolations()
  })
})`
    expect(() => assertCaseRunsAxe(source, 'd > target')).toThrow(/is skipped/)
  })

  it('rejects a top-level case when only a later describe scans', () => {
    const source = `it('target', async () => {
  expect(1).toBe(1)
})
describe('d', () => {
  it('other', async () => {
    await expectNoAxeViolations()
  })
})`
    expect(() => assertCaseRunsAxe(source, 'target')).toThrow(
      /does not run the axe assertion/,
    )
  })

  it('accepts a call in a dead branch as a known limitation', () => {
    // A static read of the tree does not evaluate `if (false)`. This is
    // the one shape the check cannot tell from a live call; it is recorded
    // rather than silently claimed to be caught.
    const source = `describe('d', () => {
  it('target', async () => {
    if (false) await expectNoAxeViolations()
  })
})`
    expect(() => assertCaseRunsAxe(source, 'd > target')).not.toThrow()
  })

  it('rejects a case whose name appears in an earlier comment', () => {
    const source = `describe('d', () => {
  // it('target', ...) used to call expectNoAxeViolations() before the split
  it('target', async () => {
    expect(1).toBe(1)
  })
})`
    expect(() => assertCaseRunsAxe(source, 'd > target')).toThrow(
      /does not run the axe assertion/,
    )
  })

  it('rejects an it.skip case', () => {
    const source = `describe('d', () => {
  it.skip('target', async () => {
    await expectNoAxeViolations()
  })
})`
    expect(() => assertCaseRunsAxe(source, 'd > target')).toThrow(/is skipped/)
  })

  it('rejects a case under a describe.todo', () => {
    const source = `describe.todo('d', () => {
  it('target', async () => {
    await expectNoAxeViolations()
  })
})`
    expect(() => assertCaseRunsAxe(source, 'd > target')).toThrow(/is skipped/)
  })

  it('rejects an it.todo case', () => {
    const source = `describe('d', () => {
  it.todo('target')
})`
    expect(() => assertCaseRunsAxe(source, 'd > target')).toThrow(/is skipped/)
  })

  it('rejects a case under a describe.skipIf', () => {
    const source = `describe.skipIf(false)('d', () => {
  it('target', async () => {
    await expectNoAxeViolations()
  })
})`
    expect(() => assertCaseRunsAxe(source, 'd > target')).toThrow(/is skipped/)
  })

  it('requires the browser case to pass the determination it is registered under', () => {
    const source = `describe('d', () => {
  it('target', async () => {
    await expectNoAxeViolations()
  })
})`
    expect(() =>
      assertCaseRunsAxe(source, 'd > target', 'accountsDetailPanel'),
    ).toThrow(/does not declare itself/)
  })

  it('accepts only a registered determination key', () => {
    // A key is a member of BROWSER_DETERMINATIONS, not any string: if the
    // option were typed as a plain string, a misspelt key would typecheck
    // and the declaration would silently take no exemption. The directive
    // below fails to compile as unused once the type is widened.
    // @ts-expect-error -- 'notRegistered' is not a BROWSER_DETERMINATIONS key
    const options: AxeAssertionOptions = { decidedInBrowser: 'notRegistered' }
    expect(options.decidedInBrowser).toBe('notRegistered')
  })
})
