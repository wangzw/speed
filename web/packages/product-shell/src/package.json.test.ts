/**
 * The package manifest's peer-declaration contract, pinned by the suite.
 *
 * The workspace convention (ui-kit, auth-ui, layout-kit, the reference
 * app) is that every package whose dependency surface statically pulls
 * react-hook-form into a host's build re-declares it as a peer at the
 * shared ^7.0.0 range, so the requirement surfaces at every level of the
 * dependency chain instead of only at the package that imports it: a
 * host that installs @speed/product-shell renders the auth-ui family the
 * shell's sign-in slot pairs with (auth-ui's barrel statically imports
 * useForm), so react-hook-form must be a declared peer of this package
 * too. The devDependency pins the workspace's version of each shared
 * library (the one its sibling packages pin) so the package's own suite
 * resolves the peer against a real install of the same version the
 * workspace tests everywhere else; the expected value is read from a
 * sibling manifest, so a dependency bump moves every package together
 * without editing this suite.
 *
 * The manifest is read from disk, never imported as a module: vite
 * refuses to resolve a package's own package.json through the module
 * graph (the locale bundles beside it import fine as JSON; the manifest
 * is special-cased). Path derivation follows api-client's README-drift
 * test: fileURLToPath(import.meta.url) plus node:path joins -- the
 * jsdom environment's global URL constructor is not Node's, so a
 * relative URL resolution against the file: base is not reliable.
 */

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
// The sibling packages that carry the workspace's pin of each shared
// library: ui-kit owns the form family, api-sdk the query runtime.
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
    // The app-entry assembly ships as the "./bootstrap" subpath (the
    // api-sdk "./runtime" pattern) so the main entry's dependency
    // floor stays what the docs promise; the subpath must point at the
    // dist pair the package build emits from src/bootstrap.tsx.
    expect(manifest.exports?.['./bootstrap']).toEqual({
      types: './dist/bootstrap.d.ts',
      import: './dist/bootstrap.js',
    })
  })

  it('declares the packages the bootstrap entry imports, and the query client as a host-supplied peer', () => {
    // The bootstrap entry imports @speed/api-client (store + client),
    // @speed/api-sdk (the runtime seam) and @speed/ui-kit (the theme
    // provider) as regular dependencies -- all three already in the
    // main entry's transitive closure -- and react-query as a peer:
    // the host supplies the query library, exactly as it does for
    // @speed/api-sdk.
    expect(manifest.dependencies?.['@speed/api-client']).toBe('workspace:*')
    expect(manifest.dependencies?.['@speed/api-sdk']).toBe('workspace:*')
    expect(manifest.dependencies?.['@speed/ui-kit']).toBe('workspace:*')
    expect(manifest.peerDependencies?.['@tanstack/react-query']).toBe('^5.0.0')
    const pinned = apiSdkManifest.devDependencies?.['@tanstack/react-query']
    expect(pinned).toMatch(/^\^5\.\d+\.\d+$/)
    expect(manifest.devDependencies?.['@tanstack/react-query']).toBe(pinned)
  })
})
