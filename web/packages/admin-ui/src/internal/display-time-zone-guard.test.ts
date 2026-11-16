/**
 * Structural guard: every display instant renders in a zone the account
 * resolved, never a hard-coded, implicit or hand-rolled one.
 *
 * Every instant a surface shows goes through ui-kit's `useFormatInstant`,
 * which resolves the zone once (the host's `DisplayTimeZoneProvider`, then
 * the browser's own zone, then UTC) and the active language. Each bypass
 * below pins a surface to a zone the account did not choose, or to the
 * engine's process-local default, which makes the same account render
 * differently depending on which machine ran the code:
 *
 * - a `timeZone` option whose value is a literal zone, or a literal
 *   fallback such as `zone ?? 'UTC'`, or a JSX `timeZone="GMT"` attribute;
 * - a name the file directly declares in a `const`, `let` or `var`
 *   statement as its first declarator, bound to an expression carrying a
 *   zone literal (`const zone = stored ?? 'UTC'`), and then passes as a
 *   zone, whether as the second argument of a wall-clock conversion or
 *   through a shorthand property, `{ timeZone }` or
 *   `{ style, timeZone }`. Only that first declarator is tracked: a name
 *   bound by destructuring, a later declarator in the same statement, a
 *   constant imported from another file, and a quoted key such as
 *   `{ 'timeZone': 'UTC' }` are not seen, so each stays a review
 *   obligation;
 * - the raw `formatInstant(value, language)` imported from `@speed/i18n`
 *   -- named, aliased or reached through a namespace import -- called
 *   without a `timeZone` option, so the constructor silently takes the
 *   engine default;
 * - `toLocaleString` / `toLocaleDateString` / `toLocaleTimeString`, which
 *   carry no display zone at all;
 * - a bare `Intl.DateTimeFormat` construction outside the resolution chain;
 * - a literal zone passed as the second argument of `instantToWallClock` or
 *   `wallClockToInstant`, which would read or build a wall clock in a zone
 *   the account did not choose.
 *
 * The suite sweeps every package's non-test source under
 * `web/packages/<package>/src` and the demo host's
 * `starters/demo/web/src`. It fails naming the file, line and rule.
 *
 * Test files are skipped: a suite may format a fixed-zone expectation to
 * derive what the resolved display zone should render, and that is not a
 * surface's decision. The demo host's harness at
 * `starters/demo/web/src/test-utils` is skipped for the same reason: it
 * mounts the production stack under a zone a case chooses, so its fixtures
 * are scaffolding rather than a surface. That one directory is named by its
 * full path; a `test-utils` directory elsewhere under a package's `src` is
 * scanned like any other source, because a name alone does not make a file
 * scaffolding. `dist` and `node_modules` are skipped because build output
 * and dependencies are not source.
 *
 * A template literal's text is not code, but its `${...}` expressions are:
 * a banned call inside an interpolation is reported, while a template that
 * merely names one is not. The same reader keeps string and comment
 * contents out of every rule, so documentation prose and message text
 * cannot trip it.
 *
 * A surface usually binds the hook's formatter to the same local name
 * (`const formatInstant = useFormatInstant()`), whose second argument is a
 * style, not a language, and which carries the resolved zone already. The
 * name alone therefore cannot say which function a call reaches. The
 * structural marker is the import: only the raw function imported from
 * `@speed/i18n` takes `(value, language, options)`, so only in a file that
 * imports it -- under its own name, an alias or a namespace -- must a
 * two-or-more-argument call name a `timeZone`.
 *
 * The allowlisted files are the resolution chain itself: `time.ts` and
 * `wall-clock.ts` construct `Intl.DateTimeFormat` with an explicit zone,
 * and `use-format-instant.ts` is the bridge that hands `formatInstant` the
 * zone `useResolvedTimeZone` resolved. Each entry is asserted to exist in
 * the scanned set, so a stale exemption cannot silently widen the guard.
 *
 * Transport formats are not display zones. The audit export file name, for
 * example, is derived from `toISOString`, which writes the instant in UTC
 * because the file name carries an instant rather than a reading -- it
 * names no `timeZone` option and calls no banned API, so it needs no
 * exemption here.
 *
 * The host is `admin-ui`, whose suite already reads files through node:fs
 * and has the Node types. Nothing here renders, so the case does not
 * depend on the DOM.
 */

import { readdirSync, readFileSync } from 'node:fs'
import type { Dirent } from 'node:fs'
import { dirname, join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

/** `web/packages`, three directories above this test's own `internal/`. */
const packagesDir = join(
  dirname(fileURLToPath(import.meta.url)),
  '..',
  '..',
  '..',
)

/** The repository root, two directories above `web/packages`. */
const repoRoot = join(packagesDir, '..', '..')

/** The demo host's source: a surface outside the package library. */
const demoSourceDir = join(repoRoot, 'starters', 'demo', 'web', 'src')

/** Source extensions a package may carry under `src`. */
const SOURCE_EXTENSIONS = new Set([
  '.ts',
  '.tsx',
  '.mts',
  '.cts',
  '.js',
  '.jsx',
  '.mjs',
  '.cjs',
])

/** A test file's name, excluded from the scan. */
const TEST_FILE = /\.(test|spec)\.[cm]?[jt]sx?$/

/**
 * Directories that are never source: `dist` and `node_modules` are build
 * output and dependencies.
 */
const SKIPPED_DIRECTORIES = new Set(['dist', 'node_modules'])

/**
 * The demo host's suite scaffold, skipped by its full path. It mounts the
 * production stack under a zone a case chooses, so its zone fixtures are
 * scaffolding rather than a surface's decision.
 */
const DEMO_TEST_UTILS_DIR = join(demoSourceDir, 'test-utils')

/**
 * The files the guard exempts, each with the reason it may use a banned
 * primitive. An entry that stops existing fails the suite, so an exemption
 * cannot outlive the code it was written for.
 */
const ALLOWED_FILES = new Map<string, string>([
  [
    'web/packages/i18n/src/time.ts',
    'the resolution chain and formatInstant: constructs Intl.DateTimeFormat with the zone the chain resolved',
  ],
  [
    'web/packages/i18n/src/wall-clock.ts',
    "the wall-clock conversion: constructs Intl.DateTimeFormat with its caller's named zone",
  ],
  [
    'web/packages/ui-kit/src/use-format-instant.ts',
    'the one bridge a surface formats through: hands formatInstant the resolved display zone',
  ],
])

/** The rule names reported beside each offending line. */
const RULES = {
  hardCodedZone: 'hard-coded time zone literal',
  missingZone: 'formatInstant without a timeZone option',
  localeString: 'toLocale*String display formatting',
  dateTimeFormat: 'bare Intl.DateTimeFormat construction',
  literalPositionalZone: 'literal time zone passed to a wall-clock conversion',
} as const

/** Appends every non-test source file under `directory` to `files`. */
function collectSourceFiles(directory: string, files: string[]): void {
  let entries: Dirent[]
  try {
    entries = readdirSync(directory, { withFileTypes: true })
  } catch {
    // A directory that does not exist contributes nothing.
    return
  }
  for (const entry of entries) {
    const full = join(directory, entry.name)
    if (entry.isDirectory()) {
      if (
        !SKIPPED_DIRECTORIES.has(entry.name) &&
        full !== DEMO_TEST_UTILS_DIR
      ) {
        collectSourceFiles(full, files)
      }
      continue
    }
    if (!entry.isFile() || TEST_FILE.test(entry.name)) {
      continue
    }
    const dot = entry.name.lastIndexOf('.')
    if (dot === -1 || !SOURCE_EXTENSIONS.has(entry.name.slice(dot))) {
      continue
    }
    files.push(full)
  }
}

/** Every scanned source file, sorted so a failure reads the same each run. */
function scannedFiles(): string[] {
  const files: string[] = []
  for (const entry of readdirSync(packagesDir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      collectSourceFiles(join(packagesDir, entry.name, 'src'), files)
    }
  }
  collectSourceFiles(demoSourceDir, files)
  return [...new Set(files)].sort()
}

/** A file's path relative to the repository root, in posix form. */
function displayPath(file: string): string {
  return relative(repoRoot, file).split(sep).join('/')
}

/** The 1-based line an offset falls on. */
function lineOf(text: string, index: number): number {
  let line = 1
  for (let i = 0; i < index && i < text.length; i += 1) {
    if (text[i] === '\n') {
      line += 1
    }
  }
  return line
}

/**
 * Two aligned views of one source file, produced in a single pass.
 *
 * - `withStrings` blanks comment contents only, so the zone literals and
 *   import specifiers that live inside strings stay readable.
 * - `codeOnly` also blanks string and template-literal text, so an
 *   identifier or property name inside a message cannot be mistaken for a
 *   call, while a template's `${...}` expressions stay code.
 *
 * Every offset and newline survives in both views, so line numbers computed
 * on either one match the original file. Template-text delimiters are kept
 * in `codeOnly` and an interpolation's braces balance, which keeps the call
 * argument splitter's nesting count correct.
 */
function maskSource(content: string): { withStrings: string; codeOnly: string } {
  const withStrings = content.split('')
  const codeOnly = content.split('')

  const blankBoth = (index: number): void => {
    if (content[index] !== '\n') {
      withStrings[index] = ' '
      codeOnly[index] = ' '
    }
  }
  const blankText = (index: number): void => {
    if (content[index] !== '\n') {
      codeOnly[index] = ' '
    }
  }

  /** Scans code from `start`; with `stopAtBrace`, stops at the `}` closing
   * the current interpolation and returns its index, unconsumed. */
  function scanCode(start: number, stopAtBrace: boolean): number {
    let i = start
    let depth = 0
    while (i < content.length) {
      const c = content[i]
      const next = content[i + 1]
      if (c === '/' && next === '/') {
        blankBoth(i)
        blankBoth(i + 1)
        i += 2
        while (i < content.length && content[i] !== '\n') {
          blankBoth(i)
          i += 1
        }
        continue
      }
      if (c === '/' && next === '*') {
        blankBoth(i)
        blankBoth(i + 1)
        i += 2
        while (i < content.length) {
          if (content[i] === '*' && content[i + 1] === '/') {
            blankBoth(i)
            blankBoth(i + 1)
            i += 2
            break
          }
          blankBoth(i)
          i += 1
        }
        continue
      }
      if (c === "'" || c === '"') {
        i = scanQuoted(i, c)
        continue
      }
      if (c === '`') {
        i = scanTemplate(i)
        continue
      }
      if (stopAtBrace) {
        if (c === '{') {
          depth += 1
        } else if (c === '}') {
          if (depth === 0) {
            return i
          }
          depth -= 1
        }
      }
      i += 1
    }
    return i
  }

  /** Blanks a quoted string's contents in `codeOnly`. */
  function scanQuoted(start: number, quote: string): number {
    let i = start + 1
    while (i < content.length) {
      const c = content[i]
      if (c === '\\') {
        blankText(i)
        blankText(i + 1)
        i += 2
        continue
      }
      blankText(i)
      if (c === quote) {
        return i + 1
      }
      i += 1
    }
    return i
  }

  /** Blanks a template literal's text while scanning its interpolations. */
  function scanTemplate(start: number): number {
    let i = start + 1
    while (i < content.length) {
      const c = content[i]
      const next = content[i + 1]
      if (c === '\\') {
        blankText(i)
        blankText(i + 1)
        i += 2
        continue
      }
      if (c === '`') {
        blankText(i)
        return i + 1
      }
      if (c === '$' && next === '{') {
        i = scanCode(i + 2, true)
        if (i < content.length) {
          i += 1
        }
        continue
      }
      blankText(i)
      i += 1
    }
    return i
  }

  scanCode(0, false)
  return { withStrings: withStrings.join(''), codeOnly: codeOnly.join('') }
}

/** Reports every match of `pattern` in `text`, with its line. */
function reportMatches(
  text: string,
  pattern: RegExp,
  rule: string,
  findings: Finding[],
): void {
  const flags = pattern.flags.includes('g')
    ? pattern.flags
    : `${pattern.flags}g`
  const global = new RegExp(pattern.source, flags)
  let match: RegExpExecArray | null
  while ((match = global.exec(text)) !== null) {
    findings.push({ rule, line: lineOf(text, match.index) })
    if (match[0].length === 0) {
      global.lastIndex += 1
    }
  }
}

/** Whether the `formatInstant` at `index` is a declaration, not a call. */
function isDeclaration(code: string, index: number): boolean {
  return /\bfunction\s*$/.test(code.slice(Math.max(0, index - 32), index))
}

/** A reported violation: the rule and the 1-based line it sits on. */
interface Finding {
  readonly rule: string
  readonly line: number
}

/** The local names a file binds from `@speed/i18n`, and its namespaces. */
interface I18nImports {
  /** Local name -> imported name, for `{ a, b as c }` specifiers. */
  readonly named: ReadonlyMap<string, string>
  /** Local names bound by `import * as ns from '@speed/i18n'`. */
  readonly namespaces: ReadonlySet<string>
}

/**
 * Every binding a file takes from `@speed/i18n` or one of its subpaths.
 * Only these bindings reach the raw signatures; a namespace is recorded by
 * its local name so `ns.formatInstant(...)` is read as the same function.
 */
function i18nImports(content: string, codeOnly: string): I18nImports {
  const named = new Map<string, string>()
  const namespaces = new Set<string>()
  const statement =
    /import\s+([^;'"]*?)\s+from\s*['"]@speed\/i18n(?:\/[^'"]*)?['"]/g
  let match: RegExpExecArray | null
  while ((match = statement.exec(content)) !== null) {
    // The module specifier is a string, so the statement is found in the
    // view that keeps strings. The keyword must also be code: a sentence
    // or a template that merely quotes an import binds nothing.
    if (codeOnly.slice(match.index, match.index + 6) !== 'import') {
      continue
    }
    const clause = (match[1] ?? '').trim()
    const namespace = /^\*\s+as\s+([A-Za-z_$][\w$]*)$/.exec(clause)
    const namespaceName = namespace?.[1]
    if (namespaceName !== undefined) {
      namespaces.add(namespaceName)
      continue
    }
    const braces = /\{([\s\S]*)\}/.exec(clause)
    if (braces === null) {
      continue
    }
    for (const raw of (braces[1] ?? '').split(',')) {
      const specifier = raw.trim().replace(/^type\s+/, '')
      if (specifier === '') {
        continue
      }
      const parts = specifier.split(/\s+as\s+/).map((part) => part.trim())
      const importedName = parts[0]
      const localName = parts[1] ?? importedName
      if (
        importedName !== undefined &&
        /^[A-Za-z_$][\w$]*$/.test(importedName) &&
        localName !== undefined &&
        /^[A-Za-z_$][\w$]*$/.test(localName)
      ) {
        named.set(localName, importedName)
      }
    }
  }
  return { named, namespaces }
}

/** Escapes a local identifier for use inside a RegExp source. */
function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/** One call argument in both views of the source. */
interface CallArgument {
  /** The argument with string and template text blanked. */
  readonly code: string
  /** The argument with string contents intact; comments are already blank. */
  readonly raw: string
}

/**
 * Splits a call's top-level arguments, given the index of its opening
 * parenthesis. Boundaries come from the code-only view, so a comma inside
 * a string or a template's text cannot split an argument; the raw view
 * supplies the literal text a zone check reads.
 */
function callArguments(
  codeOnly: string,
  source: string,
  openParen: number,
): CallArgument[] | null {
  const args: CallArgument[] = []
  let depth = 0
  let start = openParen + 1
  const push = (end: number): void => {
    args.push({
      code: codeOnly.slice(start, end).trim(),
      raw: source.slice(start, end).trim(),
    })
  }
  for (let i = openParen; i < codeOnly.length; i += 1) {
    const c = codeOnly[i]
    if (c === '(' || c === '[' || c === '{') {
      depth += 1
    } else if (c === ')' || c === ']' || c === '}') {
      depth -= 1
      if (depth === 0) {
        if (codeOnly.slice(start, i).trim() !== '' || args.length > 0) {
          push(i)
        }
        return args
      }
    } else if (c === ',' && depth === 1) {
      push(i)
      start = i + 1
    }
  }
  return null
}

/** Whether an expression is a single string literal, template included. */
function isStringLiteral(expression: string): boolean {
  const text = expression.trim()
  if (text.length < 2) {
    return false
  }
  const quote = text[0]
  if (quote === "'") {
    return /^'(?:\\[\s\S]|[^'\\])*'$/.test(text)
  }
  if (quote === '"') {
    return /^"(?:\\[\s\S]|[^"\\])*"$/.test(text)
  }
  if (quote === '`') {
    return /^`(?:\\[\s\S]|[^`\\$]|\$(?!\{))*`$/.test(text)
  }
  return false
}

/**
 * Whether a string names a time zone rather than a style tier or some
 * other option value. Matches the UTC/fixed-offset family and IANA names
 * with a region separator, so a neighbouring `style: 'short'` is not read
 * as a zone.
 */
function isTimeZoneLiteral(value: string): boolean {
  const zone = value.trim()
  if (zone === '') {
    return false
  }
  return (
    /^(?:UTC|UCT|GMT|Universal|Z)$/i.test(zone) ||
    /^Etc\/(?:UTC|UCT|GMT|GMT[+-]\d{1,2})$/i.test(zone) ||
    /^(?:UTC|GMT)[+-]\d{1,2}(?::\d{2})?$/i.test(zone) ||
    /^[A-Za-z][A-Za-z0-9_+-]*(?:\/[A-Za-z0-9_+-]+)+$/.test(zone)
  )
}

/** Whether a string contains a zone literal, wherever it sits in the text. */
function containsZoneLiteral(value: string): boolean {
  const literal = /(['"`])([^'"`\\]*)\1/g
  let match: RegExpExecArray | null
  while ((match = literal.exec(value)) !== null) {
    if (isTimeZoneLiteral(match[2] ?? '')) {
      return true
    }
  }
  return false
}

/**
 * The end of a declaration's initializer: a top-level semicolon, a closer at
 * depth zero, or a newline that does not continue an expression.
 */
function declarationValueEnd(text: string, start: number): number {
  let depth = 0
  for (let i = start; i < text.length; i += 1) {
    const c = text[i]
    if (c === '(' || c === '[' || c === '{') {
      depth += 1
      continue
    }
    if (c === ')' || c === ']' || c === '}') {
      if (depth === 0) {
        return i
      }
      depth -= 1
      continue
    }
    if (c === ';' && depth === 0) {
      return i
    }
    if (c === '\n' && depth === 0) {
      const before = text.slice(start, i).trimEnd()
      if (!/(?:[=+\-*/%&|?:,([]|\?\?|\|\||&&)$/.test(before)) {
        return i
      }
    }
  }
  return text.length
}

/**
 * The names a file binds to an initializer carrying a zone literal. Passing
 * one of them as a zone is the same bypass as writing the literal there, but
 * the use site names no literal for the text scan to see, so the binding is
 * what makes it visible.
 */
function zoneBoundNames(
  withStrings: string,
  codeOnly: string,
): ReadonlySet<string> {
  const names = new Set<string>()
  const declaration =
    /\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=;\n]+)?=/g
  let match: RegExpExecArray | null
  while ((match = declaration.exec(codeOnly)) !== null) {
    const name = match[1]
    if (name === undefined) {
      continue
    }
    const equals = match.index + match[0].length - 1
    const end = declarationValueEnd(codeOnly, equals + 1)
    if (containsZoneLiteral(withStrings.slice(equals + 1, end))) {
      names.add(name)
    }
  }
  return names
}

/** The first zone-bound name a piece of code references, or null. */
function referencedZoneName(
  code: string,
  zoneNames: ReadonlySet<string>,
): string | null {
  if (zoneNames.size === 0) {
    return null
  }
  const identifier = /(?<![.\w$])([A-Za-z_$][\w$]*)(?!\s*:)/g
  let match: RegExpExecArray | null
  while ((match = identifier.exec(code)) !== null) {
    const name = match[1]
    if (name !== undefined && zoneNames.has(name)) {
      return name
    }
  }
  return null
}

/** The nesting depth at `index` within `text`. */
function depthAt(text: string, index: number): number {
  let depth = 0
  for (let i = 0; i < index; i += 1) {
    const c = text[i]
    if (c === '(' || c === '[' || c === '{') {
      depth += 1
    } else if (c === ')' || c === ']' || c === '}') {
      depth -= 1
    }
  }
  return depth
}

/**
 * Whether a literal is an operand of a comparison rather than the option's
 * value: `timeZone: zone === 'UTC' ? undefined : zone` decides nothing about
 * the zone it passes.
 */
function isComparisonOperand(
  text: string,
  index: number,
  length: number,
): boolean {
  const before = text.slice(0, index).trimEnd()
  const after = text.slice(index + length).trimStart()
  return (
    /(?:===|!==|==|!=)$/.test(before) || /^(?:===|!==|==|!=)/.test(after)
  )
}

/** A string literal's contents, or null when the expression is not one. */
function stringLiteralText(expression: string): string | null {
  const text = expression.trim()
  if (!isStringLiteral(text)) {
    return null
  }
  return text.slice(1, -1)
}

/**
 * The end of the value that follows an option key, at its own nesting: a
 * top-level comma, semicolon or closer ends it, as does a newline that
 * starts another member. A newline after an operator is a continuation and
 * does not end it.
 */
function optionValueEnd(text: string, start: number): number {
  let depth = 0
  for (let i = start; i < text.length; i += 1) {
    const c = text[i]
    if (c === '(' || c === '[' || c === '{') {
      depth += 1
      continue
    }
    if (c === ')' || c === ']' || c === '}') {
      if (depth === 0) {
        return i
      }
      depth -= 1
      continue
    }
    if (c === ',' || c === ';') {
      if (depth === 0) {
        return i
      }
      continue
    }
    if (c === '\n' && depth === 0) {
      const rest = text.slice(i + 1)
      if (
        /^\s*(?:readonly\s+)?[A-Za-z_$][\w$]*\s*\??\s*:/.test(rest) ||
        /^\s*[}\])]/.test(rest)
      ) {
        return i
      }
    }
  }
  return text.length
}

/**
 * Reports every literal zone named as a `timeZone` value, whether it is the
 * whole value, the fallback of `??`/`||`/a ternary, or a JSX attribute.
 */
function reportTimeZoneLiterals(
  text: string,
  codeOnly: string,
  zoneNames: ReadonlySet<string>,
  findings: Finding[],
): void {
  const option = /\btimeZone\s*:\s*/g
  let match: RegExpExecArray | null
  while ((match = option.exec(text)) !== null) {
    // A `timeZone: '...'` sentence inside a string or a template's text is
    // prose, not an option; only the code view keeps the key.
    if (codeOnly.slice(match.index, match.index + 8) !== 'timeZone') {
      continue
    }
    const start = match.index + match[0].length
    const end = optionValueEnd(text, start)
    const value = text.slice(start, end)
    const codeValue = codeOnly.slice(start, end)
    const literal = /(['"`])([^'"`\\]*)\1/g
    let zone: RegExpExecArray | null
    while ((zone = literal.exec(value)) !== null) {
      const observed = zone[2] ?? ''
      if (!isTimeZoneLiteral(observed)) {
        continue
      }
      // A literal that is a comparison operand or an argument of a call is
      // not the option's value: `zone === 'UTC'` and `lookup('a/b')` both
      // name a zone-shaped string without choosing a zone.
      if (
        isComparisonOperand(value, zone.index, zone[0].length) ||
        depthAt(value, zone.index) > 0
      ) {
        continue
      }
      findings.push({
        rule: RULES.hardCodedZone,
        line: lineOf(text, start + zone.index),
      })
    }
    // The option's value may be a name bound to a zone literal elsewhere in
    // the file, which no literal at this position would reveal.
    const tainted = referencedZoneName(codeValue, zoneNames)
    if (tainted !== null) {
      const offset = codeValue.indexOf(tainted)
      findings.push({
        rule: RULES.hardCodedZone,
        line: lineOf(text, start + Math.max(0, offset)),
      })
    }
  }

  // The shorthand property: `formatInstant(v, l, { timeZone })`, where the
  // name carries a zone literal from its declaration. It is a zone use in
  // any member position, so `{ style: 'long', timeZone }` reports too.
  if (zoneNames.has('timeZone')) {
    const shorthand = /[{,]\s*timeZone\s*(?=[,}])/g
    while ((match = shorthand.exec(codeOnly)) !== null) {
      const offset = match.index + match[0].indexOf('timeZone')
      findings.push({
        rule: RULES.hardCodedZone,
        line: lineOf(codeOnly, offset),
      })
    }
  }

  const attribute = /\btimeZone\s*=\s*/g
  while ((match = attribute.exec(text)) !== null) {
    if (codeOnly.slice(match.index, match.index + 8) !== 'timeZone') {
      continue
    }
    const start = match.index + match[0].length
    const quote = text[start]
    if (quote === '{') {
      // The JSX expression form: timeZone={"GMT"}.
      const end = text.indexOf('}', start + 1)
      if (end === -1) {
        continue
      }
      const expression = text.slice(start + 1, end)
      const literal = /(['"`])([^'"`\\]*)\1/g
      let zone: RegExpExecArray | null
      while ((zone = literal.exec(expression)) !== null) {
        if (
          isTimeZoneLiteral(zone[2] ?? '') &&
          !isComparisonOperand(expression, zone.index, zone[0].length) &&
          depthAt(expression, zone.index) === 0
        ) {
          findings.push({
            rule: RULES.hardCodedZone,
            line: lineOf(text, start + 1 + zone.index),
          })
        }
      }
      const tainted = referencedZoneName(
        codeOnly.slice(start + 1, end),
        zoneNames,
      )
      if (tainted !== null) {
        findings.push({
          rule: RULES.hardCodedZone,
          line: lineOf(text, start + 1),
        })
      }
      continue
    }
    if (quote !== "'" && quote !== '"' && quote !== '`') {
      continue
    }
    const end = text.indexOf(quote, start + 1)
    if (end === -1) {
      continue
    }
    if (isTimeZoneLiteral(text.slice(start + 1, end))) {
      findings.push({ rule: RULES.hardCodedZone, line: lineOf(text, start) })
    }
  }
}

/**
 * Reports the raw `formatInstant` called without a `timeZone` option. The
 * reach is the import: its own name, an alias, or a namespace member.
 */
function reportRawFormatInstant(
  codeOnly: string,
  source: string,
  imports: I18nImports,
  findings: Finding[],
): void {
  const calls: { pattern: RegExp; declarationSafe: boolean }[] = []
  for (const [local, imported] of imports.named) {
    if (imported === 'formatInstant') {
      calls.push({
        pattern: new RegExp(`(?<![.\\w$])${escapeRegExp(local)}\\s*\\(`, 'g'),
        declarationSafe: true,
      })
    }
  }
  for (const namespace of imports.namespaces) {
    calls.push({
      pattern: new RegExp(
        `(?<![.\\w$])${escapeRegExp(namespace)}\\s*\\??\\.\\s*formatInstant\\s*\\(`,
        'g',
      ),
      declarationSafe: false,
    })
  }

  for (const call of calls) {
    let match: RegExpExecArray | null
    while ((match = call.pattern.exec(codeOnly)) !== null) {
      if (call.declarationSafe && isDeclaration(codeOnly, match.index)) {
        continue
      }
      const openParen = codeOnly.indexOf('(', match.index)
      const args = callArguments(codeOnly, source, openParen)
      if (args === null || args.length < 2) {
        continue
      }
      const options = args[2]
      if (options === undefined || !/\btimeZone\b/.test(options.code)) {
        findings.push({
          rule: RULES.missingZone,
          line: lineOf(codeOnly, match.index),
        })
      }
    }
  }
}

/** The wall-clock conversions whose second parameter is a time zone. */
const WALL_CLOCK_CONVERSIONS = new Set(['instantToWallClock', 'wallClockToInstant'])

/**
 * Reports a literal zone passed positionally to a wall-clock conversion:
 * the conversion would then read or build a reading in a zone the account
 * did not choose.
 */
function reportLiteralConversionZones(
  codeOnly: string,
  source: string,
  imports: I18nImports,
  zoneNames: ReadonlySet<string>,
  findings: Finding[],
): void {
  const calls: RegExp[] = []
  for (const [local, imported] of imports.named) {
    if (WALL_CLOCK_CONVERSIONS.has(imported)) {
      calls.push(
        new RegExp(`(?<![.\\w$])${escapeRegExp(local)}\\s*\\(`, 'g'),
      )
    }
  }
  for (const namespace of imports.namespaces) {
    calls.push(
      new RegExp(
        `(?<![.\\w$])${escapeRegExp(namespace)}\\s*\\??\\.\\s*(?:instantToWallClock|wallClockToInstant)\\s*\\(`,
        'g',
      ),
    )
  }

  for (const pattern of calls) {
    let match: RegExpExecArray | null
    while ((match = pattern.exec(codeOnly)) !== null) {
      const openParen = codeOnly.indexOf('(', match.index)
      const args = callArguments(codeOnly, source, openParen)
      if (args === null || args.length < 2) {
        continue
      }
      const zone = args[1]
      if (zone === undefined) {
        continue
      }
      const literalText = stringLiteralText(zone.raw)
      if (
        (literalText !== null && isTimeZoneLiteral(literalText)) ||
        referencedZoneName(zone.code, zoneNames) !== null
      ) {
        findings.push({
          rule: RULES.literalPositionalZone,
          line: lineOf(codeOnly, match.index),
        })
      }
    }
  }
}

/** Every banned form in one file's text. */
function findViolations(content: string): Finding[] {
  const findings: Finding[] = []
  const { withStrings, codeOnly } = maskSource(content)

  reportMatches(
    codeOnly,
    /\.toLocale(?:Date|Time)?String\s*\(/,
    RULES.localeString,
    findings,
  )
  reportMatches(
    codeOnly,
    /\bIntl\.DateTimeFormat\s*\(/,
    RULES.dateTimeFormat,
    findings,
  )

  // Zone literals live inside strings, so they are read from the view that
  // keeps string contents; comments are already blank in it. A name bound to
  // one is tainted, so passing it as a zone is reported at the use site.
  const zoneNames = zoneBoundNames(withStrings, codeOnly)
  reportTimeZoneLiterals(withStrings, codeOnly, zoneNames, findings)

  // The raw signatures are reachable only through the module's bindings:
  // the import is the structural marker, under its own name, an alias or a
  // namespace.
  const imports = i18nImports(withStrings, codeOnly)
  reportRawFormatInstant(codeOnly, withStrings, imports, findings)
  reportLiteralConversionZones(
    codeOnly,
    withStrings,
    imports,
    zoneNames,
    findings,
  )

  // One position can be caught by more than one test (a literal fallback
  // whose name is also bound to it); the file reports each rule and line
  // once.
  return [
    ...new Map(findings.map((f) => [`${f.rule}:${f.line}`, f])).values(),
  ]
}

describe('display time zone guard', () => {
  const files = scannedFiles()
  const scanned = new Set(files.map(displayPath))

  it('scans the package and demo surfaces it means to guard', () => {
    // A wrong root would make the sweep below pass with nothing read; name
    // the files the guard exists for, not just a count.
    expect(scanned.has('web/packages/admin-ui/src/AuditPage.tsx')).toBe(true)
    expect(scanned.has('starters/demo/web/src/app.tsx')).toBe(true)
  })

  it('keeps every allowlist entry present in the scanned sources', () => {
    for (const [path, reason] of ALLOWED_FILES) {
      expect(
        scanned.has(path),
        `Allowlist entry is not a scanned source: ${path} (${reason})`,
      ).toBe(true)
    }
  })

  it('scans a surface but not the test-utils scaffolding beside it', () => {
    // The demo host's own sources are surfaces and are scanned; the
    // `test-utils` directory under it is a suite's scaffold, whose zone
    // fixtures are not a surface's display decision.
    expect(scanned.has('starters/demo/web/src/app.tsx')).toBe(true)
    expect(scanned.has('starters/demo/web/src/test-utils/render.tsx')).toBe(
      false,
    )
  })

  it('formats no instant through a fixed, implicit or hand-rolled zone', () => {
    const offenders: string[] = []
    for (const file of files) {
      const path = displayPath(file)
      if (ALLOWED_FILES.has(path)) {
        continue
      }
      for (const finding of findViolations(readFileSync(file, 'utf8'))) {
        offenders.push(`${path}:${finding.line} [${finding.rule}]`)
      }
    }
    expect(
      offenders,
      [
        "Display times through ui-kit's useFormatInstant instead of a fixed or implicit zone:",
        ...offenders,
      ].join('\n'),
    ).toEqual([])
  })
})

describe('the guard detector itself', () => {
  const rulesIn = (code: string) =>
    findViolations(code).map((finding) => finding.rule)
  const rawImport = "import { formatInstant } from '@speed/i18n'\n"

  it('reports every forbidden form', () => {
    expect(
      rulesIn(
        rawImport + "formatInstant(value, language, { timeZone: 'UTC' })",
      ),
    ).toContain(RULES.hardCodedZone)
    expect(
      rulesIn(
        rawImport + "formatInstant(value, language, { timeZone: 'Etc/UTC' })",
      ),
    ).toContain(RULES.hardCodedZone)
    expect(rulesIn(`${rawImport}formatInstant(value, language)`)).toContain(
      RULES.missingZone,
    )
    expect(
      rulesIn(
        `${rawImport}formatInstant(value, language, { style: "short" })`,
      ),
    ).toContain(RULES.missingZone)
    expect(rulesIn('value.toLocaleString()')).toContain(RULES.localeString)
    expect(rulesIn('value.toLocaleDateString()')).toContain(RULES.localeString)
    expect(rulesIn('value.toLocaleTimeString()')).toContain(RULES.localeString)
    expect(
      rulesIn("new Intl.DateTimeFormat('en-US', { timeZone: zone })"),
    ).toContain(RULES.dateTimeFormat)
    expect(rulesIn("Intl.DateTimeFormat('en-US', {})")).toContain(
      RULES.dateTimeFormat,
    )
  })

  it('leaves the sanctioned forms alone', () => {
    expect(
      rulesIn(`${rawImport}formatInstant(value, language, { timeZone })`),
    ).toEqual([])
    expect(
      rulesIn(
        `${rawImport}formatInstant(value, language, { timeZone, style: "short" })`,
      ),
    ).toEqual([])
    // The hook-bound formatter shares the name but carries the zone
    // already; its second argument is a style, not a language.
    expect(
      rulesIn(
        "const formatInstant = useFormatInstant()\n" +
          "formatInstant(value, { style: 'long' })",
      ),
    ).toEqual([])
    expect(rulesIn('const formatInstant = useFormatInstant()')).toEqual([])
    expect(rulesIn('formatInstant(value)')).toEqual([])
    expect(rulesIn("new Intl.NumberFormat('en-US')")).toEqual([])
    expect(
      rulesIn("new Intl.DisplayNames(['en'], { type: 'region' })"),
    ).toEqual([])
    expect(rulesIn("// timeZone: 'UTC' is banned\nconst x = 1")).toEqual([])
    expect(
      rulesIn("/* new Intl.DateTimeFormat is banned */ const x = 1"),
    ).toEqual([])
  })

  it('leaves prose that quotes a banned form alone', () => {
    // A message quoting an import binds nothing, so the hook-bound call
    // beside it stays sanctioned.
    expect(
      rulesIn(
        'const help = "import { formatInstant } from \'@speed/i18n\'"\n' +
          'const formatInstant = useFormatInstant()\n' +
          "formatInstant(value, { style: 'long' })",
      ),
    ).toEqual([])
    expect(rulesIn('const hint = `write timeZone: \'UTC\' nowhere`')).toEqual(
      [],
    )
    expect(rulesIn("const hint = \"never pass timeZone: 'GMT'\"")).toEqual([])
  })

  it('reports a banned form inside a template interpolation', () => {
    expect(rulesIn('const s = `at ${value.toLocaleString()}`')).toContain(
      RULES.localeString,
    )
    expect(
      rulesIn(
        'const s = `at ${new Intl.DateTimeFormat("en-US", { timeZone }).format(value)}`',
      ),
    ).toContain(RULES.dateTimeFormat)
    // Template text merely naming a banned form is not a call.
    expect(
      rulesIn('const s = `toLocaleString and Intl.DateTimeFormat are banned`'),
    ).toEqual([])
  })

  it('reports the raw formatInstant behind an alias or namespace import', () => {
    expect(
      rulesIn(
        "import { formatInstant as fmt } from '@speed/i18n'\n" +
          'fmt(value, language)',
      ),
    ).toContain(RULES.missingZone)
    expect(
      rulesIn(
        "import * as i18n from '@speed/i18n'\n" +
          'i18n.formatInstant(value, language)',
      ),
    ).toContain(RULES.missingZone)
    // The same reach with the zone named is sanctioned.
    expect(
      rulesIn(
        "import { formatInstant as fmt } from '@speed/i18n'\n" +
          'fmt(value, language, { timeZone })',
      ),
    ).toEqual([])
    expect(
      rulesIn(
        "import * as i18n from '@speed/i18n'\n" +
          'i18n.formatInstant(value, language, { timeZone })',
      ),
    ).toEqual([])
    // Another module's namespace is not the raw function.
    expect(
      rulesIn(
        "import * as other from '@speed/other'\n" +
          'other.formatInstant(value, language)',
      ),
    ).toEqual([])
  })

  it('reports a fixed zone named as a timeZone value', () => {
    expect(rulesIn("const o = { timeZone: zone ?? 'UTC' }")).toContain(
      RULES.hardCodedZone,
    )
    expect(rulesIn("const o = { timeZone: 'GMT' }")).toContain(
      RULES.hardCodedZone,
    )
    expect(rulesIn("const o = { timeZone: 'Etc/GMT+8' }")).toContain(
      RULES.hardCodedZone,
    )
    expect(rulesIn("const o = { timeZone: 'Etc/GMT' }")).toContain(
      RULES.hardCodedZone,
    )
    expect(
      rulesIn('<DisplayTimeZoneProvider timeZone={"GMT"} />'),
    ).toContain(RULES.hardCodedZone)
    expect(rulesIn("const o = { timeZone: 'Asia/Shanghai' }")).toContain(
      RULES.hardCodedZone,
    )
    // A name bound to a zone literal is the same bypass whether it is the
    // option's value or the shorthand property.
    expect(
      rulesIn("const zone = 'Asia/Shanghai'\nconst o = { timeZone: zone }"),
    ).toContain(RULES.hardCodedZone)
    expect(
      rulesIn("const timeZone = stored ?? 'UTC'\nconst o = { timeZone }"),
    ).toContain(RULES.hardCodedZone)
    expect(
      rulesIn(
        `${rawImport}const zone = 'UTC'\nformatInstant(value, language, { timeZone: zone })`,
      ),
    ).toContain(RULES.hardCodedZone)
    expect(
      rulesIn("const zone = 'UTC'\n<Provider timeZone={zone} />"),
    ).toContain(RULES.hardCodedZone)
    // A zone from the resolution chain is not a literal.
    expect(rulesIn('const o = { timeZone: zone }')).toEqual([])
    expect(rulesIn('<Provider timeZone={resolvedZone} />')).toEqual([])
    expect(rulesIn('const o = { timeZone: resolveTimeZone(stored) }')).toEqual(
      [],
    )
    // A non-zone string in a neighbouring member is not a zone literal.
    expect(rulesIn("const o = { timeZone: zone, style: 'short' }")).toEqual([])
    // A zone-shaped string that is not the option's value is not a fixed
    // zone: a bare literal elsewhere in the file, a comparison operand, and
    // an argument of another call.
    expect(rulesIn("const k = 'Etc/UTC'")).toEqual([])
    expect(
      rulesIn("const o = { timeZone: zone === 'UTC' ? undefined : zone }"),
    ).toEqual([])
    expect(rulesIn("const o = { timeZone: lookup('a/b') }")).toEqual([])
    // A name resolved by the chain stays sanctioned through the shorthand.
    expect(
      rulesIn("const timeZone = resolveTimeZone(stored)\nconst o = { timeZone }"),
    ).toEqual([])
  })

  it('reports a shorthand zone that follows another member', () => {
    // The shorthand is a zone use wherever it sits in the object, not only
    // as the first member.
    expect(
      rulesIn(
        `${rawImport}const timeZone = stored ?? 'UTC'\n` +
          "formatInstant(value, language, { style: 'long', timeZone })",
      ),
    ).toContain(RULES.hardCodedZone)
  })

  it('reports the shorthand at the identifier, not the preceding comma', () => {
    // The object spans lines, so the comma before the shorthand and the
    // shorthand itself sit on different ones: the finding must name the
    // line of `timeZone`, which is where the zone use is.
    expect(
      findViolations(
        `${rawImport}const timeZone = stored ?? 'UTC'\n` +
          'formatInstant(value, language, {\n' +
          "  style: 'long',\n" +
          '  timeZone,\n' +
          '})',
      ),
    ).toEqual([{ rule: RULES.hardCodedZone, line: 5 }])
  })

  it('reports a zone from an object destructuring default', () => {
    // Name tracking does not see a destructured binding. These cases pass
    // because the declaration line's own `timeZone` key is what the
    // property and option rules catch, not the binding.
    expect(
      rulesIn(
        `${rawImport}const { timeZone = 'UTC' } = props\n` +
          'formatInstant(value, language, { timeZone })',
      ),
    ).toContain(RULES.hardCodedZone)
    expect(
      rulesIn(
        `${rawImport}const { timeZone: zone = 'UTC' } = props\n` +
          'formatInstant(value, language, { timeZone: zone })',
      ),
    ).toContain(RULES.hardCodedZone)
    // A destructuring binding with no zone literal is not tainted.
    expect(
      rulesIn(
        `${rawImport}const { timeZone: zone } = props\n` +
          'formatInstant(value, language, { timeZone: zone })',
      ),
    ).toEqual([])
  })

  it('reports a literal zone passed positionally to a wall-clock conversion', () => {
    const both =
      "import { instantToWallClock, wallClockToInstant } from '@speed/i18n'\n"
    expect(rulesIn(`${both}instantToWallClock(value, 'UTC')`)).toContain(
      RULES.literalPositionalZone,
    )
    expect(
      rulesIn(`${both}wallClockToInstant(clock, 'Asia/Shanghai')`),
    ).toContain(RULES.literalPositionalZone)
    // An alias or namespace reaches the same second parameter.
    expect(
      rulesIn(
        "import { wallClockToInstant as toInstant } from '@speed/i18n'\n" +
          "toInstant(clock, 'UTC')",
      ),
    ).toContain(RULES.literalPositionalZone)
    expect(
      rulesIn(
        "import * as i18n from '@speed/i18n'\n" +
          "i18n.wallClockToInstant(clock, 'UTC')",
      ),
    ).toContain(RULES.literalPositionalZone)
    // A name bound to a zone literal reaches the same second parameter.
    expect(
      rulesIn(`${both}const zone = 'UTC'\nwallClockToInstant(clock, zone)`),
    ).toContain(RULES.literalPositionalZone)
    // A zone resolved by the chain is sanctioned.
    expect(rulesIn(`${both}instantToWallClock(value, timeZone)`)).toEqual([])
    expect(rulesIn(`${both}wallClockToInstant(clock, resolved.timeZone)`)).toEqual(
      [],
    )
    // An empty string names no zone, so it is not a literal zone.
    expect(rulesIn(`${both}wallClockToInstant(clock, '')`)).toEqual([])
  })
})
