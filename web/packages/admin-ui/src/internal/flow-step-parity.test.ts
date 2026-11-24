import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const packagesDir = join(
  dirname(fileURLToPath(import.meta.url)),
  '..',
  '..',
  '..',
)

const webDir = join(packagesDir, '..')

interface Copy {
  readonly name: string
  readonly source: string
  readonly fallthroughKind: string
}

interface Contract {
  readonly kinds: readonly string[]
  readonly answerFields: readonly string[]
  readonly primitives: readonly string[]
  readonly webauthnHelpers: readonly string[]
}

type Field = 'kinds' | 'answerFields' | 'primitives' | 'webauthnHelpers'

const COPIES: readonly Copy[] = [
  { name: 'auth-ui', source: readSource('auth-ui'), fallthroughKind: 'redirect' },
  { name: 'account-ui', source: readSource('account-ui'), fallthroughKind: 'challenge' },
  { name: 'admin-ui', source: readSource('admin-ui'), fallthroughKind: 'challenge' },
]

function readSource(name: string): string {
  return readFileSync(
    join(packagesDir, name, 'src', 'internal', 'flow-step.tsx'),
    'utf8',
  )
}

interface SharedPiece {
  readonly module: string
  readonly name: string
  readonly description: string
}

const SHARED_TOTP_PIECES: readonly SharedPiece[] = [
  {
    module: '@speed/ui-kit',
    name: 'TotpEnrollmentPanel',
    description: 'the TOTP enrolment panel',
  },
  {
    module: '@speed/auth-core/otpauth',
    name: 'parseOtpauth',
    description: 'the otpauth parser',
  },
  {
    module: '@speed/auth-core/otpauth',
    name: 'formatSecretForDisplay',
    description: 'the base32 display formatter',
  },
]

function importedNames(source: string, module: string): string[] {
  const escaped = module.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const pattern = new RegExp(
    `import\\s*\\{([^}]*)\\}\\s*from\\s*['"]${escaped}['"]`,
    'g',
  )
  const names: string[] = []
  for (const match of source.matchAll(pattern)) {
    for (const entry of (match[1] ?? '').split(',')) {
      const trimmed = entry.trim()
      if (trimmed === '' || trimmed.startsWith('type ')) {
        continue
      }
      const name = trimmed.split(/\s+as\s+/)[0]?.trim()
      if (name !== undefined && name !== '') {
        names.push(name)
      }
    }
  }
  return names
}

interface ScannedFile {
  readonly path: string
  readonly source: string
}

const SCANNED_EXTENSIONS = [
  '.ts',
  '.tsx',
  '.mts',
  '.cts',
  '.js',
  '.jsx',
  '.mjs',
  '.cjs',
] as const

function packageSourceFiles(): ScannedFile[] {
  const files: ScannedFile[] = []
  const walk = (dir: string): void => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      if (entry.name === 'node_modules' || entry.name === 'dist') {
        continue
      }
      const path = join(dir, entry.name)
      if (entry.isDirectory()) {
        walk(path)
        continue
      }
      if (
        entry.name.endsWith('.d.ts') ||
        !SCANNED_EXTENSIONS.some((extension) => entry.name.endsWith(extension))
      ) {
        continue
      }
      files.push({ path, source: readFileSync(path, 'utf8') })
    }
  }
  walk(packagesDir)
  return files
}

const OTPAUTH_IMPLEMENTATION = join(
  packagesDir,
  'auth-core',
  'src',
  'otpauth.ts',
)

const QR_COMPONENT = join(
  packagesDir,
  'ui-kit',
  'src',
  'components',
  'TotpQrCode.tsx',
)

const OTPAUTH_FUNCTIONS = ['parseOtpauth', 'formatSecretForDisplay'] as const

function withoutComments(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/\/\/[^\n]*/g, '')
}

function localOtpauthFunctions(source: string): string[] {
  const code = withoutComments(source)
  return OTPAUTH_FUNCTIONS.filter(
    (name) =>
      new RegExp(
        `(?:export\\s+)?(?:async\\s+)?function\\s+${name}\\s*\\(`,
      ).test(code) ||
      new RegExp(
        `(?:export\\s+)?(?:const|let|var)\\s+${name}\\s*(?::[^=]+)?=`,
      ).test(code),
  )
}

function importsQrEncoder(source: string): boolean {
  const code = withoutComments(source)
  return (
    /(?:^|\n)\s*import\b[^\n]*['"]qrcode-generator['"]/.test(code) ||
    /\brequire\(\s*['"]qrcode-generator['"]\s*\)/.test(code) ||
    /\bimport\(\s*['"]qrcode-generator['"]\s*\)/.test(code)
  )
}

const DEPENDENCY_FIELDS = [
  'dependencies',
  'devDependencies',
  'peerDependencies',
  'optionalDependencies',
] as const

function repoRelative(path: string): string {
  return `web/${relative(webDir, path)}`.replace('web/../', '')
}

function packageManifests(): string[] {
  const manifests = readdirSync(packagesDir, { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => join(packagesDir, entry.name, 'package.json'))
    .filter((path) => existsSync(path))
  for (const extra of [
    join(webDir, 'package.json'),
    join(webDir, '..', 'starters', 'demo', 'web', 'package.json'),
  ]) {
    if (existsSync(extra)) {
      manifests.push(extra)
    }
  }
  return manifests
}

function switchKinds(source: string): string[] {
  const lines = source.split('\n')
  const start = lines.findIndex((line) =>
    /^\s*switch \(step\.next\.kind\) \{$/.test(line),
  )
  if (start === -1) {
    return []
  }
  const header = lines[start] ?? ''
  const indent = header.length - header.trimStart().length
  const kinds: string[] = []
  for (let i = start + 1; i < lines.length; i += 1) {
    const line = lines[i] ?? ''
    const lineIndent = line.length - line.trimStart().length
    if (line.trim() !== '' && lineIndent <= indent && line.trimStart().startsWith('}')) {
      break
    }
    const literal = /^\s*case '([a-z]+)':/.exec(line)?.[1]
    if (literal !== undefined) {
      kinds.push(literal)
    }
  }
  return kinds
}

function kindsOf(source: string, fallthroughKind: string): string[] {
  const kinds = new Set<string>(switchKinds(source))
  for (const match of source.matchAll(/step(?:\.next)?\.kind === '([a-z]+)'/g)) {
    const literal = match[1]
    if (literal !== undefined) {
      kinds.add(literal)
    }
  }
  kinds.add(fallthroughKind)
  return [...kinds].sort()
}

function answerFieldsOf(source: string): string[] {
  const fields = new Set<string>()
  for (const match of source.matchAll(/\{\s*(factor|kind|code|credential)\s*[:},]/g)) {
    const property = match[1]
    if (property !== undefined) {
      fields.add(property)
    }
  }
  return [...fields].sort()
}

const ACTION_PRIMITIVE = 'Button'

function primitivesOf(source: string): string[] {
  const primitives = new Set<string>()
  for (const match of source.matchAll(/import \w+ from '@mui\/material\/(\w+)'/g)) {
    const component = match[1]
    if (component !== undefined && component !== ACTION_PRIMITIVE) {
      primitives.add(component)
    }
  }
  return [...primitives].sort()
}

function webauthnHelpersOf(source: string): string[] {
  const imported = /import \{([^}]*)\} from '@speed\/auth-core\/webauthn'/.exec(source)?.[1]
  if (imported === undefined) {
    return []
  }
  return imported
    .split(',')
    .map((name) => name.trim())
    .filter((name) => name !== '')
    .sort()
}

function contractOf(copy: Copy): Contract {
  return {
    kinds: kindsOf(copy.source, copy.fallthroughKind),
    answerFields: answerFieldsOf(copy.source),
    primitives: primitivesOf(copy.source),
    webauthnHelpers: webauthnHelpersOf(copy.source),
  }
}

function driftMessage(field: Field): string {
  const groups = new Map<string, string[]>()
  for (const copy of COPIES) {
    const value = JSON.stringify(contractOf(copy)[field])
    const members = groups.get(value) ?? []
    members.push(copy.name)
    groups.set(value, members)
  }
  if (groups.size === 1) {
    return ''
  }
  const ranked = [...groups.entries()].sort(
    ([, left], [, right]) => right.length - left.length,
  )
  const lines = ranked.map(
    ([value, members]) => `  ${members.join(' and ')}: ${value}`,
  )
  const majority = ranked[0]
  const baseline = majority === undefined ? '' : majority[1].join(' and ')
  return (
    `the flow-step copies disagree on ${field} ` +
    `(majority: ${baseline});\n${lines.join('\n')}`
  )
}

describe('flow-step renderer copies', () => {
  it('renders the same set of step kinds', () => {
    expect(driftMessage('kinds')).toBe('')
  })

  it('reports the same answer properties', () => {
    expect(driftMessage('answerFields')).toBe('')
  })

  it('builds a step from the same render primitives', () => {
    expect(driftMessage('primitives')).toBe('')
  })

  it('drives the passkey ceremony through the same seam', () => {
    expect(driftMessage('webauthnHelpers')).toBe('')
  })
})

describe('shared TOTP pieces', () => {
  it('has every flow-step copy import the panel and the parser', () => {
    const missing: string[] = []
    for (const copy of COPIES) {
      for (const piece of SHARED_TOTP_PIECES) {
        if (!importedNames(copy.source, piece.module).includes(piece.name)) {
          missing.push(
            `${copy.name} is missing ${piece.description} ` +
              `(${piece.name} from ${piece.module})`,
          )
        }
      }
    }
    expect(missing).toEqual([])
  })

  it('keeps one otpauth implementation and one QR renderer in the workspace', () => {
    const violations: string[] = []
    for (const file of packageSourceFiles()) {
      const where = `web/${relative(webDir, file.path)}`
      if (file.path !== OTPAUTH_IMPLEMENTATION) {
        const local = localOtpauthFunctions(file.source)
        if (local.length > 0) {
          violations.push(
            `${where} defines a local ${local.join(', ')} implementation; ` +
              `the only one is web/${relative(webDir, OTPAUTH_IMPLEMENTATION)}`,
          )
        }
      }
      if (file.path !== QR_COMPONENT && importsQrEncoder(file.source)) {
        violations.push(
          `${where} imports qrcode-generator directly; ` +
            `only web/${relative(webDir, QR_COMPONENT)} may`,
        )
      }
    }
    expect(violations).toEqual([])
  })

  it('declares the QR encoder only in the ui-kit manifest', () => {
    const declaredIn: { readonly path: string; readonly field: string }[] = []
    for (const manifestPath of packageManifests()) {
      const manifest = JSON.parse(
        readFileSync(manifestPath, 'utf8'),
      ) as Record<string, unknown>
      for (const field of DEPENDENCY_FIELDS) {
        const dependencies = manifest[field]
        if (
          typeof dependencies === 'object' &&
          dependencies !== null &&
          Object.prototype.hasOwnProperty.call(
            dependencies,
            'qrcode-generator',
          )
        ) {
          declaredIn.push({ path: manifestPath, field })
        }
      }
    }
    const violations = declaredIn
      .filter(({ path }) => path !== join(packagesDir, 'ui-kit', 'package.json'))
      .map(
        ({ path, field }) =>
          `${repoRelative(path)} declares qrcode-generator in ` +
          `${field}; only web/packages/ui-kit/package.json may declare it`,
      )
    expect(violations).toEqual([])
    expect(
      declaredIn.some(
        ({ path }) => path === join(packagesDir, 'ui-kit', 'package.json'),
      ),
      'ui-kit must declare qrcode-generator for its QR component',
    ).toBe(true)
  })
})
