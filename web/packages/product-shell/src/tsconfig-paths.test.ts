/**
 * Structural guard: this package's tsconfig `paths` covers every `@speed/*`
 * specifier its program imports.
 *
 * tsconfig cannot import web/scripts/speed-aliases.mjs, so the paths section
 * is a hand-copied projection of the shared map. A hand copy drifts silently:
 * a sibling adds a subpath import, nothing here notices, and the failure lands
 * on `tsc -p tsconfig.json` only in a fresh checkout, where the siblings'
 * published "types" point into an uncommitted dist/ (that is how the missing
 * `@speed/auth-core/webauthn` entry stayed hidden). This suite walks the
 * program the typecheck compiles -- every source file under src/ and
 * test-utils/, every specifier those files and the sibling sources they reach
 * import -- and requires a paths entry for each one.
 */

import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, extname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import { speedTsconfigPaths } from '../../../scripts/speed-aliases.mjs'

const packageDir = join(dirname(fileURLToPath(import.meta.url)), '..')

/** Strip JSON comments so the tsconfig (which documents its choices) parses.
 * The scanner tracks strings because a path value such as "../x/y" is not a
 * comment; the file carries no block comments. */
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

/** The specifiers a source file imports, from static and dynamic imports and
 * re-exports. Prose mentions of a specifier (jsdoc) are not imports and do
 * not count. */
function importedSpecifiers(source: string): string[] {
  const found: string[] = []
  const pattern = /(?:\bfrom\s*|\bimport\s*\(?\s*)['"]([^'"]+)['"]/g
  for (const match of source.matchAll(pattern)) {
    const specifier = match[1]
    if (specifier !== undefined) found.push(specifier)
  }
  return found
}

/** Every .ts/.tsx file under the given directory (recursive). */
function sourceFiles(directory: string): string[] {
  const files: string[] = []
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) files.push(...sourceFiles(path))
    else if (/\.(?:ts|tsx)$/.test(entry.name)) files.push(path)
  }
  return files
}

/** Resolve an import path the way the bundler resolution mode does: the
 * source spelling may carry a .js extension tsc maps onto .ts/.tsx. */
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

/** Walk the program's import graph and collect every @speed/* specifier it
 * reaches, plus the specifiers the tsconfig paths failed to cover. */
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
    // A walk that found nothing would pass vacuously; this package imports
    // its siblings everywhere.
    expect(specifiers.size).toBeGreaterThan(0)
    expect(unresolved).toEqual([])
  })

  it('projects the shared map without extra entries', () => {
    const projected = speedTsconfigPaths(packageDir)
    const declared = Object.entries(paths)
    // Every declared entry names what the map names, at the same path.
    for (const [specifier, target] of declared) {
      expect(target).toEqual(projected[specifier])
    }
    // Nothing outside the map: a typo or a leftover entry fails here rather
    // than resolving to a stale file. (The map's order among specifiers that
    // are not prefixes of one another is free; the prefix rule is checked
    // below.)
    expect(new Set(Object.keys(paths))).toEqual(
      new Set(
        Object.keys(projected).filter(
          (specifier) => paths[specifier] !== undefined,
        ),
      ),
    )

    // The order rule itself: a subpath entry must precede its prefix.
    const order = Object.keys(paths)
    for (const specifier of order) {
      for (const other of order) {
        if (other !== specifier && other.startsWith(`${specifier}/`)) {
          expect(order.indexOf(other)).toBeLessThan(order.indexOf(specifier))
        }
      }
    }

    // Every map entry is a real file, so a moved source fails here rather
    // than in a standalone typecheck.
    for (const target of Object.values(paths)) {
      expect(resolveSource(resolve(packageDir, target[0]!))).not.toBeNull()
    }
  })
})
