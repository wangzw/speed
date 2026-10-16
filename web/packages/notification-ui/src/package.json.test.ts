/**
 * The package manifest's dependency contract, pinned by the suite.
 *
 * The notification surfaces render over the host's React/MUI tree and
 * read their data through react-query hooks the host supplies, so react,
 * react-dom, @mui/material, @emotion/* and @tanstack/react-query are
 * peers. @speed/i18n, @speed/ui-kit are regular dependencies; @speed/auth-core
 * supplies the acting tenant for query-key namespacing; @speed/api-sdk binds
 * the generated notification operations; @speed/api-client supplies the
 * event-stream opener type (and its createClient's openEventStream).
 *
 * The manifest is read from disk, never imported as a module: vite refuses
 * to resolve a package's own package.json through the module graph.
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
  readonly exports?: Readonly<Record<string, unknown>>
  readonly dependencies?: Readonly<Record<string, string>>
  readonly peerDependencies?: Readonly<Record<string, string>>
}

describe('package manifest', () => {
  it('exports the main entry built from src', () => {
    expect(manifest.exports?.['.']).toEqual({
      types: './dist/index.d.ts',
      import: './dist/index.js',
    })
  })

  it('declares the framework peers the surfaces render over', () => {
    expect(manifest.peerDependencies?.['react']).toBe('^18.0.0 || ^19.0.0')
    expect(manifest.peerDependencies?.['react-dom']).toBe('^18.0.0 || ^19.0.0')
    expect(manifest.peerDependencies?.['@mui/material']).toBe('^9.0.0')
    expect(manifest.peerDependencies?.['@tanstack/react-query']).toBe('^5.0.0')
  })

  it('declares i18n, ui-kit, auth-core, api-sdk and api-client as dependencies', () => {
    expect(manifest.dependencies?.['@speed/i18n']).toBe('workspace:*')
    expect(manifest.dependencies?.['@speed/ui-kit']).toBe('workspace:*')
    expect(manifest.dependencies?.['@speed/auth-core']).toBe('workspace:*')
    expect(manifest.dependencies?.['@speed/api-sdk']).toBe('workspace:*')
    expect(manifest.dependencies?.['@speed/api-client']).toBe('workspace:*')
  })
})
