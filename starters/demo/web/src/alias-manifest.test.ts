/**
 * The app's tsconfig paths pinned against the workspace's single package map
 * (web/scripts/speed-aliases.mjs).
 *
 * The vite and vitest configs import the map directly, so those legs cannot
 * drift by construction; tsconfig cannot import, so this suite closes the
 * remaining leg: the paths section must equal the map's tsconfig projection for
 * this directory's depth, both directions, so a target a move invalidated or a
 * specifier the map added without the tsconfig following fails here instead of
 * in a standalone typecheck.
 */

import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'
import { describe, expect, it } from 'vitest'
import { speedTsconfigPaths } from '../../../../web/scripts/speed-aliases.mjs'

const appDir = join(dirname(fileURLToPath(import.meta.url)), '..')

describe('the app tsconfig paths mirror the workspace package map', () => {
  it('equals the map projected for this app', () => {
    const tsconfigPath = join(appDir, 'tsconfig.json')
    const parsed = ts.parseConfigFileTextToJson(
      tsconfigPath,
      readFileSync(tsconfigPath, 'utf8'),
    )
    const paths = (
      parsed.config as
        | { compilerOptions?: { paths?: Record<string, string[]> } }
        | undefined
    )?.compilerOptions?.paths
    expect(paths).toEqual(speedTsconfigPaths(appDir))
  })
})
