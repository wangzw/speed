import { readdirSync, readFileSync } from 'node:fs'
import type { Dirent } from 'node:fs'
import { dirname, join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const packagesDir = join(
  dirname(fileURLToPath(import.meta.url)),
  '..',
  '..',
  '..',
)

const repoRoot = join(packagesDir, '..', '..')

const demoSourceDir = join(repoRoot, 'starters', 'demo', 'web', 'src')

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

const TEST_FILE = /\.(test|spec)\.[cm]?[jt]sx?$/

const SKIPPED_DIRECTORIES = new Set(['dist', 'node_modules'])

const DEMO_TEST_UTILS_DIR = join(demoSourceDir, 'test-utils')

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

const RULES = {
  hardCodedZone: 'hard-coded time zone literal',
  missingZone: 'formatInstant without a timeZone option',
  localeString: 'toLocale*String display formatting',
  dateTimeFormat: 'bare Intl.DateTimeFormat construction',
  literalPositionalZone: 'literal time zone passed to a wall-clock conversion',
} as const

function collectSourceFiles(directory: string, files: string[]): void {
  let entries: Dirent[]
  try {
    entries = readdirSync(directory, { withFileTypes: true })
  } catch {
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

function displayPath(file: string): string {
  return relative(repoRoot, file).split(sep).join('/')
}

function lineOf(text: string, index: number): number {
  let line = 1
  for (let i = 0; i < index && i < text.length; i += 1) {
    if (text[i] === '\n') {
      line += 1
    }
  }
  return line
}

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

function isDeclaration(code: string, index: number): boolean {
  return /\bfunction\s*$/.test(code.slice(Math.max(0, index - 32), index))
}

interface Finding {
  readonly rule: string
  readonly line: number
}

interface I18nImports {
  readonly named: ReadonlyMap<string, string>
  readonly namespaces: ReadonlySet<string>
}

function i18nImports(content: string, codeOnly: string): I18nImports {
  const named = new Map<string, string>()
  const namespaces = new Set<string>()
  const statement =
    /import\s+([^;'"]*?)\s+from\s*['"]@speed\/i18n(?:\/[^'"]*)?['"]/g
  let match: RegExpExecArray | null
  while ((match = statement.exec(content)) !== null) {
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

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

interface CallArgument {
  readonly code: string
  readonly raw: string
}

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

/** Whether a string names a time zone rather than a style tier or other option value. */
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

function stringLiteralText(expression: string): string | null {
  const text = expression.trim()
  if (!isStringLiteral(text)) {
    return null
  }
  return text.slice(1, -1)
}

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

function reportTimeZoneLiterals(
  text: string,
  codeOnly: string,
  zoneNames: ReadonlySet<string>,
  findings: Finding[],
): void {
  const option = /\btimeZone\s*:\s*/g
  let match: RegExpExecArray | null
  while ((match = option.exec(text)) !== null) {
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
    const tainted = referencedZoneName(codeValue, zoneNames)
    if (tainted !== null) {
      const offset = codeValue.indexOf(tainted)
      findings.push({
        rule: RULES.hardCodedZone,
        line: lineOf(text, start + Math.max(0, offset)),
      })
    }
  }

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

  const zoneNames = zoneBoundNames(withStrings, codeOnly)
  reportTimeZoneLiterals(withStrings, codeOnly, zoneNames, findings)

  const imports = i18nImports(withStrings, codeOnly)
  reportRawFormatInstant(codeOnly, withStrings, imports, findings)
  reportLiteralConversionZones(
    codeOnly,
    withStrings,
    imports,
    zoneNames,
    findings,
  )

  return [
    ...new Map(findings.map((f) => [`${f.rule}:${f.line}`, f])).values(),
  ]
}

describe('display time zone guard', () => {
  const files = scannedFiles()
  const scanned = new Set(files.map(displayPath))

  it('scans the package and demo surfaces it means to guard', () => {
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
    expect(rulesIn('const o = { timeZone: zone }')).toEqual([])
    expect(rulesIn('<Provider timeZone={resolvedZone} />')).toEqual([])
    expect(rulesIn('const o = { timeZone: resolveTimeZone(stored) }')).toEqual(
      [],
    )
    expect(rulesIn("const o = { timeZone: zone, style: 'short' }")).toEqual([])
    expect(rulesIn("const k = 'Etc/UTC'")).toEqual([])
    expect(
      rulesIn("const o = { timeZone: zone === 'UTC' ? undefined : zone }"),
    ).toEqual([])
    expect(rulesIn("const o = { timeZone: lookup('a/b') }")).toEqual([])
    expect(
      rulesIn("const timeZone = resolveTimeZone(stored)\nconst o = { timeZone }"),
    ).toEqual([])
  })

  it('reports a shorthand zone that follows another member', () => {
    expect(
      rulesIn(
        `${rawImport}const timeZone = stored ?? 'UTC'\n` +
          "formatInstant(value, language, { style: 'long', timeZone })",
      ),
    ).toContain(RULES.hardCodedZone)
  })

  it('reports the shorthand at the identifier, not the preceding comma', () => {
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
    expect(
      rulesIn(`${both}const zone = 'UTC'\nwallClockToInstant(clock, zone)`),
    ).toContain(RULES.literalPositionalZone)
    expect(rulesIn(`${both}instantToWallClock(value, timeZone)`)).toEqual([])
    expect(rulesIn(`${both}wallClockToInstant(clock, resolved.timeZone)`)).toEqual(
      [],
    )
    expect(rulesIn(`${both}wallClockToInstant(clock, '')`)).toEqual([])
  })
})
