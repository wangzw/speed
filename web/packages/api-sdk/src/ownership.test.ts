/**
 * ownership.test.ts -- the platform package's generated surface is exactly the
 * platform half of the merged document.
 *
 * The merged document holds both halves: platform fragments plus the
 * host's own. Application-owned operations carry the tag the host's
 * `gen` command writes from the declaring module's provenance, and
 * `web/orval.config.ts` excludes that tag through orval 8.17.0's
 * `input.filters`. These cases read the committed merged document and the
 * committed generated surface, and hold one against the other in both
 * directions: no application-marked operation reaches the surface, and every
 * platform operation reaches it. Without the filter, a platform regeneration
 * from the merged document would publish the starter's own route; without the
 * second direction, a platform route added to the document would stay absent
 * from the committed surface while every check stayed green.
 *
 * Operations are matched by method and path, with the document's `{name}`
 * parameters and the generated url's `${name}` spelling brought into one form
 * first. orval 8.17.0 camelCases path parameters as it writes the url
 * (`{type_key}` in the document becomes `${typeKey}`), so the document side is
 * normalized the same way before the two are compared. A literal path lookup
 * misses every operation with a parameter and cannot tell two methods on one
 * path apart.
 *
 * The demo host declares no application-owned operation today, so the
 * document-side set is empty and the first case is trivially true against
 * the committed pair. The leak matcher's own fixtures below keep it
 * discriminating: they show it finds an application-marked operation in a
 * synthetic document, and the drift case holds the committed surface to
 * the platform operations in both directions.
 */

import { readFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const sourceDir = dirname(fileURLToPath(import.meta.url))
const packageDir = dirname(sourceDir)
const repoRoot = resolve(packageDir, '../../..')
const contractPath = join(repoRoot, 'contracts/api.json')
const generatedPath = join(sourceDir, 'index.ts')
const orvalConfigPath = join(repoRoot, 'web/orval.config.ts')

/** The tag `gen` writes on an operation an application module declares. */
const APPLICATION_TAG = 'speed.application'

interface MergedOperation {
  readonly tags?: readonly string[]
  readonly operationId?: string
}

interface MergedDocument {
  readonly paths: Record<string, Record<string, MergedOperation>>
}

/** One operation of the merged document, reduced to what the cases compare. */
interface Operation {
  /** Upper-case HTTP method. */
  readonly method: string
  /** The contract's path, with parameters in orval's spelling. */
  readonly path: string
  readonly operationId: string
  readonly applicationOwned: boolean
}

/** `METHOD path`, the form both sides are indexed by. */
function label(operation: { method: string; path: string }): string {
  return `${operation.method} ${operation.path}`
}

/**
 * The spelling orval 8.17.0 gives a path parameter, mirrored from its
 * `getRoutePath` (`@orval/core`, src/getters/route.ts). `camel` lowercases an
 * all-caps name, splits it on every run of non-alphanumerics, capitalises each
 * word and decapitalises the first, so `type_key` becomes `typeKey` and an
 * already-camel name stays itself. `sanitize` then prefixes an ES5 keyword with
 * `_`; no path parameter in the document is one, so this mirror leaves that
 * step out.
 */
function orvalParameterName(name: string): string {
  const lower = /^[^a-z]+$/.test(name) ? name.toLowerCase() : name
  const words = lower.match(/[a-zA-Z0-9\u00c0-\u017f]+/g) ?? []
  const pascal = words.map((word) => word.charAt(0).toUpperCase() + word.slice(1)).join('')
  const camel = pascal.charAt(0).toLowerCase() + pascal.slice(1)
  return name.startsWith('_') ? `_${camel}` : camel
}

/** The contract path with its parameter names in the spelling orval writes. */
function toGeneratedPath(path: string): string {
  return path.replace(/\{([^}]+)\}/g, (_, name: string) => `{${orvalParameterName(name)}}`)
}

function documentOperations(document: MergedDocument): Operation[] {
  const operations: Operation[] = []
  for (const [path, methods] of Object.entries(document.paths)) {
    for (const [method, operation] of Object.entries(methods)) {
      operations.push({
        method: method.toUpperCase(),
        path: toGeneratedPath(path),
        operationId: operation.operationId ?? '',
        applicationOwned: operation.tags?.includes(APPLICATION_TAG) === true,
      })
    }
  }
  return operations
}

/**
 * The request url a generated call emits, brought back to the document's
 * spelling: orval writes a path parameter as `${name}`, the document as
 * `{name}`. The name is already in orval's spelling on both sides by then.
 */
function toContractPath(url: string): string {
  return url.replace(/\$\{([^}]+)\}/g, '{$1}')
}

/**
 * The `METHOD path` pairs the generated surface calls, read from the
 * `{url: `...`, method: '...'}` shape every generated call emits. orval emits
 * the url and the method adjacent, so a wrapped line still matches.
 */
function generatedCalls(source: string): Set<string> {
  const calls = new Set<string>()
  for (const match of source.matchAll(/\{url:\s*`([^`]+)`,\s*method:\s*'([A-Z]+)'/g)) {
    const url = match[1]
    const method = match[2]
    if (url !== undefined && method !== undefined) {
      calls.add(`${method} ${toContractPath(url)}`)
    }
  }
  return calls
}

/**
 * The function name orval derives from an operationId: the dot-separated
 * segments joined, each after the first capitalised. A contract operation with
 * no operationId has no derived name.
 */
function generatedFunctionName(operationId: string): string | undefined {
  if (operationId === '') return undefined
  const segments = operationId.split('.')
  if (segments.some((segment) => segment === '')) return undefined
  return segments
    .map((segment, index) => (index === 0 ? segment : segment.charAt(0).toUpperCase() + segment.slice(1)))
    .join('')
}

/**
 * The application-marked operations of a document that the generated source
 * carries, by either of the two matchers. An empty result is the property the
 * package needs; the fixtures below prove the matcher actually discriminates.
 */
function leaked(document: MergedDocument, source: string): string[] {
  const calls = generatedCalls(source)
  const found: string[] = []
  for (const operation of documentOperations(document)) {
    if (!operation.applicationOwned) continue
    const name = generatedFunctionName(operation.operationId)
    if (
      calls.has(label(operation)) ||
      (name !== undefined && source.includes(`export const ${name} =`))
    ) {
      found.push(label(operation))
    }
  }
  return found
}

/**
 * The platform operations the generated source does not call, and the calls it
 * makes to operations the document does not carry as platform operations. Both
 * are empty for a committed pair generated from the same document.
 */
function surfaceDrift(document: MergedDocument, source: string): { missing: string[]; extra: string[] } {
  const platform = documentOperations(document)
    .filter((operation) => !operation.applicationOwned)
    .map(label)
  const calls = generatedCalls(source)
  return {
    missing: platform.filter((operation) => !calls.has(operation)),
    extra: [...calls].filter((operation) => !platform.includes(operation)).sort(),
  }
}

function readContract(): MergedDocument {
  return JSON.parse(readFileSync(contractPath, 'utf8')) as MergedDocument
}

describe('the platform surface ownership split', () => {
  it('contains no application-marked operation', () => {
    const document = readContract()
    // The demo host declares none today, so the surface has nothing to leak;
    // the matcher cases below prove that this assertion still discriminates
    // for a document that does.
    expect(
      leaked(document, readFileSync(generatedPath, 'utf8')),
      `${generatedPath} carries application-owned operations; regenerate it with the platform filter in web/orval.config.ts`,
    ).toEqual([])
  })

  it('carries every platform operation and no other call', () => {
    const drift = surfaceDrift(readContract(), readFileSync(generatedPath, 'utf8'))
    expect(
      drift.missing,
      `${generatedPath} is missing platform operations of contracts/api.json; regenerate it with the command in web/packages/api-sdk/README.md`,
    ).toEqual([])
    expect(
      drift.extra,
      `${generatedPath} calls operations contracts/api.json does not carry as platform operations; regenerate the document and the surface together`,
    ).toEqual([])
  })

  it('excludes the application tag in the platform generation', () => {
    const config = readFileSync(orvalConfigPath, 'utf8')
    expect(config).toMatch(/mode:\s*'exclude'/)
    expect(config).toMatch(new RegExp(`tags:\\s*\\[\\s*'${APPLICATION_TAG}'\\s*\\]`))
  })
})

describe('the leak matcher', () => {
  const marked: MergedDocument = {
    paths: {
      '/api/v1/orgs/things/{id}': {
        get: { operationId: 'workspace.things.get', tags: [APPLICATION_TAG] },
      },
    },
  }

  it('finds an operation whose path has parameters', () => {
    const source = "speedRequest({url: `/api/v1/orgs/things/${id}`, method: 'GET', signal})"
    expect(leaked(marked, source)).toEqual(['GET /api/v1/orgs/things/{id}'])
  })

  it('finds a snake_case document parameter under the camelCase name orval writes', () => {
    const document: MergedDocument = {
      paths: {
        '/api/v1/orgs/things/{type_key}': {
          get: { operationId: 'workspace.things.get', tags: [APPLICATION_TAG] },
        },
      },
    }
    const source = "speedRequest({url: `/api/v1/orgs/things/${typeKey}`, method: 'GET', signal})"
    expect(leaked(document, source)).toEqual(['GET /api/v1/orgs/things/{typeKey}'])
  })

  it('finds an operation by its generated function name', () => {
    const source = 'export const workspaceThingsGet = (\n  id: string,\n) => {'
    expect(leaked(marked, source)).toEqual(['GET /api/v1/orgs/things/{id}'])
  })

  it('does not confuse two methods on one path', () => {
    const source = "speedRequest({url: `/api/v1/orgs/things/${id}`, method: 'DELETE', signal})"
    expect(leaked(marked, source)).toEqual([])
  })
})

describe('the path-parameter spelling', () => {
  it('derives the name orval derives', () => {
    expect(orvalParameterName('type_key')).toBe('typeKey')
    expect(orvalParameterName('typeKey')).toBe('typeKey')
    expect(orvalParameterName('id')).toBe('id')
  })
})

describe('the surface comparison', () => {
  it('reports a platform operation the generated surface does not carry, and a call the document does not', () => {
    const document: MergedDocument = {
      paths: { '/api/v1/things/{id}': { get: { operationId: 'things.get' } } },
    }
    const source = "speedRequest({url: `/api/v1/other`, method: 'GET', signal})"
    expect(surfaceDrift(document, source)).toEqual({
      missing: ['GET /api/v1/things/{id}'],
      extra: ['GET /api/v1/other'],
    })
  })

  it('holds a snake_case path parameter and the camelCase call orval emits to the same operation', () => {
    const document: MergedDocument = {
      paths: { '/api/v1/things/{type_key}': { get: { operationId: 'things.get' } } },
    }
    const source = "speedRequest({url: `/api/v1/things/${typeKey}`, method: 'GET', signal})"
    expect(surfaceDrift(document, source)).toEqual({ missing: [], extra: [] })
  })
})
