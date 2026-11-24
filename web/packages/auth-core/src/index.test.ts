import { describe, expect, it } from 'vitest'

import manifest from '../package.json'
import * as entry from './index.js'
import * as otpauth from './otpauth.js'
import * as webauthn from './webauthn.js'

describe('package manifest', () => {
  it('exports the main entry built from src', () => {
    expect(manifest.exports?.['.']).toEqual({
      types: './dist/index.d.ts',
      import: './dist/index.js',
    })
  })

  it('exports the otpauth parser on its own subpath', () => {
    expect(manifest.exports?.['./otpauth']).toEqual({
      types: './dist/otpauth.d.ts',
      import: './dist/otpauth.js',
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

  it('keeps the otpauth parser off the main entry', () => {
    expect('parseOtpauth' in entry).toBe(false)
    expect('formatSecretForDisplay' in entry).toBe(false)
  })

  it('delivers the parser on the otpauth entry', () => {
    expect(otpauth.parseOtpauth).toBeTypeOf('function')
    expect(otpauth.formatSecretForDisplay).toBeTypeOf('function')
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
