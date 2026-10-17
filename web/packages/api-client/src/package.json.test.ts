/**
 * The package manifest's dependency contract, pinned by the suite.
 *
 * The package is React-free: its single entry (client, errors, retry,
 * reporter, the token store, the event-stream opener) imports no react,
 * exports no hooks and adds no subpath, so the manifest declares no
 * react peer -- not even an optional one. npm peer declarations are
 * package-level, and an optional peer would still advertise a
 * React-shaped surface the package does not have. The absence is the
 * claim the suite pins.
 *
 * The manifest is read from disk, never imported as a module (the
 * product-shell precedent): path derivation via `fileURLToPath` plus
 * `node:path` joins, so the test works in any environment.
 */

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
