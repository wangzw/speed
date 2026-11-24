import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, extname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import { speedTsconfigPaths } from '../../../scripts/speed-aliases.mjs'

const packageDir = join(dirname(fileURLToPath(import.meta.url)), '..')

function stripJsonComments(source: string): string {
  let out = ''
  let inString = false
  let escaped = false
  for (let i = 0; i < source.length; i += 1) {
    const char = source[i]
    if (inString) {
      out += char
      if (escaped) escaped = false
      else if (char === '\\') escaped = true
      else if (char === '"') inString = false
      continue
    }
    if (char === '"') {
      inString = true
      out += char
      continue
    }
    if (char === '/' && source[i + 1] === '/') {
      while (i < source.length && source[i] !== '\n') i += 1
      out += '\n'
      continue
    }
    out += char
  }
  return out
}

const tsconfig = JSON.parse(
  stripJsonComments(readFileSync(join(packageDir, 'tsconfig.json'), 'utf8')),
) as { compilerOptions?: { paths?: Record<string, string[]> } }
const paths = tsconfig.compilerOptions?.paths ?? {}

function importedSpecifiers(source: string): string[] {
  const found: string[] = []
  const pattern = /(?:\bfrom\s*|\bimport\s*\(?\s*)['"]([^'"]+)['"]/g
  for (const match of source.matchAll(pattern)) {
    const specifier = match[1]
    if (specifier !== undefined) found.push(specifier)
  }
  return found
}

function sourceFiles(directory: string): string[] {
  const files: string[] = []
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) files.push(...sourceFiles(path))
    else if (/\.(?:ts|tsx)$/.test(entry.name)) files.push(path)
  }
  return files
}

function resolveSource(path: string): string | null {
  const stripped = path.replace(/\.jsx?$/, '')
  for (const candidate of [
    path,
    stripped,
    `${stripped}.ts`,
    `${stripped}.tsx`,
    join(stripped, 'index.ts'),
    join(stripped, 'index.tsx'),
  ]) {
    if (extname(candidate) !== '' && existsSync(candidate) && statSync(candidate).isFile()) {
      return candidate
    }
  }
  return null
}


function walkProgram(): { specifiers: Set<string>; unresolved: string[] } {
  const specifiers = new Set<string>()
  const unresolved: string[] = []
  const seen = new Set<string>()
  const queue = [
    ...sourceFiles(join(packageDir, 'src')),
    ...sourceFiles(join(packageDir, 'test-utils')),
  ]

  while (queue.length > 0) {
    const file = queue.pop()
    if (file === undefined || seen.has(file)) continue
    seen.add(file)
    let source: string
    try {
      source = readFileSync(file, 'utf8')
    } catch {
      continue
    }
    for (const specifier of importedSpecifiers(source)) {
      if (specifier.startsWith('.')) {
        const target = resolveSource(resolve(dirname(file), specifier))
        if (target !== null) queue.push(target)
        continue
      }
      if (!specifier.startsWith('@speed/')) continue
      specifiers.add(specifier)
      const mapped = paths[specifier]?.[0]
      if (mapped === undefined) {
        unresolved.push(`${relative(packageDir, file)} imports ${specifier}`)
        continue
      }
      const target = resolveSource(resolve(packageDir, mapped))
      if (target !== null) queue.push(target)
    }
  }

  return { specifiers, unresolved }
}

describe('the package tsconfig paths mirror the workspace package map', () => {
  it('resolves every @speed/* specifier the program imports', () => {
    const { specifiers, unresolved } = walkProgram()
    expect(specifiers.size).toBeGreaterThan(0)
    expect(unresolved).toEqual([])
  })

  it('projects the shared map without extra entries', () => {
    const projected = speedTsconfigPaths(packageDir)
    const declared = Object.entries(paths)
    for (const [specifier, target] of declared) {
      expect(target).toEqual(projected[specifier])
    }
    expect(new Set(Object.keys(paths))).toEqual(
      new Set(
        Object.keys(projected).filter(
          (specifier) => paths[specifier] !== undefined,
        ),
      ),
    )

    const order = Object.keys(paths)
    for (const specifier of order) {
      for (const other of order) {
        if (other !== specifier && other.startsWith(`${specifier}/`)) {
          expect(order.indexOf(other)).toBeLessThan(order.indexOf(specifier))
        }
      }
    }

    for (const target of Object.values(paths)) {
      expect(resolveSource(resolve(packageDir, target[0]!))).not.toBeNull()
    }
  })
})
