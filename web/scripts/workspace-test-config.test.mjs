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
 * budget, and `vi.waitUntil` is forbidden; a bare call, a bracket-access
 * call, a destructured alias of either (`const { waitFor } = vi`), or a
 * reference kept uncalled (`const wait = vi.waitFor`) in a test source is
 * red here. Testing Library's own `waitFor` and `findBy*` queries are
 * outside this guard: they wait under the `asyncUtilTimeout` budget the
 * setup configures.
 *
 * The demo host (starters/demo/web) is a copyable application whose
 * repository-internal test wiring is dropped on copy out, so it is not
 * checked for the preset's provenance. Its test sources are scanned for a
 * narrowed case budget and a bare wait, and its setup for the async
 * budget, because the copy carries the same wiring by hand.
 */
import {
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { describe, expect, it } from 'vitest'
import {
  gateTimeScale,
  SHARED_TIMEOUT_MS,
  sharedCoverage,
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

/** Comments are prose, not configuration; drop them before matching. */
function stripComments(source) {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/\/\/.*$/gm, '')
}

/**
 * A constant millisecond expression: a numeric literal with optional digit
 * separators, or factors joined by `*` (`2 * 60_000`). The product of the
 * factors is the value, so an arithmetic base is not read as its first
 * factor.
 */
function toMs(literal) {
  return literal
    .split('*')
    .map((factor) => Number(factor.trim().replaceAll('_', '')))
    .reduce((total, factor) => total * factor, 1)
}

/**
 * The text of the call whose opening parenthesis is at `openIndex`, with
 * nested parentheses and quoted strings respected, or null when the
 * source ends first.
 */
function callText(source, openIndex) {
  let depth = 0
  let quote = null
  for (let index = openIndex; index < source.length; index += 1) {
    const char = source[index]
    if (quote !== null) {
      if (char === '\\') {
        index += 1
      } else if (char === quote) {
        quote = null
      }
      continue
    }
    if (char === "'" || char === '"' || char === '`') {
      quote = char
    } else if (char === '(') {
      depth += 1
    } else if (char === ')') {
      depth -= 1
      if (depth === 0) {
        return source.slice(openIndex, index + 1)
      }
    }
  }
  return null
}

/**
 * The trailing numeric budget of a call whose last argument is a number. A
 * budget of 0 is no limit, which is a widening and passes.
 */
function trailingTimeout(call, floor) {
  const trailing = /,\s*([\d_]+(?:\s*\*\s*[\d_]+)*)\s*\)$/.exec(call)
  if (trailing !== null) {
    const ms = toMs(trailing[1])
    if (ms !== 0 && ms < floor) {
      return [ms]
    }
  }
  return []
}

/** The `timeout` in the options object a call passes after a name string. */
function optionTimeout(call, floor) {
  // `it('name', { timeout }, fn)`, and the call an `it.each(table)` returns
  // (`it.each(table)('name', { timeout }, fn)`): the name string is
  // followed by the options object. A `{ timeout }` deeper in an arrow
  // function's body belongs to a wait or a nested call, not to this case.
  const named =
    /\(\s*(?:(['"`])(?:\\.|(?!\1).)*\1)\s*,\s*\{([^}]*)\}/.exec(call)
  if (named === null) {
    return []
  }
  const timeout = /\btimeout\s*:\s*([\d_]+(?:\s*\*\s*[\d_]+)*)/.exec(named[2])
  if (timeout !== null) {
    const ms = toMs(timeout[1])
    // 0 is Vitest's and Playwright's "no limit", which is a widening.
    if (ms !== 0 && ms < floor) {
      return [ms]
    }
  }
  return []
}

/**
 * The per-case, per-hook and per-suite budgets in one test source that sit
 * below `floor`. Four forms are scanned: a trailing numeric budget
 * (`it(name, fn, 2000)`, `beforeEach(fn, 2000)`), an options object after
 * a name (`it(name, { timeout }, fn)`, `describe(name, { timeout }, fn)`),
 * the call an `it.each(table)`/`test.each(table)` returns, and
 * `test.setTimeout(ms)`. A case budget of 0 means no limit in both Vitest
 * and Playwright and passes. Waits such as `waitFor(..., { timeout })` and
 * `findBy*(..., { timeout })` are not case budgets and are left alone.
 */
function lowCaseTimeouts(source, floor) {
  const code = stripComments(source)
  const lowered = []
  for (const match of code.matchAll(
    /\b(?:it|test|beforeEach|afterEach|beforeAll|afterAll|describe)(?:\.(?!each\b)\w+)?\(/g,
  )) {
    const call = callText(code, match.index + match[0].length - 1)
    if (call === null) {
      continue
    }
    lowered.push(...trailingTimeout(call, floor), ...optionTimeout(call, floor))
  }
  // `it.each(table)` returns a function that is then called with the case.
  // The table is not a budget, so the scan continues past it and inspects
  // that call instead.
  for (const match of code.matchAll(/\b(?:it|test|describe)\.each\s*\(/g)) {
    const openIndex = match.index + match[0].length - 1
    const table = callText(code, openIndex)
    if (table === null) {
      continue
    }
    const rest = code.slice(openIndex + table.length)
    const open = rest.search(/\S/)
    if (open === -1 || rest[open] !== '(') {
      continue
    }
    const call = callText(code, openIndex + table.length + open)
    if (call !== null) {
      lowered.push(...trailingTimeout(call, floor), ...optionTimeout(call, floor))
    }
  }
  for (const match of code.matchAll(/\btest\.setTimeout\(\s*([\d_]+)\s*\)/g)) {
    const ms = toMs(match[1])
    if (ms !== 0 && ms < floor) {
      lowered.push(ms)
    }
  }
  return lowered
}

/**
 * The bare waits in a test source, which lack the shared budget: a
 * `vi.waitFor` or `vi.waitUntil` call (directly or by bracket access), a
 * destructuring that binds either name from `vi`, and a reference to either
 * assigned without being called (`const wait = vi.waitFor`).
 */
function bareWaitUses(source) {
  const code = stripComments(source)
  return [
    ...(code.match(/\bvi\s*\.\s*(?:waitFor|waitUntil)\s*\(/g) ?? []),
    ...(code.match(/\bvi\s*\[\s*(['"])(?:waitFor|waitUntil)\1\s*\]\s*\(/g) ?? []),
    ...(code.match(
      /\b(?:const|let|var)\s*\{[^}]*\b(?:waitFor|waitUntil)\b[^}]*\}\s*=\s*vi\b/g,
    ) ?? []),
    ...(code.match(
      /\(\s*\{[^}]*\b(?:waitFor|waitUntil)\b[^}]*\}\s*=\s*vi\s*\)/g,
    ) ?? []),
    // A reference kept without being called: the call's one-second default
    // applies once it is invoked, and the alias hides it from the call scan.
    ...(code.match(
      /(?:\b(?:const|let|var)\s+[\w$]+\s*=|\b[\w$]+\s*=)\s*vi\s*\.\s*(?:waitFor|waitUntil)\b(?!\s*\()/g,
    ) ?? []),
    ...(code.match(
      /(?:\b(?:const|let|var)\s+[\w$]+\s*=|\b[\w$]+\s*=)\s*vi\s*\[\s*(['"])(?:waitFor|waitUntil)\1\s*\](?!\s*\()/g,
    ) ?? []),
  ]
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
function asyncBudgetScale(source) {
  const match = /asyncUtilTimeout\s*:\s*asyncBudgetMs\s*\(\s*((?:[^()]|\([^()]*\))+?)\s*\)/.exec(
    stripComments(source),
  )
  return match?.[1] ?? null
}

/** The expressions that carry the caller's scale into a setup. */
const SCALE_SOURCE = /^(?:gateTimeScale\(\)|__SPEED_GATE_TIME_SCALE__)$/

/** Every test source under `dir`, skipping installed and built output. */
function testSources(dir) {
  const sources = []
  const walk = (current) => {
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      if (entry.name === 'node_modules' || entry.name === 'dist') {
        continue
      }
      const path = join(current, entry.name)
      if (entry.isDirectory()) {
        walk(path)
      } else if (/\.test\.(?:[cm]?[jt]sx?)$/.test(entry.name)) {
        sources.push(path)
      }
    }
  }
  walk(dir)
  return sources
}

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
    const source = stripComments(
      readFileSync(join(webRoot, 'vitest.config.ts'), 'utf8'),
    )
    expect(source).toContain('sharedTestOptions(')
    expect(source).not.toMatch(/testTimeout\s*:/)
    expect(source).not.toMatch(/hookTimeout\s*:/)
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
      const scale = asyncBudgetScale(readFileSync(path, 'utf8'))
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

  /** Every test source the budgets and the wait rule apply to. */
  const scopedTestSources = [
    ...testPackages.flatMap((pkg) => testSources(pkg.dir)),
    ...testSources(demoWebRoot),
  ]

  it('keeps no case budget below the shared one', () => {
    // The unscaled constant: a caller's GATE_TIME_SCALE must not change this
    // tree's verdict. Comparing the scaled options would let a case budget
    // between the floor and the scaled value pass only when a caller raised
    // the scale.
    const floor = SHARED_TIMEOUT_MS
    const offenders = []
    for (const source of scopedTestSources) {
      for (const ms of lowCaseTimeouts(readFileSync(source, 'utf8'), floor)) {
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
    for (const source of scopedTestSources) {
      const count = bareWaitUses(readFileSync(source, 'utf8')).length
      if (count > 0) {
        offenders.push(`${source}: ${count} bare wait use(s)`)
      }
    }
    expect(
      offenders,
      'call waitForCondition (@speed/test-utils/wait) instead of vi.waitFor '
        + 'or vi.waitUntil, directly, through a destructured alias, or through '
        + "a reference kept uncalled; both waits' one-second default is outside "
        + 'the setup budget and GATE_TIME_SCALE',
    ).toEqual([])
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
    expect(bareWaitUses('const wait = vi.waitFor')).toHaveLength(1)
    expect(bareWaitUses('let wait = vi.waitUntil')).toHaveLength(1)
    expect(bareWaitUses("const wait = vi['waitFor']")).toHaveLength(1)
    // A string that names the call is prose, not a reference; a called wait
    // is counted once, by the call scan rather than the reference scan.
    expect(bareWaitUses("it('names vi.waitFor in prose', () => {})")).toHaveLength(0)
    expect(bareWaitUses('await waitForCondition(() => {})')).toHaveLength(0)
    expect(bareWaitUses('const { spyOn } = vi')).toHaveLength(0)
    expect(bareWaitUses('// await vi.waitFor(() => {})')).toHaveLength(0)
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
    expect(SCALE_SOURCE.test('gateTimeScale()')).toBe(true)
    expect(SCALE_SOURCE.test('__SPEED_GATE_TIME_SCALE__')).toBe(true)
    expect(SCALE_SOURCE.test('1')).toBe(false)
    expect(SCALE_SOURCE.test('0.5')).toBe(false)
  })

  for (const pkg of testPackages) {
    it(`${pkg.name} takes its test options from the shared preset`, async () => {
      const configPath = join(pkg.dir, 'vitest.config.ts')
      const source = stripComments(readFileSync(configPath, 'utf8'))

      // The spread is what makes the preset the source of the options;
      // a hand-written block (or a locally pinned timeout) is exactly the
      // omission this guard exists to catch. The import carries the .ts
      // extension because Vite's native config loader warns on an
      // extensionless one and plans to reject it.
      expect(source).toMatch(
        /from '\.\.\/(?:\.\.\/)?vitest\.shared\.ts'/,
      )
      expect(source).toContain('sharedTestOptions(')
      expect(source).not.toMatch(/testTimeout\s*:/)
      expect(source).not.toMatch(/hookTimeout\s*:/)

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
