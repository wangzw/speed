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
 *
 * The workspace's private test-support entries are filtered out of that
 * projection: the app carries local copies under src/test-utils and must not
 * resolve @speed/test-utils at all, because a copied-out skeleton has no
 * workspace to link it from. This file itself is repository-only -- the
 * copy-out instructions delete it along with the alias configuration.
 */

import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'
import { describe, expect, it } from 'vitest'
import {
  SPEED_PACKAGE_MAP,
  speedTsconfigPaths,
} from '../../../../web/scripts/speed-aliases.mjs'

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
    const packageMap = SPEED_PACKAGE_MAP.filter(
      ({ specifier }) => !specifier.startsWith('@speed/test-utils/'),
    )
    expect(paths).toEqual(speedTsconfigPaths(appDir, packageMap))
  })
})
