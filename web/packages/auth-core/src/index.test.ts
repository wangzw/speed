/**
 * The entry surface and the manifest's subpath contract, pinned by the suite.
 *
 * The package publishes two entries: the main one is the headless session
 * state machine and its hooks and must stay free of browser-credential code;
 * `./webauthn` carries the passkey ceremony driver, which is the one place
 * that touches `navigator.credentials`. A host that cannot reach the driver
 * through the documented subpath has to reimplement it, so the mapping is
 * asserted here rather than left to the build.
 */

import { describe, expect, it } from 'vitest'

import manifest from '../package.json'
import * as entry from './index.js'
import * as webauthn from './webauthn.js'

describe('package manifest', () => {
  it('exports the main entry built from src', () => {
    expect(manifest.exports?.['.']).toEqual({
      types: './dist/index.d.ts',
      import: './dist/index.js',
    })
  })

  it('exports the passkey ceremony driver on its own subpath', () => {
    expect(manifest.exports?.['./webauthn']).toEqual({
      types: './dist/webauthn.d.ts',
      import: './dist/webauthn.js',
    })
  })
})

describe('entry surface', () => {
  it('exposes the step-up gate and the tenant-admission refusal', () => {
    expect(entry.useStepUpGate).toBeTypeOf('function')
    expect(entry.isTenantAdmissionRefusal).toBeTypeOf('function')
  })

  it('creates a session carrying the anonymous registration operation', () => {
    expect(entry.createAuthSession().register).toBeTypeOf('function')
  })

  it('keeps the browser-credential driver off the main entry', () => {
    expect('runWebauthn' in entry).toBe(false)
    expect('webauthnStepOf' in entry).toBe(false)
    expect('WebauthnError' in entry).toBe(false)
  })

  it('delivers the ceremony driver on the webauthn entry', () => {
    expect(webauthn.runWebauthn).toBeTypeOf('function')
    expect(webauthn.webauthnStepOf).toBeTypeOf('function')
    expect(webauthn.WebauthnError).toBeTypeOf('function')
  })
})
