/**
 * Structural guard: every workspace package that runs tests takes its
 * Vitest options from the shared preset (web/vitest.shared.ts).
 *
 * A package that hand-writes its test block can silently run on Vitest's
 * 5s default, which the parallel workspace run (`pnpm -r test`) exceeds
 * on a loaded machine -- the failure lands on the clock, not on an
 * assertion. This suite makes that omission red at the source: each
 * package with a `test` script must import `sharedTestOptions` from the
 * preset, must not pin its own `testTimeout` or `hookTimeout`, and its
 * exported config must carry the preset's budgets and coverage options.
 *
 * The preset also owns the boundary between the unit tier and the
 * browser tier: `*.browser.test.tsx` cases run only through the root
 * `make web-browser` entry, so the preset must keep excluding them from
 * every package's `test` leg. The last case pins that exclusion.
 *
 * The guard holds the budgets themselves too, not just their provenance:
 * a preset that lowers its shared default, a setup that lowers the async
 * budget, or a case that pins its own budget below the shared default is
 * red here. Comparing a package's config with the preset alone would
 * accept a preset lowered along with it. The floors -- the Vitest case
 * budget, the Playwright case budget and the async wait budget -- are read
 * from `web/scripts/test-time-floors.json`, a file that holds only floors,
 * so lowering a default cannot lower its own floor in the same edit. That
 * file is the one entry point for an intentional floor change: a change to
 * it carries its reason in the commit message. The values are compared
 * unscaled, so a caller's GATE_TIME_SCALE cannot change this tree's verdict.
 *
 * Vitest's `vi.waitFor` and `vi.waitUntil` are the waits Testing
 * Library's budget does not reach: neither has a global default, and both
 * use a fixed one second per call. Every `vi.waitFor` must go through
 * `waitForCondition` (@speed/test-utils/wait), which supplies the shared
 * budget, and `vi.waitUntil` is forbidden. A scanned source is red here
 * for any member access to either name -- a call, a bracket-access call,
 * optional chaining, a destructured alias (`const { waitFor } = vi`, a
 * parameter default included), an argument, a property value, an arrow's
 * return, or a reference kept uncalled (`const wait = vi.waitFor`) --
 * because outside `wait.ts` no use of either is permitted. The scan
 * follows the name through the wrappers that do not change its value (a
 * parenthesized expression, a non-null assertion, a type assertion), an
 * aliased `vi` import (`import { vi as x } from 'vitest'`), a local alias
 * (`const v = vi`) and an alias of an alias, and a direct
 * `import { waitFor } from 'vitest'`.
 *
 * The scan covers the test sources and the test-support sources the suites
 * run on: every package's `test-utils/`, `web/test-utils/src/` (the
 * browser-tier setup included), `starters/demo/web/src/test-utils/` and the
 * demo's `src/a11y/` support modules, plus the root leg's tests under
 * `web/eslint-rules/` and `web/scripts/`. Only `web/test-utils/src/wait.ts`
 * is exempt, by its exact path, because it is the helper that supplies the
 * budget.
 *
 * Four shapes are outside what a static read of the syntax tree can
 * follow: a key that is not a literal (`vi[name]`, a concatenation, a
 * template literal with a substitution, or a computed destructuring key
 * such as `const { [name]: w } = vi`), a `vi` reached through a namespace
 * import or a dynamic import (`import * as vitest`), a `vi` passed into a
 * helper that is free to access it, and a shadowed local named `vi` (read
 * as Vitest's, a false positive rather than a bypass).
 * A call in dead code such as `if (false)` counts as a use; the guard does
 * not evaluate conditions. Testing Library's own `waitFor` and `findBy*`
 * queries are outside this guard: they wait under the `asyncUtilTimeout`
 * budget the setup configures.
 *
 * The demo host (starters/demo/web) is a copyable application whose
 * repository-internal test wiring is dropped on copy out, so it is not
 * checked for the preset's provenance. Its test sources are scanned for a
 * narrowed case budget and a bare wait, and its setup for the async
 * budget, because the copy carries the same wiring by hand.
 */
import {
  existsSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, extname, join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import ts from 'typescript'
import { describe, expect, it } from 'vitest'
import {
  gateTimeScale,
  SHARED_TIMEOUT_MS,
  sharedCoverage,
  sharedReporters,
  sharedTestOptions,
} from '../vitest.shared'
import { ASYNC_UTIL_TIMEOUT_MS } from '../test-utils/src/gate-time'
import { ASYNC_UTIL_TIMEOUT_MS as DEMO_ASYNC_UTIL_TIMEOUT_MS } from '../../starters/demo/web/src/test-utils/gate-time'

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..')
const demoWebRoot = join(webRoot, '..', 'starters', 'demo', 'web')

/**
 * The floors the shared defaults must clear, validated. They live outside
 * the preset so a lowered default cannot lower its own floor in the same
 * edit; the preset holds only the default, and that file holds only the
 * floors. It is the one entry point for an intentional floor change, so a
 * change to it carries its reason in the commit message. A missing file, a
 * missing key or a non-positive, non-finite value throws here rather than
 * leaving the comparison to read `undefined` and pass.
 */
function readTimeFloors(path) {
  const floors = JSON.parse(readFileSync(path, 'utf8'))
  for (const key of [
    'vitestCaseTimeoutMs',
    'playwrightCaseTimeoutMs',
    'asyncUtilTimeoutMs',
  ]) {
    const value = floors[key]
    if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) {
      throw new Error(`${path}: ${key} must be a positive number of milliseconds`)
    }
  }
  return floors
}

const TIME_FLOORS = readTimeFloors(
  join(webRoot, 'scripts', 'test-time-floors.json'),
)
const SHARED_TIMEOUT_FLOOR_MS = TIME_FLOORS.vitestCaseTimeoutMs

/** Every workspace package directory: packages/* plus test-utils. */
const packageDirs = [
  ...readdirSync(join(webRoot, 'packages'), { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => join(webRoot, 'packages', entry.name)),
  join(webRoot, 'test-utils'),
]

/** A package is in scope when its manifest declares a `test` script. */
const testPackages = packageDirs
  .map((dir) => {
    const manifest = JSON.parse(
      readFileSync(join(dir, 'package.json'), 'utf8'),
    )
    return { dir, name: manifest.name, test: manifest.scripts?.test }
  })
  .filter((pkg) => typeof pkg.test === 'string')

/** The script kind a test source is parsed as, by its extension. */
const SCRIPT_KINDS = {
  '.ts': ts.ScriptKind.TS,
  '.mts': ts.ScriptKind.TS,
  '.cts': ts.ScriptKind.TS,
  '.tsx': ts.ScriptKind.TSX,
  '.js': ts.ScriptKind.JS,
  '.mjs': ts.ScriptKind.JS,
  '.cjs': ts.ScriptKind.JS,
  '.jsx': ts.ScriptKind.JSX,
}

/**
 * The parser kind for a path. The constructed sources in this suite's own
 * cases have no file of their own and are parsed as TSX, which accepts both
 * the JSX shapes and the plain code they hold.
 */
function scriptKindOf(filePath) {
  return SCRIPT_KINDS[extname(filePath)] ?? ts.ScriptKind.TSX
}

/**
 * The syntax tree of one test source, parsed with the TypeScript compiler.
 * The tree is what tells a use of a name from its spelling in a comment, a
 * string, a template literal, a regular expression or JSX text; no
 * hand-written lexer is involved.
 *
 * A source the parser reports a diagnostic for is an error naming the file,
 * never a skipped file. A guard that passed a source it could not read
 * would leave the blind spot it exists to close.
 */
function parseTestSource(source, filePath) {
  const sourceFile = ts.createSourceFile(
    filePath,
    source,
    ts.ScriptTarget.Latest,
    true,
    scriptKindOf(filePath),
  )
  const diagnostics = sourceFile.parseDiagnostics
  if (!Array.isArray(diagnostics)) {
    throw new Error(
      `${filePath}: the TypeScript parser exposed no diagnostics, so the `
        + 'guard cannot tell a parse failure from a clean source',
    )
  }
  const [diagnostic] = diagnostics
  if (diagnostic !== undefined) {
    throw new Error(
      `${filePath}: cannot parse the test source: `
        + ts.flattenDiagnosticMessageText(diagnostic.messageText, ' '),
    )
  }
  return sourceFile
}

/** Every node of a tree, parents before children. */
function forEachNode(node, visit) {
  visit(node)
  ts.forEachChild(node, (child) => forEachNode(child, visit))
}

/**
 * The name a property or binding element is written under, or null.
 *
 * A computed key counts when its expression is a literal: `['waitFor']`,
 * `[0]` and `` [`waitFor`] `` name the same property as the uncomputed
 * forms. A computed key built from a variable or a substitution stays
 * null; that shape is a known limitation of the guard.
 */
function writtenName(name) {
  if (
    ts.isIdentifier(name)
    || ts.isStringLiteral(name)
    || ts.isNumericLiteral(name)
  ) {
    return name.text
  }
  if (ts.isComputedPropertyName(name)) {
    const key = name.expression
    if (
      ts.isStringLiteral(key)
      || ts.isNumericLiteral(key)
      || ts.isNoSubstitutionTemplateLiteral(key)
    ) {
      return key.text
    }
  }
  return null
}

/** The root and the members of a property-access chain, or null. */
function calleeChain(node) {
  if (ts.isIdentifier(node)) {
    return [node.text]
  }
  if (ts.isPropertyAccessExpression(node)) {
    const base = calleeChain(node.expression)
    return base === null ? null : [...base, node.name.text]
  }
  return null
}

/**
 * The expression a node denotes, with the wrappers that do not change its
 * value peeled off: parentheses, non-null assertions and type assertions.
 */
function unwrapExpression(node) {
  let current = node
  while (
    ts.isParenthesizedExpression(current)
    || ts.isNonNullExpression(current)
    || ts.isAsExpression(current)
    || ts.isTypeAssertionExpression(current)
    || ts.isSatisfiesExpression(current)
  ) {
    current = current.expression
  }
  return current
}

/** The two Vitest waits with a fixed one-second default and no global one. */
const WAIT_NAMES = new Set(['waitFor', 'waitUntil'])

/**
 * Every local name that denotes the Vitest `vi` object: `vi`, an aliased
 * named import (`import { vi as x } from 'vitest'`), a variable holding
 * one of those (`const v = vi`), and an alias of an alias. Read to a fixed
 * point so a chain of aliases resolves however it is ordered.
 */
function viNames(sourceFile) {
  const names = new Set(['vi'])
  forEachNode(sourceFile, (node) => {
    if (
      ts.isImportDeclaration(node)
      && ts.isStringLiteral(node.moduleSpecifier)
      && node.moduleSpecifier.text === 'vitest'
      && ts.isNamedImports(node.importClause?.namedBindings)
    ) {
      for (const element of node.importClause.namedBindings.elements) {
        if ((element.propertyName ?? element.name).text === 'vi') {
          names.add(element.name.text)
        }
      }
    }
  })
  let changed = true
  while (changed) {
    changed = false
    forEachNode(sourceFile, (node) => {
      if (
        ts.isVariableDeclaration(node)
        && ts.isIdentifier(node.name)
        && node.initializer !== undefined
      ) {
        const value = unwrapExpression(node.initializer)
        if (
          ts.isIdentifier(value)
          && names.has(value.text)
          && !names.has(node.name.text)
        ) {
          names.add(node.name.text)
          changed = true
        }
      }
      if (
        ts.isBinaryExpression(node)
        && node.operatorToken.kind === ts.SyntaxKind.EqualsToken
        && ts.isIdentifier(node.left)
      ) {
        const value = unwrapExpression(node.right)
        if (
          ts.isIdentifier(value)
          && names.has(value.text)
          && !names.has(node.left.text)
        ) {
          names.add(node.left.text)
          changed = true
        }
      }
    })
  }
  return names
}

/** Whether a node names one of `names`, through the value wrappers. */
function isNamedReference(node, names) {
  const value = unwrapExpression(node)
  return ts.isIdentifier(value) && names.has(value.text)
}

/**
 * `vi.waitFor`, `vi.waitUntil`, `vi['waitFor']`, `vi?.['waitUntil']`, and
 * the same accesses on an alias of `vi`, whichever wrappers the base
 * carries (`(vi)`, `vi!`, `vi as typeof vi`).
 */
function isWaitAccess(node, names) {
  if (ts.isPropertyAccessExpression(node)) {
    return isNamedReference(node.expression, names)
      && WAIT_NAMES.has(node.name.text)
  }
  if (ts.isElementAccessExpression(node)) {
    const key = node.argumentExpression
    return isNamedReference(node.expression, names)
      && (ts.isStringLiteral(key)
        || ts.isNoSubstitutionTemplateLiteral(key))
      && WAIT_NAMES.has(key.text)
  }
  return false
}

/** Whether an object binding pattern binds one of the two wait names. */
function bindsWaitName(pattern) {
  return (
    ts.isObjectBindingPattern(pattern)
    && pattern.elements.some(
      (element) =>
        ts.isBindingElement(element)
        && WAIT_NAMES.has(
          writtenName(element.propertyName ?? element.name) ?? '',
        ),
    )
  )
}

/**
 * `const { waitFor } = vi`, renamed or not, with a parenthesized,
 * asserted or non-null right-hand side and an alias of `vi` there too.
 * A parameter default (`function f({ waitFor } = vi) {}`) is the same
 * binding and is read through the parameter as well.
 */
function isDestructuredFromVi(node, names) {
  if (
    ts.isVariableDeclaration(node)
    && bindsWaitName(node.name)
    && node.initializer !== undefined
    && isNamedReference(node.initializer, names)
  ) {
    return true
  }
  return (
    ts.isParameter(node)
    && bindsWaitName(node.name)
    && node.initializer !== undefined
    && isNamedReference(node.initializer, names)
  )
}

/** `({ waitFor } = vi)`, renamed or not, through the same wrappers. */
function isAssignedFromVi(node, names) {
  if (
    !ts.isBinaryExpression(node)
    || node.operatorToken.kind !== ts.SyntaxKind.EqualsToken
    || !isNamedReference(node.right, names)
  ) {
    return false
  }
  const target = unwrapExpression(node.left)
  return ts.isObjectLiteralExpression(target)
    && target.properties.some(
      (property) =>
        (ts.isShorthandPropertyAssignment(property)
          || ts.isPropertyAssignment(property))
        && WAIT_NAMES.has(writtenName(property.name) ?? ''),
    )
}

/**
 * A direct `import { waitFor } from 'vitest'`: naming the wait at the
 * import reaches the same fixed default as `vi.waitFor` and bypasses every
 * member-access check above.
 */
function importsWaitDirectly(node) {
  return (
    ts.isImportDeclaration(node)
    && ts.isStringLiteral(node.moduleSpecifier)
    && node.moduleSpecifier.text === 'vitest'
    && ts.isNamedImports(node.importClause?.namedBindings)
    && node.importClause.namedBindings.elements.some((element) =>
      WAIT_NAMES.has((element.propertyName ?? element.name).text))
  )
}

/** Whether any call in a tree names `name`. */
function hasCallNamed(sourceFile, name) {
  let found = false
  forEachNode(sourceFile, (node) => {
    if (
      ts.isCallExpression(node)
      && ts.isIdentifier(node.expression)
      && node.expression.text === name
    ) {
      found = true
    }
  })
  return found
}

/** Whether any object property in a tree is written under `name`. */
function hasPropertyNamed(sourceFile, name) {
  let found = false
  forEachNode(sourceFile, (node) => {
    if (
      (ts.isPropertyAssignment(node)
        || ts.isShorthandPropertyAssignment(node)
        || ts.isMethodDeclaration(node)
        || ts.isPropertyDeclaration(node))
      && writtenName(node.name) === name
    ) {
      found = true
    }
  })
  return found
}

/** The initializer of the first object property written under `name`. */
function propertyInitializer(sourceFile, name) {
  let found = null
  forEachNode(sourceFile, (node) => {
    if (
      found === null
      && ts.isPropertyAssignment(node)
      && writtenName(node.name) === name
    ) {
      found = node.initializer
    }
  })
  return found
}

/** Whether a tree imports from a specifier matching `pattern`. */
function importsFrom(sourceFile, pattern) {
  let found = false
  forEachNode(sourceFile, (node) => {
    if (
      ts.isImportDeclaration(node)
      && ts.isStringLiteral(node.moduleSpecifier)
      && pattern.test(node.moduleSpecifier.text)
    ) {
      found = true
    }
  })
  return found
}

/**
 * Whether the `reporters` property is the shared helper's result, with the
 * given base reporter when one is named.
 */
function reportersComeFromShared(sourceFile, base) {
  const initializer = propertyInitializer(sourceFile, 'reporters')
  if (
    initializer === null
    || !ts.isCallExpression(initializer)
    || !ts.isIdentifier(initializer.expression)
    || initializer.expression.text !== 'sharedReporters'
  ) {
    return false
  }
  if (base === undefined) {
    return initializer.arguments.length === 0
  }
  const [argument] = initializer.arguments
  return initializer.arguments.length === 1
    && ts.isStringLiteral(argument)
    && argument.text === base
}

/** The calls that declare a case, hook or suite. */
const CASE_HEADS = new Set([
  'it',
  'test',
  'beforeEach',
  'afterEach',
  'beforeAll',
  'afterAll',
  'describe',
])

/**
 * A constant millisecond budget: a numeric literal, or factors joined by `*`
 * (`2 * 60_000`), optionally parenthesized. Anything that names a value is
 * not a constant, so a variable is not read as its first factor.
 */
function constantMs(node) {
  if (ts.isNumericLiteral(node)) {
    return Number(node.text.replaceAll('_', ''))
  }
  if (ts.isParenthesizedExpression(node)) {
    return constantMs(node.expression)
  }
  if (
    ts.isBinaryExpression(node)
    && node.operatorToken.kind === ts.SyntaxKind.AsteriskToken
  ) {
    const left = constantMs(node.left)
    const right = constantMs(node.right)
    if (left !== null && right !== null) {
      return left * right
    }
  }
  return null
}

/** The `timeout` of an options object, when it is a constant. */
function timeoutOf(options) {
  for (const property of options.properties) {
    if (
      ts.isPropertyAssignment(property)
      && writtenName(property.name) === 'timeout'
    ) {
      return constantMs(property.initializer)
    }
  }
  return null
}

/** The budgets below `floor` carried by one case call. */
function caseCallBudgets(call, floor) {
  const lowered = []
  const options = call.arguments[1]
  if (options !== undefined && ts.isObjectLiteralExpression(options)) {
    const ms = timeoutOf(options)
    if (ms !== null && ms !== 0 && ms < floor) {
      lowered.push(ms)
    }
  }
  // A trailing budget needs at least two arguments: `it(name, 2000)` is one,
  // while a lone numeric argument (`it.runIf(1)`) is a condition or a table.
  const trailing = call.arguments.at(-1)
  if (
    call.arguments.length >= 2
    && trailing !== undefined
    && !ts.isObjectLiteralExpression(trailing)
  ) {
    const ms = constantMs(trailing)
    if (ms !== null && ms !== 0 && ms < floor) {
      lowered.push(ms)
    }
  }
  return lowered
}

/**
 * The per-case, per-hook and per-suite budgets in one test source that sit
 * below `floor`. Four forms are read from the syntax tree: a trailing numeric
 * budget (`it(name, fn, 2000)`, `beforeEach(fn, 2000)`), an options object in
 * the second argument (`it(name, { timeout }, fn)`, `describe(name, {
 * timeout }, fn)`), the call an `it.each(table)`/`test.each(table)` returns,
 * and `test.setTimeout(ms)`. A case budget of 0 means no limit in both Vitest
 * and Playwright and passes. Waits such as `waitFor(..., { timeout })` and
 * `findBy*(..., { timeout })` are not case budgets and are left alone.
 */
function lowCaseTimeouts(source, floor, filePath = 'constructed.tsx') {
  const sourceFile = parseTestSource(source, filePath)
  const lowered = []
  forEachNode(sourceFile, (node) => {
    if (!ts.isCallExpression(node)) {
      return
    }
    const chain = calleeChain(node.expression)
    if (chain === null || !CASE_HEADS.has(chain[0])) {
      return
    }
    // `test.setTimeout(ms)` is a case budget of its own, read below; it does
    // not take case arguments that are budgets.
    if (chain.length === 2 && chain[0] === 'test' && chain[1] === 'setTimeout') {
      return
    }
    // `it.each(table)` returns a function that is then called with the case.
    // The table is not a budget, so the call it returns is inspected instead.
    if (chain.at(-1) === 'each') {
      if (ts.isCallExpression(node.parent) && node.parent.expression === node) {
        lowered.push(...caseCallBudgets(node.parent, floor))
      }
      return
    }
    lowered.push(...caseCallBudgets(node, floor))
  })
  forEachNode(sourceFile, (node) => {
    if (!ts.isCallExpression(node)) {
      return
    }
    const chain = calleeChain(node.expression)
    if (
      chain === null
      || chain.length !== 2
      || chain[0] !== 'test'
      || chain[1] !== 'setTimeout'
    ) {
      return
    }
    const [argument] = node.arguments
    const ms = argument === undefined ? null : constantMs(argument)
    if (ms !== null && ms !== 0 && ms < floor) {
      lowered.push(ms)
    }
  })
  return lowered
}

/**
 * The uses of `vi.waitFor` or `vi.waitUntil` in a scanned source that
 * bypass the shared budget. Outside `wait.ts` there is no permitted use of
 * either name, so every member access counts, whatever becomes of it: a
 * call, an argument, a property value, an arrow's return or a reference
 * kept for later. Optional chaining and bracket access are the same
 * access, an alias of `vi` names the same object, and a direct
 * `import { waitFor } from 'vitest'` is the same wait. A destructuring
 * that binds either name from `vi` -- as a declaration, an assignment or a
 * parameter default -- is listed too. The syntax tree tells a use from
 * prose: a comment, string, template literal, regular expression or JSX
 * text that spells a wait out is not one.
 */
function bareWaitUses(source, filePath = 'constructed.tsx') {
  const sourceFile = parseTestSource(source, filePath)
  const names = viNames(sourceFile)
  const uses = []
  forEachNode(sourceFile, (node) => {
    if (
      isWaitAccess(node, names)
      || isDestructuredFromVi(node, names)
      || isAssignedFromVi(node, names)
      || importsWaitDirectly(node)
    ) {
      uses.push(node.pos)
    }
  })
  return uses
}

/**
 * The argument a setup hands `asyncBudgetMs` for its async timeout, or null
 * when it names no such budget. The argument must be the caller's scale --
 * `gateTimeScale()`, or the browser tier's injected
 * `__SPEED_GATE_TIME_SCALE__` -- rather than a literal: a literal pins the
 * budget on the setup's own value and drops the callers' scale, and even
 * the literal 1 would leave the floor in place while ignoring a caller who
 * raised it.
 */
function asyncBudgetScale(source, filePath = 'constructed.ts') {
  const sourceFile = parseTestSource(source, filePath)
  const initializer = propertyInitializer(sourceFile, 'asyncUtilTimeout')
  if (
    initializer === null
    || !ts.isCallExpression(initializer)
    || !ts.isIdentifier(initializer.expression)
    || initializer.expression.text !== 'asyncBudgetMs'
  ) {
    return null
  }
  const [argument] = initializer.arguments
  return argument === undefined ? null : argument.getText(sourceFile)
}

/** The async budget a setup file names, read from `path` so a parse failure
 * names the real file rather than the constructed placeholder. */
function setupAsyncBudgetScale(path) {
  return asyncBudgetScale(readFileSync(path, 'utf8'), path)
}

/** The expressions that carry the caller's scale into a setup. */
const SCALE_SOURCE = /^(?:gateTimeScale\(\)|__SPEED_GATE_TIME_SCALE__)$/

/** The source extensions a scan reads. */
const SOURCE_FILE = /\.(?:[cm]?[jt]sx?)$/

/** Every script under `dir`, skipping installed and built output. */
function scriptSources(dir) {
  const sources = []
  const walk = (current) => {
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      if (entry.name === 'node_modules' || entry.name === 'dist') {
        continue
      }
      const path = join(current, entry.name)
      if (entry.isDirectory()) {
        walk(path)
      } else if (SOURCE_FILE.test(entry.name)) {
        sources.push(path)
      }
    }
  }
  walk(dir)
  return sources
}

/** Every test source under `dir`, skipping installed and built output. */
function testSources(dir) {
  return scriptSources(dir).filter((path) =>
    /\.test\.(?:[cm]?[jt]sx?)$/.test(path))
}

/**
 * The test-support directories the wait rule scans: each package's
 * `test-utils/`, the shared `web/test-utils/src/` (the browser tier's
 * setup included), the demo host's own copy and its `src/a11y/` support
 * modules. They are derived from the package roster and the fixed roots,
 * so a new helper file is in scope without a second list to maintain.
 */
function testSupportDirs() {
  const dirs = []
  for (const pkgDir of packageDirs) {
    const dir = join(pkgDir, 'test-utils')
    if (existsSync(dir)) {
      dirs.push(dir)
    }
  }
  dirs.push(join(webRoot, 'test-utils', 'src'))
  dirs.push(join(demoWebRoot, 'src', 'test-utils'))
  dirs.push(join(demoWebRoot, 'src', 'a11y'))
  return dirs
}

/**
 * The one source allowed to call `vi.waitFor`: the helper that supplies the
 * shared budget. An exact path, so no other file is exempted with it.
 */
const WAIT_HELPER = join(webRoot, 'test-utils', 'src', 'wait.ts')

/**
 * The async defaults that sit below the async floor `floors` names. Both
 * copies (the workspace's and the demo host's) carry the constant by hand,
 * and each is held to the floor unscaled, like the preset's test budget.
 */
function asyncBudgetsBelowFloor(floors) {
  return [ASYNC_UTIL_TIMEOUT_MS, DEMO_ASYNC_UTIL_TIMEOUT_MS].filter(
    (ms) => ms < floors.asyncUtilTimeoutMs,
  )
}

describe('workspace test configuration', () => {
  it('finds the packages that run tests', () => {
    expect(testPackages.length).toBeGreaterThan(0)
  })

  it('excludes the browser-tier cases from the unit preset', () => {
    const options = sharedTestOptions()
    expect(options.exclude).toContain('**/*.browser.test.tsx')
    // Naming `exclude` replaces Vitest's defaults, so the preset must
    // keep them too -- which it derives from configDefaults rather than
    // restates.
    expect(options.exclude).toContain('**/node_modules/**')
  })

  it('gives the root eslint-rules/scripts leg the shared preset', () => {
    // Those tests live outside every package, so no package config covers
    // them; without a config at the web root the leg runs on Vitest's
    // fixed 5s default.
    const configPath = join(webRoot, 'vitest.config.ts')
    const sourceFile = parseTestSource(readFileSync(configPath, 'utf8'), configPath)
    expect(hasCallNamed(sourceFile, 'sharedTestOptions')).toBe(true)
    expect(hasPropertyNamed(sourceFile, 'testTimeout')).toBe(false)
    expect(hasPropertyNamed(sourceFile, 'hookTimeout')).toBe(false)
  })

  it('fixes the test reporter so the log does not depend on the runner', () => {
    // Vitest 4 picks MinimalReporter (silent "passed-only") when std-env
    // reports an agent environment, which drops the console output of
    // passing cases. A pinned reporter is what keeps a warning from
    // appearing only when a person runs the gate. A pinned list also
    // stops Vitest from appending its own github-actions reporter, so the
    // shared helper appends it under GitHub Actions instead.
    expect(sharedReporters('default', {})).toBe('default')
    expect(sharedReporters('default', { GITHUB_ACTIONS: 'true' })).toEqual([
      'default',
      'github-actions',
    ])
    expect(sharedReporters('verbose', {})).toBe('verbose')
    expect(sharedReporters('verbose', { GITHUB_ACTIONS: 'true' })).toEqual([
      'verbose',
      'github-actions',
    ])
    expect(sharedTestOptions().reporters).toEqual(sharedReporters())
    const configPath = join(demoWebRoot, 'vitest.config.ts')
    expect(
      reportersComeFromShared(
        parseTestSource(readFileSync(configPath, 'utf8'), configPath),
      ),
    ).toBe(true)
    // The a11y sweep's verbose reporter is the other fixed choice; the
    // singular CLI spelling is a different key, so the config keeps the
    // plural one.
    const a11yPath = join(demoWebRoot, 'vitest.a11y.config.ts')
    const a11yFile = parseTestSource(readFileSync(a11yPath, 'utf8'), a11yPath)
    expect(reportersComeFromShared(a11yFile, 'verbose')).toBe(true)
    expect(hasPropertyNamed(a11yFile, 'reporter')).toBe(false)
  })

  it('keeps the shared case budget at or above its floor', () => {
    // Compared unscaled: SHARED_TIMEOUT_MS is the preset's constant, so a
    // caller's GATE_TIME_SCALE cannot change this tree's verdict. The
    // options must then be that constant times the scale.
    expect(SHARED_TIMEOUT_MS).toBeGreaterThanOrEqual(SHARED_TIMEOUT_FLOOR_MS)
    const options = sharedTestOptions()
    expect(options.testTimeout).toBe(SHARED_TIMEOUT_MS * gateTimeScale())
    expect(options.hookTimeout).toBe(SHARED_TIMEOUT_MS * gateTimeScale())
  })

  it('refuses a floor file that names no floor', () => {
    const dir = mkdtempSync(join(tmpdir(), 'test-time-floors-'))
    try {
      const path = join(dir, 'test-time-floors.json')
      writeFileSync(path, JSON.stringify({ vitestCaseTimeoutMs: 60_000 }))
      expect(() => readTimeFloors(path)).toThrow(/playwrightCaseTimeoutMs/)
      writeFileSync(
        path,
        JSON.stringify({
          vitestCaseTimeoutMs: 0,
          playwrightCaseTimeoutMs: 120_000,
          asyncUtilTimeoutMs: 30_000,
        }),
      )
      expect(() => readTimeFloors(path)).toThrow(/vitestCaseTimeoutMs/)
      // `1e999` parses to Infinity, which `<= 0` alone would let through.
      writeFileSync(
        path,
        '{"vitestCaseTimeoutMs": 60000, "playwrightCaseTimeoutMs": 120000, '
          + '"asyncUtilTimeoutMs": 1e999}',
      )
      expect(() => readTimeFloors(path)).toThrow(/asyncUtilTimeoutMs/)
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })

  it('takes the async floor from the floor file', () => {
    // The guard's comparison must read the floor the file names. Raising
    // it above the shared default makes that default an offender; a guard
    // that compared against a hardcoded floor would still clear it.
    const dir = mkdtempSync(join(tmpdir(), 'test-time-floors-'))
    try {
      const path = join(dir, 'test-time-floors.json')
      const raisedFloorMs = ASYNC_UTIL_TIMEOUT_MS + 15_000
      writeFileSync(
        path,
        JSON.stringify({
          vitestCaseTimeoutMs: 60_000,
          playwrightCaseTimeoutMs: 120_000,
          asyncUtilTimeoutMs: raisedFloorMs,
        }),
      )
      const floors = readTimeFloors(path)
      expect(floors.asyncUtilTimeoutMs).toBe(raisedFloorMs)
      expect(asyncBudgetsBelowFloor(floors)).toEqual([
        ASYNC_UTIL_TIMEOUT_MS,
        DEMO_ASYNC_UTIL_TIMEOUT_MS,
      ])
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })

  it('keeps the async budget at or above its floor', () => {
    // Both copies carry the constant by hand; each is held to the floor
    // unscaled, like the preset's test budget. The floor compared against is
    // the one the floor file names.
    expect(asyncBudgetsBelowFloor(TIME_FLOORS)).toEqual([])
    const setups = [
      join(webRoot, 'test-utils/src/setup.ts'),
      join(webRoot, 'test-utils/src/browser-setup.ts'),
      join(demoWebRoot, 'src/test-utils/setup.ts'),
    ]
    for (const path of setups) {
      const scale = setupAsyncBudgetScale(path)
      expect(
        scale,
        `${path} names no async budget for the guard to check`,
      ).not.toBeNull()
      expect(
        scale,
        `${path} pins the async budget instead of taking the caller's scale`,
      ).toMatch(SCALE_SOURCE)
    }
  })

  it('names the setup file it cannot parse', () => {
    // The setups loop reads and parses real files; a setup that stops
    // parsing must be reported under its own path, not under the
    // placeholder asyncBudgetScale uses for constructed sources. A call
    // site that reads the file without handing over the path would leave
    // the failing setup unidentified.
    const dir = mkdtempSync(join(tmpdir(), 'workspace-test-setup-'))
    try {
      const path = join(dir, 'setup.ts')
      writeFileSync(path, 'configure({ asyncUtilTimeout: asyncBudgetMs(')
      expect(() => setupAsyncBudgetScale(path)).toThrow(path)
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })

  /** Every test source the budgets and the wait rule apply to. */
  const scopedTestSources = [
    ...testPackages.flatMap((pkg) => testSources(pkg.dir)),
    ...testSources(demoWebRoot),
    ...testSources(join(webRoot, 'eslint-rules')),
    ...testSources(join(webRoot, 'scripts')),
  ]

  /**
   * Every source the wait rule scans: the case files and the test-support
   * sources the cases run on. `wait.ts` is the only exemption, by exact
   * path, because it is where the budget is supplied.
   */
  const scopedWaitSources = [
    ...new Set([
      ...scopedTestSources,
      ...testSupportDirs().flatMap(scriptSources),
    ]),
  ].filter((source) => source !== WAIT_HELPER)

  it('keeps no case budget below the shared one', () => {
    // The unscaled constant: a caller's GATE_TIME_SCALE must not change this
    // tree's verdict. Comparing the scaled options would let a case budget
    // between the floor and the scaled value pass only when a caller raised
    // the scale.
    const floor = SHARED_TIMEOUT_MS
    const offenders = []
    for (const source of scopedTestSources) {
      for (const ms of lowCaseTimeouts(
        readFileSync(source, 'utf8'),
        floor,
        source,
      )) {
        offenders.push(`${source}: a case budget of ${ms}ms`)
      }
    }
    expect(
      offenders,
      'a case pins a budget below the shared default; drop it or raise it',
    ).toEqual([])
  })

  it('rejects every vi.waitFor and vi.waitUntil use outside waitForCondition', () => {
    const offenders = []
    for (const source of scopedWaitSources) {
      const count = bareWaitUses(
        readFileSync(source, 'utf8'),
        source,
      ).length
      if (count > 0) {
        offenders.push(`${source}: ${count} bare wait use(s)`)
      }
    }
    expect(
      offenders,
      'call waitForCondition (@speed/test-utils/wait) instead of vi.waitFor '
        + 'or vi.waitUntil: either name is reached only through that helper, '
        + "so a call, an alias, an argument, a property value and a returned "
        + 'reference all count, and both waits\' one-second default is outside '
        + 'the setup budget and GATE_TIME_SCALE',
    ).toEqual([])
  })

  it('scans the test-support directories and exempts only the wait helper', () => {
    // The scan list is the guard's own blind spot: a helper source the
    // suites run on is where a bare wait hides. Every package's test-utils,
    // the shared tree and the demo host's copy are in scope; the one file
    // that supplies the budget is out by its exact path, not by a rule.
    const dirs = testSupportDirs()
    expect(dirs).toContain(join(webRoot, 'test-utils', 'src'))
    expect(dirs).toContain(join(demoWebRoot, 'src', 'test-utils'))
    expect(dirs).toContain(join(demoWebRoot, 'src', 'a11y'))
    expect(dirs).toContain(join(webRoot, 'packages', 'account-ui', 'test-utils'))
    expect(dirs).toContain(join(webRoot, 'packages', 'admin-ui', 'test-utils'))
    expect(scopedWaitSources).toContain(
      join(webRoot, 'packages', 'account-ui', 'test-utils', 'refresh-warning.ts'),
    )
    // The root leg's own tests and the demo's a11y support modules are in
    // scope too: they run under the same budgets and may not reach either
    // wait.
    expect(scopedTestSources).toContain(
      join(webRoot, 'scripts', 'workspace-test-config.test.mjs'),
    )
    expect(scopedTestSources).toContain(
      join(webRoot, 'eslint-rules', 'no-literal-text.test.mjs'),
    )
    expect(scopedWaitSources).toContain(join(demoWebRoot, 'src', 'a11y', 'scan.ts'))
    expect(scopedWaitSources).not.toContain(WAIT_HELPER)
  })

  it('scans the case budgets it is meant to catch', () => {
    expect(
      lowCaseTimeouts("it('a case', { timeout: 5_000 }, async () => {})", 60_000),
    ).toEqual([5_000])
    expect(lowCaseTimeouts('test.setTimeout(1_000)', 60_000)).toEqual([1_000])
    expect(
      lowCaseTimeouts("it('a case', async () => {}, 2_000)", 60_000),
    ).toEqual([2_000])
    expect(
      lowCaseTimeouts('beforeEach(async () => {}, 2_000)', 60_000),
    ).toEqual([2_000])
    expect(
      lowCaseTimeouts("describe('a suite', { timeout: 5_000 }, () => {})", 60_000),
    ).toEqual([5_000])
    expect(
      lowCaseTimeouts(
        "it.each([[1]])('a case', { timeout: 5_000 }, async () => {})",
        60_000,
      ),
    ).toEqual([5_000])
    expect(
      lowCaseTimeouts(
        "it.each([[1]])('a case', async () => {}, 2_000)",
        60_000,
      ),
    ).toEqual([2_000])
    expect(
      lowCaseTimeouts("it('a case', { timeout: 120_000 }, async () => {})", 60_000),
    ).toEqual([])
    // A case budget of 0 is no limit in both Vitest and Playwright, so it is
    // a widening and passes in every form the gate reads.
    expect(
      lowCaseTimeouts("it('a case', { timeout: 0 }, async () => {})", 60_000),
    ).toEqual([])
    expect(lowCaseTimeouts('test.setTimeout(0)', 60_000)).toEqual([])
    expect(
      lowCaseTimeouts("it('a case', async () => {}, 0)", 60_000),
    ).toEqual([])
    // The exception is exactly 0: a 1ms budget is still below the floor.
    expect(lowCaseTimeouts('test.setTimeout(1)', 60_000)).toEqual([1])
    expect(
      lowCaseTimeouts("it('a case', { timeout: 1 }, async () => {})", 60_000),
    ).toEqual([1])
    // An arithmetic base is its product, not its first factor.
    expect(
      lowCaseTimeouts("it('a case', { timeout: 2 * 60_000 }, async () => {})", 60_000),
    ).toEqual([])
    expect(
      lowCaseTimeouts("it('a case', { timeout: 5 * 1000 }, async () => {})", 60_000),
    ).toEqual([5_000])
    expect(
      lowCaseTimeouts("await waitFor(() => {}, { timeout: 5_000 })", 60_000),
    ).toEqual([])
    expect(
      lowCaseTimeouts("// it('a case', { timeout: 5_000 }, async () => {})", 60_000),
    ).toEqual([])
  })

  it('scans the wait uses it is meant to catch', () => {
    expect(bareWaitUses('await vi.waitFor(() => {})')).toHaveLength(1)
    expect(bareWaitUses('await vi.waitUntil(() => true)')).toHaveLength(1)
    expect(bareWaitUses("await vi['waitFor'](() => {})")).toHaveLength(1)
    expect(bareWaitUses('const { waitFor } = vi')).toHaveLength(1)
    expect(bareWaitUses('const { waitUntil } = vi')).toHaveLength(1)
    expect(bareWaitUses('const { waitFor: wrapped } = vi')).toHaveLength(1)
    expect(bareWaitUses('let { waitFor, waitUntil } = vi')).toHaveLength(1)
    expect(bareWaitUses('({ waitFor } = vi)')).toHaveLength(1)
    // A computed key whose expression is a literal names the same property.
    expect(bareWaitUses("const { ['waitFor']: w } = vi")).toHaveLength(1)
    expect(bareWaitUses("const { ['waitUntil']: w } = vi")).toHaveLength(1)
    expect(bareWaitUses('const { [`waitFor`]: w } = vi')).toHaveLength(1)
    expect(bareWaitUses("({ ['waitFor']: w } = vi)")).toHaveLength(1)
    expect(
      bareWaitUses("function f({ ['waitUntil']: w } = vi) {}"),
    ).toHaveLength(1)
    // A computed key built from a variable is outside the static read.
    expect(bareWaitUses('const { [name]: w } = vi')).toHaveLength(0)
    expect(bareWaitUses('const wait = vi.waitFor')).toHaveLength(1)
    expect(bareWaitUses('let wait = vi.waitUntil')).toHaveLength(1)
    expect(bareWaitUses("const wait = vi['waitFor']")).toHaveLength(1)
    // Every use of either name is a member access, however it is reached:
    // passed as an argument, kept as a property value, returned from an
    // arrow, read through optional chaining, or invoked through a method of
    // its own.
    expect(bareWaitUses('wrap(vi.waitFor)')).toHaveLength(1)
    expect(bareWaitUses('const w = { run: vi.waitFor }')).toHaveLength(1)
    expect(bareWaitUses('const w = () => vi.waitFor')).toHaveLength(1)
    expect(bareWaitUses('await vi?.waitFor(() => {})')).toHaveLength(1)
    expect(bareWaitUses('vi.waitFor.call(null, () => {})')).toHaveLength(1)
    expect(bareWaitUses("await vi?.['waitUntil'](() => true)")).toHaveLength(1)
    // A string that names the call is prose; the syntax tree holds no node
    // for it, so no pattern reads it as a use.
    expect(bareWaitUses("it('names vi.waitFor in prose', () => {})")).toHaveLength(0)
    // A string that spells out an assignment is prose too.
    expect(bareWaitUses("it('x = vi.waitFor', () => {})")).toHaveLength(0)
    expect(bareWaitUses('await waitForCondition(() => {})')).toHaveLength(0)
    expect(bareWaitUses('const { spyOn } = vi')).toHaveLength(0)
    expect(bareWaitUses('// await vi.waitFor(() => {})')).toHaveLength(0)
    // An array element is prose: a string is a property key only when the
    // bracket that holds it follows the object it is read from.
    expect(bareWaitUses("const allowed = ['vi.waitFor']")).toHaveLength(0)
    expect(bareWaitUses("check(['vi.waitUntil'])")).toHaveLength(0)
    expect(bareWaitUses("const [first] = ['vi.waitFor']")).toHaveLength(0)
    // A keyword before the bracket cannot be the object a string is read from,
    // so the string after it is an array element like any other and prose.
    expect(bareWaitUses("function f() { return ['vi.waitFor'] }")).toHaveLength(0)
    expect(bareWaitUses("for (const n of ['vi.waitFor']) {}")).toHaveLength(0)
    expect(bareWaitUses("function* g() { yield ['vi.waitUntil'] }")).toHaveLength(0)
    expect(bareWaitUses("await ['vi.waitFor']")).toHaveLength(0)
    expect(bareWaitUses("switch (x) { case ['vi.waitFor']: break }")).toHaveLength(0)
    expect(bareWaitUses("x in ['vi.waitFor']")).toHaveLength(0)
    expect(bareWaitUses("typeof ['vi.waitFor']")).toHaveLength(0)
  })

  it('scans the wrappers and aliases a wait can hide behind', () => {
    // A parameter default destructures the same object a declaration does.
    expect(bareWaitUses('function f({ waitFor } = vi) {}')).toHaveLength(1)
    // Parentheses, non-null assertions and type assertions do not change
    // the object, so the member access is the same one.
    expect(bareWaitUses('(vi).waitFor(() => {})')).toHaveLength(1)
    expect(bareWaitUses('vi!.waitFor(() => {})')).toHaveLength(1)
    expect(
      bareWaitUses('(vi as unknown as typeof vi).waitFor(() => {})'),
    ).toHaveLength(1)
    expect(
      bareWaitUses('(<typeof vi>vi).waitFor(() => {})', 'constructed.ts'),
    ).toHaveLength(1)
    // A local alias, an aliased import, and an alias of an alias all name
    // the same object.
    expect(bareWaitUses('const v = vi\nv.waitFor(() => {})')).toHaveLength(1)
    expect(
      bareWaitUses("import { vi as x } from 'vitest'\nx.waitFor(() => {})"),
    ).toHaveLength(1)
    expect(
      bareWaitUses("import { vi as x } from 'vitest'\nconst { waitFor } = x"),
    ).toHaveLength(1)
    expect(
      bareWaitUses('const v = vi\nconst w = v\nw.waitFor(() => {})'),
    ).toHaveLength(1)
    expect(
      bareWaitUses('const v = vi as unknown as typeof vi\nv.waitFor(() => {})'),
    ).toHaveLength(1)
    // Naming the wait at the import reaches the same one-second default.
    expect(
      bareWaitUses("import { waitFor } from 'vitest'\nwaitFor(() => {})"),
    ).toHaveLength(1)
    expect(
      bareWaitUses("import { waitFor as w } from 'vitest'\nw(() => {})"),
    ).toHaveLength(1)
    // An alias of something else is not Vitest's vi.
    expect(bareWaitUses('const v = other\nv.waitFor(() => {})')).toHaveLength(0)
  })

  it('scans a wait after a regex literal or JSX text that carries a quote', () => {
    // A quote inside a regex literal must not open a string: doing so drops
    // every later line up to the next matching quote, and the wait below it
    // goes unseen.
    expect(
      bareWaitUses("const single = /'/g\nawait vi.waitFor(() => {})"),
    ).toHaveLength(1)
    expect(
      bareWaitUses('const double = /"/g\nawait vi.waitFor(() => {})'),
    ).toHaveLength(1)
    expect(
      bareWaitUses('const backtick = /`/g\nawait vi.waitFor(() => {})'),
    ).toHaveLength(1)
    expect(
      bareWaitUses("const pattern = /['\"]x/g\nawait vi.waitFor(() => {})"),
    ).toHaveLength(1)
    // The TypeScript parser reads these as regex literals, not strings, so
    // the quotes inside them cannot hide the wait below; both shapes occur
    // in this repository's test sources.
    expect(
      bareWaitUses(
        "const literal = /(['\"`])([^'\"`\\\\]*)\\1/g\nawait vi.waitFor(() => {})",
      ),
    ).toHaveLength(1)
    expect(
      bareWaitUses(
        "/(?:^|\\n)\\s*import\\b[^\\n]*['\"]qrcode-generator['\"]/.test(code)\n"
          + 'await vi.waitFor(() => {})',
      ),
    ).toHaveLength(1)
    // An apostrophe inside JSX text is prose, not a string opener.
    expect(
      bareWaitUses("<p>it's a member</p>\nawait vi.waitFor(() => {})"),
    ).toHaveLength(1)
    // A template literal is free to span lines, and the wait spelled out
    // inside it stays prose; the one below it is a use.
    expect(
      bareWaitUses('const text = `a\nvi.waitFor`\nawait vi.waitFor(() => {})'),
    ).toHaveLength(1)
    // A division is not a regex, so it masks nothing after it.
    expect(
      bareWaitUses('const half = total / 2\nawait vi.waitFor(() => {})'),
    ).toHaveLength(1)
    // A string is a value: the division after it is still a division.
    expect(
      bareWaitUses("const half = 'a'.length / 2\nawait vi.waitFor"),
    ).toHaveLength(1)
  })

  it('scans a wait below a JSX closing tag that shares its line with a template', () => {
    // `</Link>` is a closing tag, not a regex: read as one, the scan runs to
    // the slash inside the template on the same line, swallows the opening
    // backtick, and the closing one opens a template that runs to the end of
    // the file, hiding every wait below it.
    expect(
      bareWaitUses(
        'render(<Link>a</Link>); f(`/a/${id}`)\nawait vi.waitFor(() => {})',
      ),
    ).toHaveLength(1)
    expect(
      bareWaitUses(
        'render(<div>a</div>); f(`/a/${id}`)\nawait vi.waitFor(() => {})',
      ),
    ).toHaveLength(1)
    // A self-closing tag's slash follows the tag name, or an attribute value.
    // A lexer that reads the `}` before it as one that can precede a regex
    // opens one there, runs to the next slash inside the template on the same
    // line and takes that template's backticks, hiding every wait below. The
    // parser reads the tag as JSX.
    expect(
      bareWaitUses(
        'render(<Br />); f(`/a/${id}`)\nawait vi.waitFor(() => {})',
      ),
    ).toHaveLength(1)
    expect(
      bareWaitUses(
        'render(<A p={x} />); f(`/a/${id}`)\nawait vi.waitFor(() => {})\n'
          + 'const t = `z`',
      ),
    ).toHaveLength(1)
    // A `<` operator still opens a regex: the wait name inside the pattern is
    // prose. Read as a division instead, it would be counted as a use.
    expect(
      bareWaitUses('const match = x < /vi.waitFor/\nawait vi.waitFor(() => {})'),
    ).toHaveLength(1)
  })

  it('scans a wait below a regex literal that carries a backtick', () => {
    // A backtick inside a regex literal used to open a template that ran to
    // the next backtick, or to the end of the file when there was none. The
    // parser reads the whole literal as a regular expression.
    expect(
      bareWaitUses(
        'if (x) /`/.test(s)\nawait vi.waitFor(() => {})\nconst t = `z`',
      ),
    ).toHaveLength(1)
    // The unspaced form is a comparison, not a JSX tag; it is parsed as
    // TypeScript, where `</` cannot open a JSX closing tag.
    expect(
      bareWaitUses(
        'x</`/.test(s)\nawait vi.waitFor(() => {})\nconst t = `z`',
        'constructed.ts',
      ),
    ).toHaveLength(1)
  })

  it('reports a test source it cannot parse instead of skipping it', () => {
    // A source with no syntax tree is not a source with no offenders: the
    // guard stops and names the file, so the blind spot cannot pass as clean.
    for (const source of [
      'const text = `unclosed\nawait vi.waitFor(() => {})',
      "const label = 'unfinished\nawait vi.waitFor(() => {})",
      'const label = "unfinished\nawait vi.waitFor(() => {})',
    ]) {
      expect(() => bareWaitUses(source)).toThrow(/constructed\.tsx/)
    }
  })

  it('scans the async budget argument it is meant to catch', () => {
    expect(
      asyncBudgetScale(
        'configure({ asyncUtilTimeout: asyncBudgetMs(gateTimeScale()) })',
      ),
    ).toBe('gateTimeScale()')
    expect(
      asyncBudgetScale(
        'configure({ asyncUtilTimeout: asyncBudgetMs(__SPEED_GATE_TIME_SCALE__) })',
      ),
    ).toBe('__SPEED_GATE_TIME_SCALE__')
    // A literal is reported and refused by SCALE_SOURCE even at the floor:
    // it would ignore a caller who raised the scale.
    expect(
      asyncBudgetScale('configure({ asyncUtilTimeout: asyncBudgetMs(1) })'),
    ).toBe('1')
    expect(
      asyncBudgetScale('configure({ asyncUtilTimeout: asyncBudgetMs(0.5) })'),
    ).toBe('0.5')
    expect(asyncBudgetScale('configure({ asyncUtilTimeout: 30_000 })')).toBeNull()
    // A source that cannot be parsed names the file it came from, not the
    // placeholder: a setup the guard could not read must not report
    // `constructed.ts` and leave the real file unidentified.
    expect(() =>
      asyncBudgetScale(
        'configure({ asyncUtilTimeout: asyncBudgetMs(',
        'setup.ts',
      ),
    ).toThrow(/setup\.ts/)
    expect(SCALE_SOURCE.test('gateTimeScale()')).toBe(true)
    expect(SCALE_SOURCE.test('__SPEED_GATE_TIME_SCALE__')).toBe(true)
    expect(SCALE_SOURCE.test('1')).toBe(false)
    expect(SCALE_SOURCE.test('0.5')).toBe(false)
  })

  for (const pkg of testPackages) {
    it(`${pkg.name} takes its test options from the shared preset`, async () => {
      const configPath = join(pkg.dir, 'vitest.config.ts')
      const sourceFile = parseTestSource(
        readFileSync(configPath, 'utf8'),
        configPath,
      )

      // The spread is what makes the preset the source of the options;
      // a hand-written block (or a locally pinned timeout) is exactly the
      // omission this guard exists to catch. The import carries the .ts
      // extension because Vite's native config loader warns on an
      // extensionless one and plans to reject it.
      expect(
        importsFrom(sourceFile, /^\.\.\/(?:\.\.\/)?vitest\.shared\.ts$/),
      ).toBe(true)
      expect(hasCallNamed(sourceFile, 'sharedTestOptions')).toBe(true)
      expect(hasPropertyNamed(sourceFile, 'testTimeout')).toBe(false)
      expect(hasPropertyNamed(sourceFile, 'hookTimeout')).toBe(false)

      const config = (await import(pathToFileURL(configPath).href)).default

      // Compared against the preset rather than a literal: the shared
      // budget is multiplied by the caller's GATE_TIME_SCALE, and the
      // guard is that the package took it, not a particular number. The
      // floor case above is what stops the preset itself being lowered.
      expect(config.test?.testTimeout).toBe(sharedTestOptions().testTimeout)
      expect(config.test?.hookTimeout).toBe(sharedTestOptions().hookTimeout)
      expect(config.test?.coverage).toMatchObject({
        provider: sharedCoverage.provider,
        include: sharedCoverage.include,
        reporter: sharedCoverage.reporter,
      })
      // test-utils deliberately drops the floor (its src is driven by the
      // consuming packages' suites); any package that keeps one keeps the
      // shared one.
      if (config.test?.coverage?.thresholds !== undefined) {
        expect(config.test.coverage.thresholds).toEqual(
          sharedCoverage.thresholds,
        )
      }
    })
  }
})
