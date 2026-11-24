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
