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

const APPLICATION_TAG = 'speed.application'

interface MergedOperation {
  readonly tags?: readonly string[]
  readonly operationId?: string
}

interface MergedDocument {
  readonly paths: Record<string, Record<string, MergedOperation>>
}

interface Operation {
  readonly method: string
  readonly path: string
  readonly operationId: string
  readonly applicationOwned: boolean
}

function label(operation: { method: string; path: string }): string {
  return `${operation.method} ${operation.path}`
}

function orvalParameterName(name: string): string {
  const lower = /^[^a-z]+$/.test(name) ? name.toLowerCase() : name
  const words = lower.match(/[a-zA-Z0-9\u00c0-\u017f]+/g) ?? []
  const pascal = words.map((word) => word.charAt(0).toUpperCase() + word.slice(1)).join('')
  const camel = pascal.charAt(0).toLowerCase() + pascal.slice(1)
  return name.startsWith('_') ? `_${camel}` : camel
}

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

function toContractPath(url: string): string {
  return url.replace(/\$\{([^}]+)\}/g, '{$1}')
}

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

function generatedFunctionName(operationId: string): string | undefined {
  if (operationId === '') return undefined
  const segments = operationId.split('.')
  if (segments.some((segment) => segment === '')) return undefined
  return segments
    .map((segment, index) => (index === 0 ? segment : segment.charAt(0).toUpperCase() + segment.slice(1)))
    .join('')
}

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
