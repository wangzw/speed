import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const manifestPath = join(
  dirname(fileURLToPath(import.meta.url)),
  '..',
  'package.json',
)
const manifest = JSON.parse(readFileSync(manifestPath, 'utf8')) as {
  readonly peerDependencies?: Readonly<Record<string, string>>
  readonly peerDependenciesMeta?: Readonly<
    Record<string, Readonly<{ optional?: boolean }>>
  >
}

describe('package manifest', () => {
  it('declares no react peer', () => {
    expect(manifest.peerDependencies?.react).toBeUndefined()
  })

  it('carries no optional peer marker for react', () => {
    expect(manifest.peerDependenciesMeta?.react).toBeUndefined()
  })
})
