/**
 * ownership.test.ts -- the platform package's generated surface carries
 * only platform-owned operations.
 *
 * The merged document holds both halves: platform fragments plus the
 * host's own. Application-owned operations carry the tag the host's
 * `gen` command writes from the declaring module's provenance, and
 * `web/orval.config.ts` excludes that tag through orval 8.17.0's
 * `input.filters`. This case reads the committed merged document, finds
 * the marked operations, and asserts no generated call reaches any of
 * their paths. Without the filter, a platform regeneration from the
 * merged document would publish the starter's own route.
 *
 * The case also asserts the merged document has at least one marked
 * operation, so a vocabulary that silently stopped marking cannot make
 * the check vacuous.
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

/** The tag `gen` writes on an operation an application module declares. */
const APPLICATION_TAG = 'speed.application'

interface MergedOperation {
  readonly tags?: readonly string[]
}

/**
 * The paths of the operations the merged document marks as
 * application-owned. The path is what the generated code carries as the
 * request url; the platform surface contains none of them.
 */
function applicationPaths(): string[] {
  const document = JSON.parse(readFileSync(contractPath, 'utf8')) as {
    paths: Record<string, Record<string, MergedOperation>>
  }
  const paths: string[] = []
  for (const [path, methods] of Object.entries(document.paths)) {
    for (const operation of Object.values(methods)) {
      if (operation.tags?.includes(APPLICATION_TAG) === true) {
        paths.push(path)
        break
      }
    }
  }
  return paths
}

describe('the platform surface ownership split', () => {
  it('contains no application-marked operation', () => {
    const marked = applicationPaths()
    expect(marked.length).toBeGreaterThan(0)
    const generated = readFileSync(generatedPath, 'utf8')
    for (const path of marked) {
      expect(
        generated.includes(`\`${path}\``),
        `${generatedPath} carries the application-owned operation ${path}; regenerate it with the platform filter in web/orval.config.ts`,
      ).toBe(false)
    }
  })
})
