import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

interface Manifest {
  readonly exports?: Readonly<Record<string, unknown>>
  readonly dependencies?: Readonly<Record<string, string>>
  readonly peerDependencies?: Readonly<Record<string, string>>
  readonly devDependencies?: Readonly<Record<string, string>>
}

const packageDir = join(dirname(fileURLToPath(import.meta.url)), '..')

function readManifest(dir: string): Manifest {
  return JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8')) as Manifest
}

const manifest = readManifest(packageDir)
const uiKitManifest = readManifest(join(packageDir, '..', 'ui-kit'))
const apiSdkManifest = readManifest(join(packageDir, '..', 'api-sdk'))

describe('package manifest', () => {
  it('declares react-hook-form as a peer at the workspace-wide range', () => {
    expect(manifest.peerDependencies?.['react-hook-form']).toBe('^7.0.0')
  })

  it('satisfies that peer from its own devDependencies at the workspace-pinned version', () => {
    const pinned = uiKitManifest.devDependencies?.['react-hook-form']
    expect(pinned).toMatch(/^\d+\.\d+\.\d+$/)
    expect(manifest.devDependencies?.['react-hook-form']).toBe(pinned)
  })

  it('exports the bootstrap entry as its own subpath, built from the same src', () => {
    expect(manifest.exports?.['./bootstrap']).toEqual({
      types: './dist/bootstrap.d.ts',
      import: './dist/bootstrap.js',
    })
  })

  it('declares the packages the bootstrap entry imports, and the query client as a host-supplied peer', () => {
    expect(manifest.dependencies?.['@speed/api-client']).toBe('workspace:*')
    expect(manifest.dependencies?.['@speed/api-sdk']).toBe('workspace:*')
    expect(manifest.dependencies?.['@speed/ui-kit']).toBe('workspace:*')
    expect(manifest.peerDependencies?.['@tanstack/react-query']).toBe('^5.0.0')
    const pinned = apiSdkManifest.devDependencies?.['@tanstack/react-query']
    expect(pinned).toMatch(/^\^5\.\d+\.\d+$/)
    expect(manifest.devDependencies?.['@tanstack/react-query']).toBe(pinned)
  })
})
