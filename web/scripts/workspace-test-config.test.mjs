import {
  existsSync,
  globSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, extname, join, relative, sep } from 'node:path'
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

const packageDirs = [
  ...readdirSync(join(webRoot, 'packages'), { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => join(webRoot, 'packages', entry.name)),
  join(webRoot, 'test-utils'),
]

const testPackages = packageDirs
  .map((dir) => {
    const manifest = JSON.parse(
      readFileSync(join(dir, 'package.json'), 'utf8'),
    )
    return { dir, name: manifest.name, test: manifest.scripts?.test }
  })
  .filter((pkg) => typeof pkg.test === 'string')

const UNIT_CONFIG_PATH = join(webRoot, 'vitest.unit.config.ts')

function workspaceMemberDirs(root = webRoot) {
  const workspaceFile = join(root, 'pnpm-workspace.yaml')
  const lines = readFileSync(workspaceFile, 'utf8').split('\n')
  const start = lines.findIndex((line) => /^packages:\s*$/.test(line))
  if (start === -1) {
    throw new Error(`${workspaceFile}: no top-level packages: list`)
  }
  const patterns = []
  for (const line of lines.slice(start + 1)) {
    if (/^\s*(?:#.*)?$/.test(line)) {
      continue
    }
    const item = /^\s*-\s*(.+?)\s*$/.exec(line)
    if (item === null) {
      break
    }
    patterns.push(item[1].replace(/\s+#.*$/, '').replace(/^['"]|['"]$/g, ''))
  }
  if (patterns.length === 0) {
    throw new Error(`${workspaceFile}: packages: lists no member`)
  }
  const dirs = []
  for (const pattern of patterns) {
    const matches = globSync(`${pattern}/package.json`, { cwd: root })
    if (matches.length === 0) {
      throw new Error(`${workspaceFile}: ${pattern} matches no member`)
    }
    for (const match of matches) {
      dirs.push(join(root, dirname(match)))
    }
  }
  return dirs
}

const workspaceTestMembers = workspaceMemberDirs()
  .map((dir) => {
    const manifest = JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8'))
    return { dir, name: manifest.name, test: manifest.scripts?.test }
  })
  .filter((member) => typeof member.test === 'string')

function unitProjectPath(dir) {
  return relative(webRoot, join(dir, 'vitest.config.ts')).split(sep).join('/')
}

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

function scriptKindOf(filePath) {
  return SCRIPT_KINDS[extname(filePath)] ?? ts.ScriptKind.TSX
}

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

function forEachNode(node, visit) {
  visit(node)
  ts.forEachChild(node, (child) => forEachNode(child, visit))
}

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

const WAIT_NAMES = new Set(['waitFor', 'waitUntil'])

const REAL_TIMER_NAMES = new Set(['useRealTimers'])

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

function isNamedReference(node, names) {
  const value = unwrapExpression(node)
  return ts.isIdentifier(value) && names.has(value.text)
}

function isMemberAccess(node, names, forbidden) {
  if (ts.isPropertyAccessExpression(node)) {
    return isNamedReference(node.expression, names)
      && forbidden.has(node.name.text)
  }
  if (ts.isElementAccessExpression(node)) {
    const key = node.argumentExpression
    return isNamedReference(node.expression, names)
      && (ts.isStringLiteral(key)
        || ts.isNoSubstitutionTemplateLiteral(key))
      && forbidden.has(key.text)
  }
  return false
}

function bindsName(pattern, forbidden) {
  return (
    ts.isObjectBindingPattern(pattern)
    && pattern.elements.some(
      (element) =>
        ts.isBindingElement(element)
        && forbidden.has(
          writtenName(element.propertyName ?? element.name) ?? '',
        ),
    )
  )
}

function isDestructuredFromVi(node, names, forbidden) {
  if (
    ts.isVariableDeclaration(node)
    && bindsName(node.name, forbidden)
    && node.initializer !== undefined
    && isNamedReference(node.initializer, names)
  ) {
    return true
  }
  return (
    ts.isParameter(node)
    && bindsName(node.name, forbidden)
    && node.initializer !== undefined
    && isNamedReference(node.initializer, names)
  )
}

function isAssignedFromVi(node, names, forbidden) {
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
        && forbidden.has(writtenName(property.name) ?? ''),
    )
}

function importsNameDirectly(node, forbidden) {
  return (
    ts.isImportDeclaration(node)
    && ts.isStringLiteral(node.moduleSpecifier)
    && node.moduleSpecifier.text === 'vitest'
    && ts.isNamedImports(node.importClause?.namedBindings)
    && node.importClause.namedBindings.elements.some((element) =>
      forbidden.has((element.propertyName ?? element.name).text))
  )
}

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

const CASE_HEADS = new Set([
  'it',
  'test',
  'beforeEach',
  'afterEach',
  'beforeAll',
  'afterAll',
  'describe',
])

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

function caseCallBudgets(call, floor) {
  const lowered = []
  const options = call.arguments[1]
  if (options !== undefined && ts.isObjectLiteralExpression(options)) {
    const ms = timeoutOf(options)
    if (ms !== null && ms !== 0 && ms < floor) {
      lowered.push(ms)
    }
  }
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
    if (chain.length === 2 && chain[0] === 'test' && chain[1] === 'setTimeout') {
      return
    }
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

function bareMemberUses(source, forbidden, filePath = 'constructed.tsx') {
  const sourceFile = parseTestSource(source, filePath)
  const names = viNames(sourceFile)
  const uses = []
  forEachNode(sourceFile, (node) => {
    if (
      isMemberAccess(node, names, forbidden)
      || isDestructuredFromVi(node, names, forbidden)
      || isAssignedFromVi(node, names, forbidden)
      || importsNameDirectly(node, forbidden)
    ) {
      uses.push(node.pos)
    }
  })
  return uses
}

function bareWaitUses(source, filePath = 'constructed.tsx') {
  return bareMemberUses(source, WAIT_NAMES, filePath)
}

function bareRealTimerUses(source, filePath = 'constructed.tsx') {
  return bareMemberUses(source, REAL_TIMER_NAMES, filePath)
}

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

function setupAsyncBudgetScale(path) {
  return asyncBudgetScale(readFileSync(path, 'utf8'), path)
}

const SCALE_SOURCE = /^(?:gateTimeScale\(\)|__SPEED_GATE_TIME_SCALE__)$/

function setupScales(setups) {
  return setups.map((path) => {
    const scale = setupAsyncBudgetScale(path)
    if (scale === null) {
      throw new Error(`${path} names no async budget for the guard to check`)
    }
    if (!SCALE_SOURCE.test(scale)) {
      throw new Error(
        `${path} pins the async budget instead of taking the caller's scale`,
      )
    }
    return scale
  })
}

const SOURCE_FILE = /\.(?:[cm]?[jt]sx?)$/

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

function testSources(dir) {
  return scriptSources(dir).filter((path) =>
    /\.test\.(?:[cm]?[jt]sx?)$/.test(path))
}

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

const WAIT_HELPER = join(webRoot, 'test-utils', 'src', 'wait.ts')

const REAL_TIMER_HELPERS = new Set([
  join(webRoot, 'test-utils', 'src', 'timers.ts'),
  join(demoWebRoot, 'src', 'test-utils', 'timers.ts'),
])

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
    expect(options.exclude).toContain('**/node_modules/**')
  })

  it('gives the root eslint-rules/scripts leg the shared preset', () => {
    const configPath = join(webRoot, 'vitest.config.ts')
    const sourceFile = parseTestSource(readFileSync(configPath, 'utf8'), configPath)
    expect(hasCallNamed(sourceFile, 'sharedTestOptions')).toBe(true)
    expect(hasPropertyNamed(sourceFile, 'testTimeout')).toBe(false)
    expect(hasPropertyNamed(sourceFile, 'hookTimeout')).toBe(false)
  })

  it('runs every workspace member that has tests in one vitest process', async () => {
    expect(workspaceTestMembers.length).toBeGreaterThan(0)
    const config = (await import(pathToFileURL(UNIT_CONFIG_PATH).href)).default
    const projects = config.test?.projects
    expect(Array.isArray(projects)).toBe(true)
    const expanded = new Set()
    for (const project of projects) {
      expect(typeof project, String(project)).toBe('string')
      const matches = globSync(project, { cwd: webRoot })
      expect(matches, project).not.toHaveLength(0)
      for (const match of matches) {
        expanded.add(match.split(sep).join('/'))
      }
    }
    expect([...expanded].sort()).toEqual(
      workspaceTestMembers.map((member) => unitProjectPath(member.dir)).sort(),
    )
  })

  it('ignores a workspace pattern match that carries no manifest', () => {
    const root = mkdtempSync(join(tmpdir(), 'workspace-members-'))
    try {
      writeFileSync(
        join(root, 'pnpm-workspace.yaml'),
        'packages:\n  - packages/*\n',
      )
      mkdirSync(join(root, 'packages', 'real'), { recursive: true })
      writeFileSync(
        join(root, 'packages', 'real', 'package.json'),
        JSON.stringify({ name: 'real', scripts: { test: 'vitest run' } }),
      )
      mkdirSync(join(root, 'packages', 'stray', 'node_modules'), {
        recursive: true,
      })
      writeFileSync(
        join(root, 'packages', 'stray', 'node_modules', 'left.txt'),
        '',
      )
      expect(workspaceMemberDirs(root)).toEqual([
        join(root, 'packages', 'real'),
      ])
    } finally {
      rmSync(root, { recursive: true, force: true })
    }
  })

  it('gives the single-process unit config the shared preset and reporters', async () => {
    const sourceFile = parseTestSource(
      readFileSync(UNIT_CONFIG_PATH, 'utf8'),
      UNIT_CONFIG_PATH,
    )
    expect(hasCallNamed(sourceFile, 'sharedTestOptions')).toBe(true)
    expect(hasPropertyNamed(sourceFile, 'testTimeout')).toBe(false)
    expect(hasPropertyNamed(sourceFile, 'hookTimeout')).toBe(false)
    const config = (await import(pathToFileURL(UNIT_CONFIG_PATH).href)).default
    expect(config.test?.reporters).toEqual(sharedReporters())
  })

  it("runs each workspace member's tests with the package's own script", () => {
    for (const member of workspaceTestMembers) {
      expect(member.test, member.name).toBe('vitest run')
    }
  })

  it('fixes the test reporter so the log does not depend on the runner', () => {
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
    const a11yPath = join(demoWebRoot, 'vitest.a11y.config.ts')
    const a11yFile = parseTestSource(readFileSync(a11yPath, 'utf8'), a11yPath)
    expect(reportersComeFromShared(a11yFile, 'verbose')).toBe(true)
    expect(hasPropertyNamed(a11yFile, 'reporter')).toBe(false)
  })

  it('keeps the shared case budget at or above its floor', () => {
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
    expect(asyncBudgetsBelowFloor(TIME_FLOORS)).toEqual([])
    expect(
      setupScales([
        join(webRoot, 'test-utils/src/setup.ts'),
        join(webRoot, 'test-utils/src/browser-setup.ts'),
        join(demoWebRoot, 'src/test-utils/setup.ts'),
      ]),
    ).toHaveLength(3)
  })

  it('names the setup file it cannot parse', () => {
    const dir = mkdtempSync(join(tmpdir(), 'workspace-test-setup-'))
    try {
      const path = join(dir, 'setup.ts')
      writeFileSync(path, 'configure({ asyncUtilTimeout: asyncBudgetMs(')
      const setups = [join(webRoot, 'test-utils/src/setup.ts'), path]
      expect(() => setupScales(setups)).toThrow(path)
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })

  const scopedTestSources = [
    ...testPackages.flatMap((pkg) => testSources(pkg.dir)),
    ...testSources(demoWebRoot),
    ...testSources(join(webRoot, 'eslint-rules')),
    ...testSources(join(webRoot, 'scripts')),
  ]

  const scopedSupportSources = [
    ...new Set([
      ...scopedTestSources,
      ...testSupportDirs().flatMap(scriptSources),
    ]),
  ]

  const scopedWaitSources = scopedSupportSources.filter(
    (source) => source !== WAIT_HELPER,
  )

  const scopedRealTimerSources = scopedSupportSources.filter(
    (source) => !REAL_TIMER_HELPERS.has(source),
  )

  it('keeps no case budget below the shared one', () => {
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

  it('rejects every direct vi.useRealTimers use outside restoreRealTimers', () => {
    const offenders = []
    for (const source of scopedRealTimerSources) {
      const count = bareRealTimerUses(
        readFileSync(source, 'utf8'),
        source,
      ).length
      if (count > 0) {
        offenders.push(`${source}: ${count} direct real-timer use(s)`)
      }
    }
    expect(
      offenders,
      'call restoreRealTimers (@speed/test-utils/timers) instead of '
        + 'vi.useRealTimers: restoring the real clock directly strands the '
        + "jsdom frame interval a fake-timer focus change armed, so a call, "
        + 'an alias, an argument, a property value and a returned reference '
        + 'all count, and only the two timers.ts helpers are exempt',
    ).toEqual([])
  })

  it('scans the test-support directories and exempts only the two helpers', () => {
    const dirs = testSupportDirs()
    expect(dirs).toContain(join(webRoot, 'test-utils', 'src'))
    expect(dirs).toContain(join(demoWebRoot, 'src', 'test-utils'))
    expect(dirs).toContain(join(demoWebRoot, 'src', 'a11y'))
    expect(dirs).toContain(join(webRoot, 'packages', 'account-ui', 'test-utils'))
    expect(dirs).toContain(join(webRoot, 'packages', 'admin-ui', 'test-utils'))
    expect(scopedWaitSources).toContain(
      join(webRoot, 'packages', 'account-ui', 'test-utils', 'refresh-warning.ts'),
    )
    expect(scopedTestSources).toContain(
      join(webRoot, 'scripts', 'workspace-test-config.test.mjs'),
    )
    expect(scopedTestSources).toContain(
      join(webRoot, 'eslint-rules', 'no-literal-text.test.mjs'),
    )
    expect(scopedWaitSources).toContain(join(demoWebRoot, 'src', 'a11y', 'scan.ts'))
    expect(scopedWaitSources).not.toContain(WAIT_HELPER)
    expect(scopedRealTimerSources).toContain(join(demoWebRoot, 'src', 'a11y', 'scan.ts'))
    expect(scopedRealTimerSources).toContain(
      join(webRoot, 'test-utils', 'src', 'timers.test.ts'),
    )
    expect(scopedRealTimerSources).toContain(
      join(demoWebRoot, 'src', 'impersonation.test.tsx'),
    )
    for (const helper of REAL_TIMER_HELPERS) {
      expect(scopedRealTimerSources).not.toContain(helper)
    }
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
    expect(
      lowCaseTimeouts("it('a case', { timeout: 0 }, async () => {})", 60_000),
    ).toEqual([])
    expect(lowCaseTimeouts('test.setTimeout(0)', 60_000)).toEqual([])
    expect(
      lowCaseTimeouts("it('a case', async () => {}, 0)", 60_000),
    ).toEqual([])
    expect(lowCaseTimeouts('test.setTimeout(1)', 60_000)).toEqual([1])
    expect(
      lowCaseTimeouts("it('a case', { timeout: 1 }, async () => {})", 60_000),
    ).toEqual([1])
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
    expect(bareWaitUses("const { ['waitFor']: w } = vi")).toHaveLength(1)
    expect(bareWaitUses("const { ['waitUntil']: w } = vi")).toHaveLength(1)
    expect(bareWaitUses('const { [`waitFor`]: w } = vi')).toHaveLength(1)
    expect(bareWaitUses("({ ['waitFor']: w } = vi)")).toHaveLength(1)
    expect(
      bareWaitUses("function f({ ['waitUntil']: w } = vi) {}"),
    ).toHaveLength(1)
    expect(bareWaitUses('const { [name]: w } = vi')).toHaveLength(0)
    expect(bareWaitUses('const wait = vi.waitFor')).toHaveLength(1)
    expect(bareWaitUses('let wait = vi.waitUntil')).toHaveLength(1)
    expect(bareWaitUses("const wait = vi['waitFor']")).toHaveLength(1)
    expect(bareWaitUses('wrap(vi.waitFor)')).toHaveLength(1)
    expect(bareWaitUses('const w = { run: vi.waitFor }')).toHaveLength(1)
    expect(bareWaitUses('const w = () => vi.waitFor')).toHaveLength(1)
    expect(bareWaitUses('await vi?.waitFor(() => {})')).toHaveLength(1)
    expect(bareWaitUses('vi.waitFor.call(null, () => {})')).toHaveLength(1)
    expect(bareWaitUses("await vi?.['waitUntil'](() => true)")).toHaveLength(1)
    expect(bareWaitUses("it('names vi.waitFor in prose', () => {})")).toHaveLength(0)
    expect(bareWaitUses("it('x = vi.waitFor', () => {})")).toHaveLength(0)
    expect(bareWaitUses('await waitForCondition(() => {})')).toHaveLength(0)
    expect(bareWaitUses('const { spyOn } = vi')).toHaveLength(0)
    expect(bareWaitUses('// await vi.waitFor(() => {})')).toHaveLength(0)
    expect(bareWaitUses("const allowed = ['vi.waitFor']")).toHaveLength(0)
    expect(bareWaitUses("check(['vi.waitUntil'])")).toHaveLength(0)
    expect(bareWaitUses("const [first] = ['vi.waitFor']")).toHaveLength(0)
    expect(bareWaitUses("function f() { return ['vi.waitFor'] }")).toHaveLength(0)
    expect(bareWaitUses("for (const n of ['vi.waitFor']) {}")).toHaveLength(0)
    expect(bareWaitUses("function* g() { yield ['vi.waitUntil'] }")).toHaveLength(0)
    expect(bareWaitUses("await ['vi.waitFor']")).toHaveLength(0)
    expect(bareWaitUses("switch (x) { case ['vi.waitFor']: break }")).toHaveLength(0)
    expect(bareWaitUses("x in ['vi.waitFor']")).toHaveLength(0)
    expect(bareWaitUses("typeof ['vi.waitFor']")).toHaveLength(0)
  })

  it('scans the direct real-timer uses it is meant to catch', () => {
    expect(bareRealTimerUses('vi.useRealTimers()')).toHaveLength(1)
    expect(bareRealTimerUses("vi['useRealTimers']()")).toHaveLength(1)
    expect(bareRealTimerUses('const { useRealTimers } = vi')).toHaveLength(1)
    expect(
      bareRealTimerUses('const { useRealTimers: restore } = vi'),
    ).toHaveLength(1)
    expect(bareRealTimerUses('let { useRealTimers } = vi')).toHaveLength(1)
    expect(bareRealTimerUses('({ useRealTimers } = vi)')).toHaveLength(1)
    expect(
      bareRealTimerUses("const { ['useRealTimers']: r } = vi"),
    ).toHaveLength(1)
    expect(bareRealTimerUses('const restore = vi.useRealTimers')).toHaveLength(1)
    expect(
      bareRealTimerUses("const restore = vi['useRealTimers']"),
    ).toHaveLength(1)
    expect(bareRealTimerUses('wrap(vi.useRealTimers)')).toHaveLength(1)
    expect(
      bareRealTimerUses('const w = { restore: vi.useRealTimers }'),
    ).toHaveLength(1)
    expect(bareRealTimerUses('const w = () => vi.useRealTimers')).toHaveLength(1)
    expect(bareRealTimerUses('vi?.useRealTimers()')).toHaveLength(1)
    expect(bareRealTimerUses('vi.useRealTimers.call(null)')).toHaveLength(1)
    expect(bareRealTimerUses("await vi?.['useRealTimers']()")).toHaveLength(1)
    expect(
      bareRealTimerUses('function f({ useRealTimers } = vi) {}'),
    ).toHaveLength(1)
    expect(
      bareRealTimerUses('const v = vi\nv.useRealTimers()'),
    ).toHaveLength(1)
    expect(
      bareRealTimerUses("import { vi as x } from 'vitest'\nx.useRealTimers()"),
    ).toHaveLength(1)
    expect(
      bareRealTimerUses(
        "import { vi as x } from 'vitest'\nconst { useRealTimers } = x",
      ),
    ).toHaveLength(1)
    expect(
      bareRealTimerUses(
        "import { useRealTimers } from 'vitest'\nuseRealTimers()",
      ),
    ).toHaveLength(1)
    expect(bareRealTimerUses('(vi).useRealTimers()')).toHaveLength(1)
    expect(bareRealTimerUses('vi!.useRealTimers()')).toHaveLength(1)
    expect(
      bareRealTimerUses('(vi as unknown as typeof vi).useRealTimers()'),
    ).toHaveLength(1)
    expect(
      bareRealTimerUses('(<typeof vi>vi).useRealTimers()', 'constructed.ts'),
    ).toHaveLength(1)
    expect(
      bareRealTimerUses('const v = vi\nconst w = v\nw.useRealTimers()'),
    ).toHaveLength(1)
    expect(
      bareRealTimerUses('const v = vi as unknown as typeof vi\nv.useRealTimers()'),
    ).toHaveLength(1)
    expect(
      bareRealTimerUses("import { useRealTimers as r } from 'vitest'\nr()"),
    ).toHaveLength(1)
    expect(
      bareRealTimerUses('const { [`useRealTimers`]: r } = vi'),
    ).toHaveLength(1)
    expect(bareRealTimerUses('vi[`useRealTimers`]()')).toHaveLength(1)
    expect(bareRealTimerUses('({ useRealTimers: r } = vi)')).toHaveLength(1)
    expect(
      bareRealTimerUses("({ ['useRealTimers']: r } = vi)"),
    ).toHaveLength(1)
    expect(
      bareRealTimerUses("function f({ ['useRealTimers']: r } = vi) {}"),
    ).toHaveLength(1)
    expect(bareRealTimerUses('const { [name]: r } = vi')).toHaveLength(0)
    expect(
      bareRealTimerUses('const v = other\nv.useRealTimers()'),
    ).toHaveLength(0)
    expect(
      bareRealTimerUses("it('calls vi.useRealTimers', () => {})"),
    ).toHaveLength(0)
    expect(bareRealTimerUses('// vi.useRealTimers()')).toHaveLength(0)
    expect(bareRealTimerUses('await restoreRealTimers()')).toHaveLength(0)
    expect(bareRealTimerUses('const { useFakeTimers } = vi')).toHaveLength(0)
    expect(bareRealTimerUses('vi.useFakeTimers()')).toHaveLength(0)
  })

  it('exempts only the two timers helpers from the real-timer rule', () => {
    for (const helper of REAL_TIMER_HELPERS) {
      expect(scopedRealTimerSources).not.toContain(helper)
      expect(
        bareRealTimerUses(readFileSync(helper, 'utf8'), helper).length,
      ).toBeGreaterThan(0)
    }
  })

  it('scans the wrappers and aliases a wait can hide behind', () => {
    expect(bareWaitUses('function f({ waitFor } = vi) {}')).toHaveLength(1)
    expect(bareWaitUses('(vi).waitFor(() => {})')).toHaveLength(1)
    expect(bareWaitUses('vi!.waitFor(() => {})')).toHaveLength(1)
    expect(
      bareWaitUses('(vi as unknown as typeof vi).waitFor(() => {})'),
    ).toHaveLength(1)
    expect(
      bareWaitUses('(<typeof vi>vi).waitFor(() => {})', 'constructed.ts'),
    ).toHaveLength(1)
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
    expect(
      bareWaitUses("import { waitFor } from 'vitest'\nwaitFor(() => {})"),
    ).toHaveLength(1)
    expect(
      bareWaitUses("import { waitFor as w } from 'vitest'\nw(() => {})"),
    ).toHaveLength(1)
    expect(bareWaitUses('const v = other\nv.waitFor(() => {})')).toHaveLength(0)
  })

  it('scans a wait after a regex literal or JSX text that carries a quote', () => {
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
    expect(
      bareWaitUses("<p>it's a member</p>\nawait vi.waitFor(() => {})"),
    ).toHaveLength(1)
    expect(
      bareWaitUses('const text = `a\nvi.waitFor`\nawait vi.waitFor(() => {})'),
    ).toHaveLength(1)
    expect(
      bareWaitUses('const half = total / 2\nawait vi.waitFor(() => {})'),
    ).toHaveLength(1)
    expect(
      bareWaitUses("const half = 'a'.length / 2\nawait vi.waitFor"),
    ).toHaveLength(1)
  })

  it('scans a wait below a JSX closing tag that shares its line with a template', () => {
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
    expect(
      bareWaitUses('const match = x < /vi.waitFor/\nawait vi.waitFor(() => {})'),
    ).toHaveLength(1)
  })

  it('scans a wait below a regex literal that carries a backtick', () => {
    expect(
      bareWaitUses(
        'if (x) /`/.test(s)\nawait vi.waitFor(() => {})\nconst t = `z`',
      ),
    ).toHaveLength(1)
    expect(
      bareWaitUses(
        'x</`/.test(s)\nawait vi.waitFor(() => {})\nconst t = `z`',
        'constructed.ts',
      ),
    ).toHaveLength(1)
  })

  it('reports a test source it cannot parse instead of skipping it', () => {
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
    expect(
      asyncBudgetScale('configure({ asyncUtilTimeout: asyncBudgetMs(1) })'),
    ).toBe('1')
    expect(
      asyncBudgetScale('configure({ asyncUtilTimeout: asyncBudgetMs(0.5) })'),
    ).toBe('0.5')
    expect(asyncBudgetScale('configure({ asyncUtilTimeout: 30_000 })')).toBeNull()
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

      expect(
        importsFrom(sourceFile, /^\.\.\/(?:\.\.\/)?vitest\.shared\.ts$/),
      ).toBe(true)
      expect(hasCallNamed(sourceFile, 'sharedTestOptions')).toBe(true)
      expect(hasPropertyNamed(sourceFile, 'testTimeout')).toBe(false)
      expect(hasPropertyNamed(sourceFile, 'hookTimeout')).toBe(false)

      const config = (await import(pathToFileURL(configPath).href)).default

      expect(config.test?.testTimeout).toBe(sharedTestOptions().testTimeout)
      expect(config.test?.hookTimeout).toBe(sharedTestOptions().hookTimeout)
      expect(config.test?.coverage).toMatchObject({
        provider: sharedCoverage.provider,
        include: sharedCoverage.include,
        reporter: sharedCoverage.reporter,
      })
      if (config.test?.coverage?.thresholds !== undefined) {
        expect(config.test.coverage.thresholds).toEqual(
          sharedCoverage.thresholds,
        )
      }
    })
  }
})
