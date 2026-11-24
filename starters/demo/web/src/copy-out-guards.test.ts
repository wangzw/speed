import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const appDir = join(dirname(fileURLToPath(import.meta.url)), '..')
const srcDir = join(appDir, 'src')
const webRoot = join(resolve(appDir, '../../..'), 'web')

interface PackageManifest {
  dependencies?: Record<string, string>
  devDependencies?: Record<string, string>
  peerDependencies?: Record<string, string>
  optionalDependencies?: Record<string, string>
}

const manifest = JSON.parse(
  readFileSync(join(appDir, 'package.json'), 'utf8'),
) as PackageManifest

function sourceFiles(dir: string): string[] {
  const files: string[] = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) files.push(...sourceFiles(path))
    else if (/\.tsx?$/.test(entry.name)) files.push(path)
  }
  return files
}

function isTestFile(path: string): boolean {
  return /\.(test|spec)\.tsx?$/.test(path)
}

function importedSpecifiers(source: string): string[] {
  const patterns = [
    /\bfrom\s*['"]([^'"]+)['"]/g,
    /\bimport\s*\(\s*['"]([^'"]+)['"]\s*\)/g,
    /\bimport\s+['"]([^'"]+)['"]/g,
    /\bdeclare\s+module\s*['"]([^'"]+)['"]/g,
  ]
  const specifiers: string[] = []
  for (const pattern of patterns) {
    for (const match of source.matchAll(pattern)) {
      if (match[1] !== undefined) specifiers.push(match[1])
    }
  }
  return specifiers
}

function packageName(specifier: string): string {
  return specifier.split('/').slice(0, 2).join('/')
}

const REPOSITORY_ONLY_SUITE = 'alias-manifest.test.ts'

describe('the manifest installs from a registry', () => {
  it('carries no workspace-protocol specifier in any dependency block', () => {
    const blocks = [
      manifest.dependencies,
      manifest.devDependencies,
      manifest.peerDependencies,
      manifest.optionalDependencies,
    ]
    const offenders: string[] = []
    for (const block of blocks) {
      for (const [name, range] of Object.entries(block ?? {})) {
        if (range.includes('workspace:')) offenders.push(`${name}: ${range}`)
      }
    }
    expect(offenders).toEqual([])
  })
})

describe('every runtime @speed import is a declared dependency', () => {
  it('declares the package each non-test source file imports', () => {
    const declared = new Set(Object.keys(manifest.dependencies ?? {}))
    const offenders: string[] = []
    for (const file of sourceFiles(srcDir)) {
      if (isTestFile(file)) continue
      const where = relative(appDir, file)
      for (const specifier of importedSpecifiers(readFileSync(file, 'utf8'))) {
        if (!specifier.startsWith('@speed/')) continue
        const name = packageName(specifier)
        if (!declared.has(name)) {
          offenders.push(`${where} imports ${specifier} (${name})`)
        }
      }
    }
    expect(offenders).toEqual([])
  })
})

describe('no suite reaches into the repository web/ tree', () => {
  it('keeps test imports relative to the app or the installed packages', () => {
    const offenders: string[] = []
    for (const file of sourceFiles(srcDir)) {
      if (!isTestFile(file)) continue
      if (file.endsWith(REPOSITORY_ONLY_SUITE)) continue
      const where = relative(appDir, file)
      for (const specifier of importedSpecifiers(readFileSync(file, 'utf8'))) {
        if (!specifier.startsWith('.')) continue
        const target = resolve(dirname(file), specifier)
        if (target === webRoot || target.startsWith(webRoot + sep)) {
          offenders.push(`${where} imports ${specifier}`)
        }
      }
    }
    expect(offenders).toEqual([])
  })
})

describe('no source file imports the workspace test-support package', () => {
  it('uses the app-local test-utils instead of @speed/test-utils', () => {
    const offenders: string[] = []
    for (const file of sourceFiles(srcDir)) {
      const where = relative(appDir, file)
      for (const specifier of importedSpecifiers(readFileSync(file, 'utf8'))) {
        if (
          specifier === '@speed/test-utils' ||
          specifier.startsWith('@speed/test-utils/')
        ) {
          offenders.push(`${where} imports ${specifier}`)
        }
      }
    }
    expect(offenders).toEqual([])
  })
})
